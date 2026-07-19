// Step 5 is wired directly in main.go via classificationJob.RunMarketRegimeClassificationJob.
// Step 4 is wired directly in main.go via regimeJob.RunMarketInputsJob so
// that the job struct receives the same logger and dependencies as the rest
// of the pipeline without needing to be passed through an extra wrapper here.
// Step 7 (sector momentum scoring) is wired directly in main.go via
// sectorMomentumJob.RunSectorMomentumJob.
package main

import (
	marketdatajobs "ai-stock-service/internal/services/marketdata/jobs"
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/ingestion"
	marketdata "ai-stock-service/internal/services/marketdata"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// ── Pipeline runner ───────────────────────────────────────────────────────────

// pipelineStep pairs a human-readable name with the function that executes it.
type pipelineStep struct {
	name string
	run  func() error
}

// runPipeline executes steps in order, logging start/complete/duration for each.
// If any step returns a non-nil error the pipeline logs the failure and returns
// that error — it does NOT call os.Exit itself.  The caller is responsible for
// exit so that any deferred cleanup (e.g. pool.Close) runs before the process
// terminates.
//
// When nightlyRunRepo is non-nil the pipeline records its execution in the
// nightly_runs table. The returned run ID is valid only when nightlyRunRepo
// is provided.
//
// gateLevel is the R-09 circuit breaker gate level ("full", "ep_only", "halt")
// recorded on the nightly_runs row for audit. Pass "" to omit.
//
// Emits Prometheus metrics: nightly_pipeline_runs_total and nightly_pipeline_duration_seconds.
func runPipeline(ctx context.Context, steps []pipelineStep, log *slog.Logger, nightlyRunRepo *repository.NightlyRunRepo, runDate time.Time, gateLevel string) (uuid.UUID, error) {
	log.Info("nightly pipeline starting", "steps", len(steps), "gate_level", gateLevel)
	var runID uuid.UUID
	stepDurations := make(map[string]int64, len(steps))

	pipelineStart := time.Now()

	// Record the run start in the DB.
	if nightlyRunRepo != nil {
		id, err := nightlyRunRepo.Insert(ctx, runDate, len(steps), gateLevel)
		if err != nil {
			log.Warn("failed to insert nightly_run row — continuing without audit tracking", "error", err)
		} else {
			runID = id
		}
	}

	// Record pipeline start metric — enables crash detection (started without completed/failed)
	metrics.NightlyPipelineRunsTotal.WithLabelValues("started", "all").Inc()

	for i, step := range steps {
		log.Info("pipeline step starting",
			"step_num", i+1,
			"step_name", step.name,
			"step", step.name,
		)

		stepTimer := prometheus.NewTimer(metrics.NightlyPipelineDuration.WithLabelValues("nightly", step.name))
		start := time.Now()
		if err := step.run(); err != nil {
			stepTimer.ObserveDuration()
			durationMs := time.Since(start).Milliseconds()
			stepDurations[step.name] = durationMs
			log.Error("pipeline step failed — aborting",
				"step_num", i+1,
				"step_name", step.name,
				"step", step.name,
				"error", err,
				"duration_ms", durationMs,
			)

			// Record step failure metric
			metrics.NightlyPipelineStepTotal.WithLabelValues(step.name, "failure").Inc()

			if nightlyRunRepo != nil && runID != uuid.Nil {
				if markErr := nightlyRunRepo.MarkFailed(ctx, runID, i, step.name, err.Error(), stepDurations); markErr != nil {
					log.Warn("failed to mark nightly_run as failed", "error", markErr)
				}
			}

			// Record pipeline failure metric — step-level failures tracked separately
			metrics.NightlyPipelineRunsTotal.WithLabelValues("failed", "all").Inc()
			metrics.NightlyPipelineDuration.WithLabelValues("nightly", "total").Observe(time.Since(pipelineStart).Seconds())

			return runID, fmt.Errorf("step %d (%s): %w", i+1, step.name, err)
		}

		stepTimer.ObserveDuration()
		durationMs := time.Since(start).Milliseconds()
		stepDurations[step.name] = durationMs
		log.Info("pipeline step complete",
			"step_num", i+1,
			"step_name", step.name,
			"step", step.name,
			"duration_ms", durationMs,
		)

		// Record step success metric
		metrics.NightlyPipelineStepTotal.WithLabelValues(step.name, "success").Inc()
	}

	log.Info("nightly pipeline complete", "steps", len(steps))

	if nightlyRunRepo != nil && runID != uuid.Nil {
		if markErr := nightlyRunRepo.MarkCompleted(ctx, runID, len(steps), stepDurations); markErr != nil {
			log.Warn("failed to mark nightly_run as completed", "error", markErr)
		}
	}

	// Record pipeline success metric
	metrics.NightlyPipelineRunsTotal.WithLabelValues("completed", "all").Inc()
	metrics.NightlyPipelineDuration.WithLabelValues("nightly", "total").Observe(time.Since(pipelineStart).Seconds())

	return runID, nil
}

// ── Step 1: TradingView screener imports ──────────────────────────────────────

// runTradingViewImports triggers the tv-collector service to run an immediate
// TradingView screener collection for today.
//
// When tvCollectorURL is set the Go pipeline calls POST /run on the tv-collector
// HTTP service and blocks until the collection finishes.  The tv-collector is
// the sole mechanism through which screener data enters the system — it no
// longer runs on an independent APScheduler; the nightly pipeline is its only
// trigger.
//
// When tvCollectorURL is empty (e.g. local dev without the Python service) the
// step logs a warning and succeeds so that Steps 3 and 4 can still run against
// previously stored data.
func runTradingViewImports(
	ctx context.Context,
	tvCollectorURL string,
	log *slog.Logger,
) error {
	if tvCollectorURL == "" {
		log.Warn("TradingView screener imports: TV_COLLECTOR_URL not set — skipping collection trigger",
			"hint", "set TV_COLLECTOR_URL=http://tv-collector:8001 to enable automated collection")
		return nil
	}

	client := ingestion.NewTVCollectorClient(tvCollectorURL, 0) // 0 → default 10 min timeout
	log.Info("TradingView screener imports: triggering collection", "url", tvCollectorURL)

	if err := client.TriggerCollection(ctx); err != nil {
		return fmt.Errorf("TradingView collection failed: %w", err)
	}
	return nil
}

// ── Step 2: TradingView snapshot freshness check ──────────────────────────────

// runFundamentalsSnapshot verifies that today's TradingView screener rows are
// present in tradingview_snapshot_daily (the table where all screener data —
// including fundamentals fields — is stored after tv-collector ingestion).
//
// The old fundamentals_snapshot table is no longer used; all EPS/margin/ROE
// data lives in tradingview_snapshot_daily.  This step is a post-Step-1
// sanity check: if no rows exist for today it logs a warning and continues
// (non-fatal) so Steps 3 and 4 still run.
func runFundamentalsSnapshot(
	ctx context.Context,
	tvRepo *repository.TVSnapshotRepo,
	date time.Time,
	log *slog.Logger,
) error {
	count, err := tvRepo.CountByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("tv snapshot check: %w", err)
	}
	if count == 0 {
		log.Warn("tv snapshot check: no tradingview_snapshot_daily rows for today — tv-collector may not have run yet",
			"date", date.Format("2006-01-02"),
		)
	} else {
		log.Info("tv snapshot check: data present",
			"date", date.Format("2006-01-02"),
			"rows", count,
		)
	}
	return nil
}

// ── Step 2b: Fetch TV priority tickers ────────────────────────────────────────

// fetchTVTickers returns the distinct set of tickers from today's TradingView
// screener output.  These tickers are injected into the CRITICAL ingestion
// queue so they get fresh candles before the ranking pipeline runs.
func fetchTVTickers(
	ctx context.Context,
	tvRepo *repository.TVSnapshotRepo,
	date time.Time,
	log *slog.Logger,
) []string {
	grouped, err := tvRepo.TickersByDateGrouped(ctx, date)
	if err != nil {
		log.Warn("failed to fetch TV tickers for priority ingestion — continuing without",
			"error", err,
		)
		return nil
	}

	seen := make(map[string]struct{})
	var tickers []string
	for _, srcTickers := range grouped {
		for _, t := range srcTickers {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				tickers = append(tickers, t)
			}
		}
	}

	log.Info("TV priority tickers fetched",
		"count", len(tickers),
		"date", date.Format("2006-01-02"),
	)
	return tickers
}

// ── Step 3: Start candle ingestion (async) ────────────────────────────────────

// startCandleIngestionAsync spawns the background worker pool and returns
// immediately with an IngestionHandle.  The caller must later call
// waitForPriorityIngestion to block until CRITICAL tickers are fresh.
func startCandleIngestionAsync(
	ctx context.Context,
	svc *marketdata.IngestionService,
	tickerRepo *repository.TickerRepo,
	candleRepo *repository.CandlesDailyRepo,
	workerCount int,
	rpm int,
	targetDate time.Time,
	tvPriorityTickers []string,
	log *slog.Logger,
) (*marketdatajobs.IngestionHandle, error) {
	return marketdatajobs.StartCandleIngestionAsync(
		ctx, svc, tickerRepo, candleRepo,
		marketdatajobs.AsyncIngestionConfig{
			WorkerCount:       workerCount,
			RequestsPerMinute: rpm,
			TargetDate:        targetDate,
			TVPriorityTickers: tvPriorityTickers,
		},
		log,
	)
}

// ── Step 4: Wait for priority candle ingestion ────────────────────────────────

// waitForPriorityIngestion blocks until every CRITICAL ticker (benchmark
// indices + sector ETFs) has a fresh candle for today.  Returns an error if
// any CRITICAL ticker failed so the regime pipeline does not run on stale data.
func waitForPriorityIngestion(
	ctx context.Context,
	handle *marketdatajobs.IngestionHandle,
	log *slog.Logger,
) error {
	if handle == nil {
		return fmt.Errorf("ingestion handle is nil — Step 3 must succeed before Step 4")
	}
	log.Info("[ingestion] waiting for priority ingestion to complete")
	if err := handle.WaitForPriorityIngestion(ctx); err != nil {
		return fmt.Errorf("priority ingestion failed: %w", err)
	}
	log.Info("[ingestion] priority ingestion completed — proceeding with regime pipeline")
	return nil
}

// ── Post-pipeline: drain background ingestion ────────────────────────────────

// drainBackgroundIngestion blocks until all NORMAL tickers have finished
// ingesting.  Called after the regime pipeline completes so the process does
// not exit while workers are still writing candles.  Failures are logged as
// warnings rather than errors — they do not abort the process.
func drainBackgroundIngestion(
	ctx context.Context,
	handle *marketdatajobs.IngestionHandle,
	log *slog.Logger,
) {
	if handle == nil {
		return
	}
	log.Info("[ingestion] draining background ingestion")
	if err := handle.Wait(ctx); err != nil {
		log.Warn("[ingestion] background ingestion completed with errors", "error", err)
	} else {
		log.Info("[ingestion] background ingestion completed successfully")
	}
}

// checkEffectiveSampleSize logs the ratio of raw_N / effective_N for the trade
// outcome table. If the ratio exceeds 1.5, it logs at WARN level indicating
// duplication is inflating the apparent sample size.
func checkEffectiveSampleSize(ctx context.Context, tradeOutcomeRepo *repository.TradeOutcomeRepo, logger *slog.Logger) {
	rawN, effectiveN, err := tradeOutcomeRepo.GetEffectiveSampleSize(ctx, "all")
	if err != nil {
		logger.Warn("Failed to compute effective sample size", "error", err)
		return
	}
	ratio := float64(rawN) / float64(effectiveN)
	lvl := slog.LevelInfo
	msg := "effective sample size check"
	if rawN > 0 && effectiveN > 0 && ratio > 1.5 {
		lvl = slog.LevelWarn
		msg = "effective sample size warning: duplication inflating apparent N"
	}
	logger.LogAttrs(ctx, lvl, msg,
		slog.String("step", "effective_sample"),
		slog.Int("raw_n", rawN),
		slog.Int("effective_n", effectiveN),
		slog.Float64("ratio", ratio),
	)
}
