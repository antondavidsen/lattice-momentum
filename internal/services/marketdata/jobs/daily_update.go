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

// DailyUpdateConfig controls the behaviour of RunDailyUpdate.
type DailyUpdateConfig struct {
	// Concurrency is the maximum number of parallel worker goroutines.
	// Defaults to 5 when ≤ 0.
	Concurrency int

	// RequestsPerMinute is the provider's configured RPM limit.
	// Used for informational logging only — actual rate limiting is enforced
	// inside the Provider's own token-bucket Limiter (provider/ratelimit.go).
	// Defaults to 5 (free-tier baseline) when ≤ 0.
	RequestsPerMinute int
}

// RunDailyUpdate appends any candles that are missing since the last stored
// date for every ticker (plus benchmark indices and sector ETFs).
//
// For each symbol:
//   - If we have existing candles → fetch from (latest date + 1 day) to today
//   - If no candles exist → fetch the last lookbackDays trading days as a
//     safety net
//
// Error policy:
//   - CRITICAL ticker failures (benchmarks + sector ETFs) → collected in
//     criticalErrs; function returns an error so the caller can abort.
//   - Non-critical failures → logged; function continues to completion.
func RunDailyUpdate(
	ctx context.Context,
	svc *marketdata.IngestionService,
	tickerRepo *repository.TickerRepo,
	candleRepo *repository.CandlesDailyRepo,
	cfg DailyUpdateConfig,
	log *slog.Logger,
) error {
	const lookbackDays = 7

	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = 5
	}

	// ── 1. Build symbol list ───────────────────────────────────────────────────
	symbols, err := buildSymbolList(ctx, tickerRepo, "")
	if err != nil {
		return err
	}

	// ── 2. Load most recent stored date per symbol ────────────────────────────
	latestDates, err := candleRepo.LatestDates(ctx)
	if err != nil {
		return fmt.Errorf("load latest dates: %w", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	// Use today as the upper bound. The nightly pipeline runs at 21:00 UTC,
	// after US market close, so today's end-of-day bar is available.
	fallback := today.AddDate(0, 0, -lookbackDays)

	// ── 3. Build pending-work list ────────────────────────────────────────────
	var pendingJobs []tickerJob
	for _, sym := range symbols {
		from := fallback
		if latest, ok := latestDates[sym]; ok {
			from = latest.AddDate(0, 0, 1)
		}
		if !from.Before(today) {
			log.Debug("already up to date", "ticker", sym)
			continue
		}
		pendingJobs = append(pendingJobs, tickerJob{
			ticker:     sym,
			from:       from,
			to:         today,
			isCritical: models.IsCriticalTicker(sym),
		})
	}

	log.Info("[ingestion] market ingestion started",
		"tickers_to_update", len(pendingJobs),
		"workers", cfg.Concurrency,
		"rpm_limit", cfg.RequestsPerMinute,
		"provider", svc.Provider().Name(),
	)

	if len(pendingJobs) == 0 {
		log.Info("[ingestion] market ingestion complete: all tickers already up to date")
		return nil
	}

	start := time.Now()

	// ── 4. Spin up workers ────────────────────────────────────────────────────
	jobCh := make(chan tickerJob, len(pendingJobs))
	resultCh := make(chan tickerResult, len(pendingJobs))

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go runWorker(ctx, i+1, jobCh, resultCh, svc, log, &wg)
	}

	for _, j := range pendingJobs {
		jobCh <- j
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// ── 5. Collect results ────────────────────────────────────────────────────
	var (
		successes    int
		failures     int
		criticalErrs []error
	)

	for r := range resultCh {
		if r.err != nil {
			failures++
			if r.isCritical {
				criticalErrs = append(criticalErrs, fmt.Errorf("%s: %w", r.ticker, r.err))
			}
		} else {
			successes++
		}
	}

	log.Info("[ingestion] market ingestion completed",
		"success", successes,
		"failed", failures,
		"duration_ms", time.Since(start).Milliseconds(),
		"provider", svc.Provider().Name(),
	)

	if len(criticalErrs) > 0 {
		return fmt.Errorf(
			"daily update: %d critical ticker ingest failure(s); first: %w",
			len(criticalErrs), criticalErrs[0],
		)
	}

	return nil
}

// ── ensure models package is imported (IsCriticalTicker used above) ──
var _ = models.IsCriticalTicker
