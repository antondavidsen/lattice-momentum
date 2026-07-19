// Package marketdata provides the IngestionService that ties together a
// market data Provider, the CandlesDailyRepo, and the TickerRepo.
// It is the single orchestration layer that jobs (backfill, daily update)
// call to fetch and persist candles.
package marketdata

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/marketdata/provider"
)

// IngestionService orchestrates fetching candles from a Provider and storing
// them in the candles_daily table.
type IngestionService struct {
	provider   provider.Provider
	candleRepo *repository.CandlesDailyRepo
	tickerRepo *repository.TickerRepo
	log        *slog.Logger
}

// NewIngestionService constructs an IngestionService.
func NewIngestionService(
	p provider.Provider,
	candleRepo *repository.CandlesDailyRepo,
	tickerRepo *repository.TickerRepo,
	log *slog.Logger,
) *IngestionService {
	return &IngestionService{
		provider:   p,
		candleRepo: candleRepo,
		tickerRepo: tickerRepo,
		log:        log,
	}
}

// IngestTicker fetches and stores candles for a single ticker over [from, to].
// It returns the number of candles upserted (0 when the window contains no
// trading days or the ticker is already up-to-date) and any error encountered.
//
// It first ensures a row exists in the tickers table (inserting a placeholder
// if needed) so the candles_daily FK constraint is satisfied. This is important
// for benchmark indices (SPY, QQQ) and sector ETFs (XLK, …) that may not have
// been imported through the normal TradingView ingestion path.
func (s *IngestionService) IngestTicker(ctx context.Context, ticker string, from, to time.Time) (int, error) {
	// Satisfy the FK constraint before we try to write any candles.
	if err := s.tickerRepo.EnsureExists(ctx, ticker); err != nil {
		return 0, fmt.Errorf("ensure ticker %s exists: %w", ticker, err)
	}

	candles, err := s.provider.FetchDailyCandles(ctx, ticker, from, to)
	if err != nil {
		return 0, fmt.Errorf("fetch candles %s [%s, %s]: %w",
			ticker, from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}

	if len(candles) == 0 {
		// Suppress the log when the entire requested window falls on a weekend
		// or a single non-trading day — the API correctly returns nothing and
		// the next weekday run will pick up the missing bar automatically.
		if !isLikelyTradingWindow(from, to) {
			return 0, nil
		}
		s.log.Info("no candles returned",
			"ticker", ticker,
			"from", from.Format("2006-01-02"),
			"to", to.Format("2006-01-02"),
			"provider", s.provider.Name(),
		)
		return 0, nil
	}

	if err := s.candleRepo.UpsertBatch(ctx, candles); err != nil {
		return 0, fmt.Errorf("store candles %s: %w", ticker, err)
	}

	// Track tickers ingested
	metrics.TickersIngestedTotal.WithLabelValues(s.provider.Name()).Add(float64(len(candles)))

	s.log.Info("ingested candles",
		"ticker", ticker,
		"count", len(candles),
		"from", candles[0].Date.Format("2006-01-02"),
		"to", candles[len(candles)-1].Date.Format("2006-01-02"),
		"provider", s.provider.Name(),
	)
	return len(candles), nil
}

// Provider exposes the underlying data provider (useful for tests and logging).
func (s *IngestionService) Provider() provider.Provider {
	return s.provider
}

// isLikelyTradingWindow returns false when every day in [from, to] is a
// Saturday or Sunday, meaning the API is expected to return no bars.
// It does NOT account for public holidays (those are rare and harmless).
// The check is intentionally loose: if any single day in the window is a
// weekday we log, so operators notice genuine gaps (e.g. a delayed 404).
func isLikelyTradingWindow(from, to time.Time) bool {
	// Normalise to date-only in UTC to avoid DST / timezone edge cases when
	// dates arrive from the DB as midnight UTC and may have sub-day offsets.
	f := from.UTC().Truncate(24 * time.Hour)
	t := to.UTC().Truncate(24 * time.Hour)

	for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			return true // at least one weekday in the window
		}
	}
	return false
}
