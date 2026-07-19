// Package jobs provides a shared worker pool for ingesting market data tickers.
// ingestion_worker_pool.go implements shared primitives (types + retry helper
// + single-queue worker) used by both RunDailyUpdate and the async priority pool.
//
// Rate-limiting contract:
//
//	Rate limiting is NOT implemented here. Every svc.IngestTicker call blocks
//	inside the Provider's token-bucket Limiter (provider/ratelimit.go) before
//	touching the network, so all workers share a single RPM budget regardless
//	of concurrency.
//
// Retry strategy:
//
//	Attempt 1 – immediate
//	Attempt 2 – wait 2 s
//	Attempt 3 – wait 4 s → give up
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	marketdata "ai-stock-service/internal/services/marketdata"
)

const (
	// maxRetries is the number of total attempts per ticker (1 initial + 2 retries).
	maxRetries = 3
	// retryBaseWait is the wait before the first retry. Subsequent waits double.
	retryBaseWait = 2 * time.Second
)

// tickerJob is the unit of work enqueued for each worker.
type tickerJob struct {
	ticker     string
	from       time.Time
	to         time.Time
	isCritical bool // true for CRITICAL-tier tickers (benchmarks + sector ETFs)
}

// tickerResult carries the outcome of a completed tickerJob back to the
// result collector.
type tickerResult struct {
	ticker          string
	isCritical      bool
	candlesInserted int
	durationMs      int64
	err             error
}

// ingestWithRetry calls svc.IngestTicker with exponential backoff.
// Returns on first success; logs and retries up to maxRetries on failure.
// Respects ctx cancellation during retry sleeps.
func ingestWithRetry(
	ctx context.Context,
	svc *marketdata.IngestionService,
	job tickerJob,
	log *slog.Logger,
) (int, error) {
	wait := retryBaseWait
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		n, err := svc.IngestTicker(ctx, job.ticker, job.from, job.to)
		if err == nil {
			return n, nil
		}
		lastErr = err

		if attempt < maxRetries {
			log.Warn("[ingestion] ticker ingest attempt failed, will retry",
				"ticker", job.ticker,
				"attempt", attempt,
				"max_retries", maxRetries,
				"retry_after_ms", wait.Milliseconds(),
				"error", err,
			)
			select {
			case <-time.After(wait):
				wait *= 2
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	}

	return 0, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// runWorker drains a single jobs channel until it is closed, calling
// ingestWithRetry for each job and forwarding results to resultCh.
// Used by RunDailyUpdate (flat, non-priority pool).
func runWorker(
	ctx context.Context,
	id int,
	jobs <-chan tickerJob,
	results chan<- tickerResult,
	svc *marketdata.IngestionService,
	log *slog.Logger,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for job := range jobs {
		start := time.Now()
		n, err := ingestWithRetry(ctx, svc, job, log)
		durationMs := time.Since(start).Milliseconds()

		if err == nil {
			log.Info("[ingestion] worker processed ticker",
				"worker_id", id,
				"ticker", job.ticker,
				"candles_inserted", n,
				"duration_s", fmt.Sprintf("%.1f", float64(durationMs)/1000.0),
			)
		} else {
			log.Error("[ingestion] ticker ingest failed after all retries",
				"worker_id", id,
				"ticker", job.ticker,
				"is_critical", job.isCritical,
				"duration_ms", durationMs,
				"error", err,
			)
		}

		results <- tickerResult{
			ticker:          job.ticker,
			isCritical:      job.isCritical,
			candlesInserted: n,
			durationMs:      durationMs,
			err:             err,
		}
	}
}
