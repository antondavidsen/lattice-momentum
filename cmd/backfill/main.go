// cmd/backfill: fetches up to N years of historical OHLCV candles for all
// tickers currently in the database (plus benchmark indices and sector ETFs).
//
// Usage:
//
//	backfill [-ticker AAPL] [-years 2]
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log/slog"
	"net"
	"os"
	"time"

	"ai-stock-service/internal/bootstrap"
	"ai-stock-service/internal/repository"
	marketdata "ai-stock-service/internal/services/marketdata"
	"ai-stock-service/internal/services/marketdata/jobs"
	"ai-stock-service/internal/services/marketdata/provider"
)

func main() {
	ticker := flag.String("ticker", "", "backfill a single ticker (empty = all tickers + indices + sectors)")
	years := flag.Int("years", 2, "number of years of history to fetch")
	flag.Parse()
	os.Exit(runMain(ticker, years))
}

func runMain(ticker *string, years *int) int {
	b := bootstrap.MustStartup("backfill",
		bootstrap.WithoutDotenv(),
	)
	defer b.Pool.Close()

	cfg := b.Cfg
	pool := b.Pool
	logger := b.Logger
	ctx := b.Ctx

	// Warm up the TLS root certificate pool on the main goroutine BEFORE
	// spawning any concurrent workers. Go's x509 system roots are lazily
	// initialised via sync.Once; if multiple goroutines trigger that init
	// concurrently the process can SIGSEGV.
	warmupTLS(logger)

	// Build the market data provider from config (polygon | twelvedata).
	p, err := provider.NewFromConfig(cfg)
	if err != nil {
		logger.Error("create market data provider", "error", err)
		return 1
	}
	logger.Info("market data provider ready", "provider", p.Name())

	// Wire repositories and service.
	tickerRepo := repository.NewTickerRepo(pool)
	candleRepo := repository.NewCandlesDailyRepo(pool)
	svc := marketdata.NewIngestionService(p, candleRepo, tickerRepo, logger)

	// Run the backfill job.
	if err := jobs.RunBackfill(ctx, svc, tickerRepo, candleRepo, jobs.BackfillConfig{
		Years:       *years,
		Concurrency: cfg.MarketDataConcurrency,
	}, *ticker, logger); err != nil {
		logger.Error("backfill failed", "error", err)
		return 1
	}

	logger.Info("backfill finished successfully")
	return 0
}

// warmupTLS forces eager initialisation of the system TLS root certificate pool
// on the main goroutine. Without this, Go's sync.Once-based lazy init can race
// when multiple HTTP workers start concurrently, causing a SIGSEGV.
func warmupTLS(log *slog.Logger) {
	log.Info("warming up TLS root certificate pool")
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{MinVersion: tls.VersionTLS12},
	}
	conn, err := dialer.DialContext(context.Background(), "tcp", "api.massive.com:443")
	if err != nil {
		log.Warn("TLS warmup failed — continuing anyway", "error", err)
		return
	}
	_ = conn.Close()
	log.Info("TLS root certificate pool loaded successfully")
}
