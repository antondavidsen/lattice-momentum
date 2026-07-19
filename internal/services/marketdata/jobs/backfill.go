// Package jobs contains the long-running data ingestion jobs.
// Backfill fetches up to N years of historical daily OHLCV candles for every
// ticker (plus benchmark indices and sector ETFs) in the database.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	marketdata "ai-stock-service/internal/services/marketdata"
)

// BackfillConfig controls the behaviour of RunBackfill.
type BackfillConfig struct {
	// Years is the number of calendar years of history to fetch (default 2).
	Years int
	// Concurrency is the maximum number of tickers fetched in parallel (default 5).
	// The provider's own rate limiter is the primary throttle; this caps the
	// number of goroutines queued waiting for a rate-limit token.
	Concurrency int
}

// RunBackfill fetches historical candles for every ticker (and all benchmark
// indices + sector ETFs) and stores them in the candles_daily table.
//
// For each symbol it computes the earliest date that still needs data:
//   - If no candles exist yet → fetch from (today − Years)
//   - If candles exist → fetch from (latest stored date + 1 day) to today
//
// singleTicker, when non-empty, restricts the run to a single symbol.
func RunBackfill(
	ctx context.Context,
	svc *marketdata.IngestionService,
	tickerRepo *repository.TickerRepo,
	candleRepo *repository.CandlesDailyRepo,
	cfg BackfillConfig,
	singleTicker string,
	log *slog.Logger,
) error {
	if cfg.Years <= 0 {
		cfg.Years = 2
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}

	// ── 1. Build the symbol list ──────────────────────────────────────────────
	symbols, err := buildSymbolList(ctx, tickerRepo, singleTicker)
	if err != nil {
		return err
	}
	log.Info("backfill starting", "symbols", len(symbols), "years", cfg.Years)

	// ── 2. Load existing coverage so we can skip already-filled ranges ────────
	latestDates, err := candleRepo.LatestDates(ctx)
	if err != nil {
		return fmt.Errorf("load latest dates: %w", err)
	}

	// Load candle counts to detect symbols with incomplete history.
	candleCounts, err := candleRepo.CountByTicker(ctx)
	if err != nil {
		return fmt.Errorf("load candle counts: %w", err)
	}

	cutoff := time.Now().UTC().AddDate(-cfg.Years, 0, 0).Truncate(24 * time.Hour)
	today := time.Now().UTC().Truncate(24 * time.Hour)

	// Approximate number of trading days we expect for the requested range.
	// ~252 trading days per year; we use 80% as the threshold to allow for
	// weekends, holidays, and recently-listed symbols.
	expectedMin := int64(float64(cfg.Years) * 252 * 0.80)

	// ── 3. Fan out with bounded concurrency ───────────────────────────────────
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, sym := range symbols {
		// Determine inclusive start date for this symbol.
		from := cutoff
		if latest, ok := latestDates[sym]; ok && latest.After(cutoff) {
			count := candleCounts[sym]
			if count < expectedMin {
				// Too few candles — history is incomplete, fetch from full cutoff.
				log.Info("incomplete history, fetching full range",
					"ticker", sym, "stored", count, "expectedMin", expectedMin)
				from = cutoff
			} else {
				from = latest.AddDate(0, 0, 1)
			}
		}
		if !from.Before(today) {
			log.Debug("already up to date", "ticker", sym)
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if _, err := svc.IngestTicker(ctx, sym, from, today); err != nil {
				log.Error("backfill failed", "ticker", sym, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", sym, err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("backfill completed with %d error(s); first: %w", len(errs), errs[0])
	}

	log.Info("backfill complete", "symbols", len(symbols))
	return nil
}

// buildSymbolList returns the symbols to process. When singleTicker is set it
// returns a one-element slice; otherwise it merges all DB tickers with the
// hard-coded benchmark and sector lists.
func buildSymbolList(ctx context.Context, tickerRepo *repository.TickerRepo, singleTicker string) ([]string, error) {
	if singleTicker != "" {
		return []string{singleTicker}, nil
	}

	dbTickers, err := tickerRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tickers: %w", err)
	}

	seen := make(map[string]struct{})
	var symbols []string
	add := func(s string) {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			symbols = append(symbols, s)
		}
	}

	for i := range dbTickers {
		add(dbTickers[i].Ticker)
	}
	for i := range models.Benchmarks {
		add(models.Benchmarks[i])
	}
	for i := range models.SectorETFs {
		add(models.SectorETFs[i])
	}

	return symbols, nil
}
