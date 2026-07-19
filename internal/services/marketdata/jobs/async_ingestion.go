// Package jobs implements a non-blocking candle ingestion worker pool with two priority tiers (CRITICAL and NORMAL) for the nightly pipeline.  It provides an IngestionHandle to synchronise on completion of either tier, allowing the regime engine to run while NORMAL tickers continue in the background.
// async_ingestion.go provides a non-blocking candle ingestion worker pool
// with two priority tiers:
//
//	CRITICAL — benchmark indices (SPY, QQQ, IWM, DIA) + all sector ETFs.
//	           The nightly pipeline waits for this tier before running the
//	           regime engine.
//
//	NORMAL   — every other universe ticker.
//	           Processed asynchronously while the regime pipeline runs.
//
// Usage pattern (nightly pipeline):
//
//	handle, err := StartCandleIngestionAsync(ctx, svc, tickerRepo, candleRepo, cfg{TargetDate: today}, log)
//	if err != nil { return err }
//
//	// Block only until CRITICAL tickers are fresh, then proceed with regime jobs.
//	if err := handle.WaitForPriorityIngestion(ctx); err != nil { return err }
//
//	// Regime jobs run here while NORMAL tickers continue in the background.
//
//	// Drain remaining work before the process exits.
//	_ = handle.Wait(ctx)
//
// Rate limiting:
//
//	Workers do NOT throttle themselves.  Every svc.IngestTicker call blocks
//	inside the Provider's token-bucket Limiter (provider/ratelimit.go) before
//	touching the network, so all workers share a single RPM budget regardless
//	of WorkerCount.
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

// AsyncIngestionConfig configures the background candle ingestion worker pool.
type AsyncIngestionConfig struct {
	// WorkerCount is the number of concurrent worker goroutines.
	// Defaults to 1 (free-tier baseline: 5 req/min ÷ ~10 s per request).
	// Raise this when moving to a paid API tier with higher RPM limits.
	WorkerCount int

	// RequestsPerMinute is the provider's configured RPM ceiling.
	// Forwarded to observability logs only — actual throttling is enforced by
	// the provider's own token-bucket Limiter (provider/ratelimit.go).
	// Defaults to 5 when ≤ 0.
	RequestsPerMinute int

	// TargetDate is the pipeline's logical run date (the trading day whose
	// candles we want).  When zero the function falls back to
	// time.Now().UTC() truncated to midnight — but callers should always set
	// this explicitly so the date is consistent with the rest of the pipeline
	// and honours the --date override flag.
	TargetDate time.Time

	// TVPriorityTickers are tickers from today's TradingView screener output
	// that should be ingested at CRITICAL priority (alongside benchmarks and
	// sector ETFs).  These tickers may not yet exist in the tickers table,
	// so they would otherwise be missed by buildSymbolList.
	TVPriorityTickers []string
}

// IngestionHandle is returned by StartCandleIngestionAsync.
// It exposes two independent wait points so the caller can synchronise on
// whichever completion boundary it needs.
type IngestionHandle struct {
	// priorityDone is closed once every CRITICAL ticker has been processed
	// (whether successfully or with an error).
	priorityDone <-chan struct{}

	// allDone is closed once every ticker (CRITICAL + NORMAL) has been
	// processed.
	allDone <-chan struct{}

	mu           sync.Mutex
	criticalErrs []error // populated before priorityDone is closed
	normalErrs   []error // populated before allDone is closed
}

// WaitForPriorityIngestion blocks until every CRITICAL ticker has been
// processed.  Returns an error when one or more CRITICAL tickers failed so
// the caller can abort the regime pipeline before acting on stale data.
func (h *IngestionHandle) WaitForPriorityIngestion(ctx context.Context) error {
	select {
	case <-h.priorityDone:
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.criticalErrs) > 0 {
			return fmt.Errorf(
				"[ingestion] priority ingestion: %d critical ticker failure(s); first: %w",
				len(h.criticalErrs), h.criticalErrs[0],
			)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Wait blocks until every ticker (CRITICAL + NORMAL) has been processed.
// Returns a non-nil error summarising any NORMAL ticker failures; these are
// informational — the caller should log and continue rather than abort.
func (h *IngestionHandle) Wait(ctx context.Context) error {
	select {
	case <-h.allDone:
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.normalErrs) > 0 {
			return fmt.Errorf(
				"[ingestion] background ingestion: %d normal ticker failure(s); first: %w",
				len(h.normalErrs), h.normalErrs[0],
			)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartCandleIngestionAsync starts the worker pool in the background and
// returns immediately with an IngestionHandle.
//
// Workers drain the CRITICAL queue first, then the NORMAL queue.  Both queues
// share the provider's token-bucket rate limiter, so the configured RPM is
// never exceeded regardless of WorkerCount.
func StartCandleIngestionAsync(
	ctx context.Context,
	svc *marketdata.IngestionService,
	tickerRepo *repository.TickerRepo,
	candleRepo *repository.CandlesDailyRepo,
	cfg AsyncIngestionConfig,
	log *slog.Logger,
) (*IngestionHandle, error) {
	// lookbackDays defines how far back to fetch candles for tickers with no
	// prior candle history in the database. Must be large enough to populate
	// the chart handler's 6-month window (≈180 calendar days) so that newly
	// imported tickers from TradingView screeners have enough history for
	// SMA-50 calculations and meaningful chart display.
	const lookbackDays = 180

	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = 5
	}

	// ── 1. Build full symbol list ──────────────────────────────────────────────
	symbols, err := buildSymbolList(ctx, tickerRepo, "")
	if err != nil {
		return nil, err
	}

	// ── 2. Load latest stored candle date per symbol ───────────────────────────
	latestDates, err := candleRepo.LatestDates(ctx)
	if err != nil {
		return nil, fmt.Errorf("load latest dates: %w", err)
	}

	// Use the caller-supplied target date as the upper bound.  This keeps
	// candle ingestion in sync with the rest of the nightly pipeline and
	// respects the --date override flag.  Falls back to time.Now() truncated
	// to midnight UTC when the caller does not set TargetDate (zero value).
	today := cfg.TargetDate.UTC().Truncate(24 * time.Hour)
	if today.IsZero() {
		today = time.Now().UTC().Truncate(24 * time.Hour)
	}
	fallback := today.AddDate(0, 0, -lookbackDays)

	// ── 3. Classify symbols into CRITICAL and NORMAL queues ───────────────────
	var criticalJobs, normalJobs []tickerJob
	for _, sym := range symbols {
		from, upToDate := computeFromDate(latestDates, sym, fallback, today)
		if upToDate {
			log.Debug("[ingestion] already up to date", "ticker", sym)
			continue
		}
		job := tickerJob{
			ticker:     sym,
			from:       from,
			to:         today,
			isCritical: models.IsCriticalTicker(sym),
		}
		if job.isCritical {
			criticalJobs = append(criticalJobs, job)
		} else {
			normalJobs = append(normalJobs, job)
		}
	}

	// ── 3b. Inject TV priority tickers into the critical queue ────────────────
	// These tickers come from today's TradingView screener output and may not
	// exist in the tickers table yet.  They are treated as CRITICAL so they
	// get fresh candles before the ranking pipeline runs.
	for _, tvTicker := range cfg.TVPriorityTickers {
		// Skip tickers already in the critical or normal queues.
		alreadyQueued := false
		for _, j := range criticalJobs {
			if j.ticker == tvTicker {
				alreadyQueued = true
				break
			}
		}
		if !alreadyQueued {
			for _, j := range normalJobs {
				if j.ticker == tvTicker {
					alreadyQueued = true
					break
				}
			}
		}
		if alreadyQueued {
			continue
		}

		from, upToDate := computeFromDate(latestDates, tvTicker, fallback, today)
		if upToDate {
			log.Debug("[ingestion] TV ticker already up to date", "ticker", tvTicker)
			continue
		}
		criticalJobs = append(criticalJobs, tickerJob{
			ticker:     tvTicker,
			from:       from,
			to:         today,
			isCritical: true,
		})
	}

	log.Info("[ingestion] starting worker pool",
		"priority_queue_size", len(criticalJobs),
		"normal_queue_size", len(normalJobs),
		"tv_priority_tickers", len(cfg.TVPriorityTickers),
		"workers", cfg.WorkerCount,
		"rpm_limit", cfg.RequestsPerMinute,
		"provider", svc.Provider().Name(),
	)

	priorityDoneCh := make(chan struct{})
	allDoneCh := make(chan struct{})

	handle := &IngestionHandle{
		priorityDone: priorityDoneCh,
		allDone:      allDoneCh,
	}

	// Fast path: nothing to do — close both channels immediately.
	if len(criticalJobs) == 0 && len(normalJobs) == 0 {
		log.Info("[ingestion] all tickers already up to date")
		close(priorityDoneCh)
		close(allDoneCh)
		return handle, nil
	}

	total := len(criticalJobs) + len(normalJobs)

	// ── 4. Pre-fill priority channels (both closed so workers can range) ───────
	//
	// CRITICAL jobs are enqueued into a dedicated channel that workers drain
	// before touching the normal channel.  Both channels are filled and closed
	// before any worker goroutine starts, so workers never block on sends.
	criticalCh := make(chan tickerJob, max(len(criticalJobs), 1))
	normalCh := make(chan tickerJob, max(len(normalJobs), 1))
	resultCh := make(chan tickerResult, total)

	for _, j := range criticalJobs {
		criticalCh <- j
	}
	close(criticalCh)

	for _, j := range normalJobs {
		normalCh <- j
	}
	close(normalCh)

	// ── 5. Launch workers ──────────────────────────────────────────────────────
	var wg sync.WaitGroup
	for i := 0; i < cfg.WorkerCount; i++ {
		wg.Add(1)
		go runPriorityWorker(ctx, i+1, criticalCh, normalCh, resultCh, svc, log, &wg)
	}

	// Close resultCh once all workers finish so the collector loop below exits.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// ── 6. Collect results asynchronously ─────────────────────────────────────
	go func() {
		pendingCritical := len(criticalJobs)
		priorityClosed := false

		// If there were no CRITICAL jobs pending, signal priority done now so
		// WaitForPriorityIngestion returns immediately.
		if pendingCritical == 0 {
			priorityClosed = true
			log.Info("[ingestion] priority ingestion completed",
				"critical_failures", 0,
				"note", "no critical tickers were pending",
			)
			close(priorityDoneCh)
		}

		for r := range resultCh {
			durationSec := float64(r.durationMs) / 1000.0
			log.Info("[ingestion] worker processed ticker",
				"ticker", r.ticker,
				"duration_s", fmt.Sprintf("%.1f", durationSec),
				"candles_inserted", r.candlesInserted,
				"is_critical", r.isCritical,
				"error", r.err,
			)

			if r.isCritical {
				pendingCritical--
				if r.err != nil {
					handle.mu.Lock()
					handle.criticalErrs = append(handle.criticalErrs,
						fmt.Errorf("%s: %w", r.ticker, r.err))
					handle.mu.Unlock()
				}
				if pendingCritical == 0 && !priorityClosed {
					priorityClosed = true
					handle.mu.Lock()
					critFailures := len(handle.criticalErrs)
					handle.mu.Unlock()
					log.Info("[ingestion] priority ingestion completed",
						"critical_failures", critFailures,
					)
					close(priorityDoneCh)
				}
			} else if r.err != nil {
				handle.mu.Lock()
				handle.normalErrs = append(handle.normalErrs,
					fmt.Errorf("%s: %w", r.ticker, r.err))
				handle.mu.Unlock()
			}
		}

		// Safety net: ensure priorityDone is always closed before allDone.
		if !priorityClosed {
			close(priorityDoneCh)
		}
		close(allDoneCh)
	}()

	return handle, nil
}

// computeFromDate returns the start date for a candle fetch window and whether
// the ticker is already up to date.
//
// Logic:
//
//	No prior candle  → from = fallback (today − lookbackDays), upToDate = false
//	latest < today   → from = latest + 1 day,                  upToDate = false
//	latest >= today  → from = latest + 1 day,                  upToDate = true
//
// The previous guard used !from.Before(today) which is `from >= today`.
// That incorrectly skipped tickers whose latest candle was yesterday:
//
//	latest = yesterday  →  from = today  →  !(today < today) = true  →  SKIP  ← BUG
//
// The correct condition is from.After(today) i.e. from > today, which only
// skips tickers whose latest candle is already today or newer.
func computeFromDate(latestDates map[string]time.Time, sym string, fallback, today time.Time) (from time.Time, upToDate bool) {
	from = fallback
	if latest, ok := latestDates[sym]; ok {
		from = latest.AddDate(0, 0, 1)
	}
	return from, from.After(today)
}

// runPriorityWorker processes jobs from criticalCh first, then normalCh.
// Both channels must already be closed (pre-filled) when the worker starts.
// Rate limiting is handled transparently by the provider's token-bucket
// Limiter inside svc.IngestTicker — workers never sleep explicitly.
func runPriorityWorker(
	ctx context.Context,
	id int,
	criticalCh <-chan tickerJob,
	normalCh <-chan tickerJob,
	results chan<- tickerResult,
	svc *marketdata.IngestionService,
	log *slog.Logger,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	processJob := func(job tickerJob) {
		start := time.Now()
		n, err := ingestWithRetry(ctx, svc, job, log)
		durationMs := time.Since(start).Milliseconds()
		if err != nil {
			log.Error("[ingestion] ticker ingest failed after all retries",
				"worker_id", id,
				"is_critical", job.isCritical,
				"duration_ms", durationMs,
				"error", err,
			)
		}
		results <- tickerResult{
			ticker:          job.ticker,
			isCritical:      job.isCritical,
			candlesInserted: n,
			durationMs:      time.Since(start).Milliseconds(),
			err:             err,
		}
	}

	// Drain CRITICAL queue first.
	for job := range criticalCh {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processJob(job)
	}

	// Then drain NORMAL queue.
	for job := range normalCh {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processJob(job)
	}
}
