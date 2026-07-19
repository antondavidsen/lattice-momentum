package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services"
)

// jobName is emitted in every log line so operators can filter by job in
// aggregated log streams (Loki, CloudWatch, etc.).
const jobName = "MarketInputsJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// marketInputsBuilder is the subset of services.MarketInputsService that the
// job requires.  Using an interface allows tests to inject a mock without a
// live database connection.
type marketInputsBuilder interface {
	BuildMarketInputs(ctx context.Context, date time.Time) (services.MarketInputs, error)
}

// marketInputsStorer is the subset of repository.MarketInputsRepo that the
// job requires.
type marketInputsStorer interface {
	InsertMarketInputs(ctx context.Context, m *models.MarketInputsDaily) error
}

// candleFreshnessChecker allows the job to verify that benchmark candle data
// for the target date is actually present before computing regime inputs.
// This prevents the job from silently persisting a row for "today" when the
// candle ingest step failed to fetch today's closes.
type candleFreshnessChecker interface {
	GetLatestCandleDate(ctx context.Context, ticker string) (time.Time, error)
}

// Compile-time assertions: production types must satisfy the interfaces.
var _ marketInputsBuilder = (*services.MarketInputsService)(nil)
var _ marketInputsStorer = (*repository.MarketInputsRepo)(nil)
var _ candleFreshnessChecker = (*repository.MarketDataRepo)(nil)

// MarketRegimeInputsJob computes and persists the daily market regime input
// signals.  It is designed to be constructed once at startup and called once
// per nightly pipeline run.
//
// All dependencies are injected via the constructor — the job itself carries
// no mutable state and is safe to call from a single goroutine.
type MarketRegimeInputsJob struct {
	svc  marketInputsBuilder
	repo marketInputsStorer
	log  *slog.Logger
	// freshness is optional: when non-nil, a pre-flight check verifies that
	// SPY has a candle for the target date before any computation begins.
	freshness candleFreshnessChecker
}

// NewMarketRegimeInputsJob constructs a MarketRegimeInputsJob from the
// production concrete types.
func NewMarketRegimeInputsJob(
	svc *services.MarketInputsService,
	repo *repository.MarketInputsRepo,
	log *slog.Logger,
) *MarketRegimeInputsJob {
	return &MarketRegimeInputsJob{svc: svc, repo: repo, log: log}
}

// NewMarketRegimeInputsJobFromSources constructs a MarketRegimeInputsJob from
// any values that satisfy the service and store interfaces.
// Intended for use in tests where mocks replace the real implementations.
func NewMarketRegimeInputsJobFromSources(
	svc marketInputsBuilder,
	repo marketInputsStorer,
	log *slog.Logger,
) *MarketRegimeInputsJob {
	return &MarketRegimeInputsJob{svc: svc, repo: repo, log: log}
}

// WithFreshnessChecker attaches a candleFreshnessChecker to the job and
// returns the job for fluent chaining.
//
// When set, RunMarketInputsJob verifies that SPY has a candle dated ≥ the
// target date before calling BuildMarketInputs.  If the check fails the job
// returns a hard error instead of computing signals from stale data.
//
// Example wiring in production:
//
//	regimeJob := jobs.NewMarketRegimeInputsJob(svc, repo, log).
//	    WithFreshnessChecker(marketDataRepo)
func (j *MarketRegimeInputsJob) WithFreshnessChecker(c candleFreshnessChecker) *MarketRegimeInputsJob {
	j.freshness = c
	return j
}

// RunMarketInputsJob executes the full compute-and-persist cycle for date.
//
// Steps:
//  0. (optional) Pre-flight freshness check — abort if SPY candle data is stale.
//  1. Log job start.
//  2. Call MarketInputsService.BuildMarketInputs to derive all regime signals.
//  3. Upsert the result via MarketInputsRepo (idempotent — safe to rerun).
//  4. Log a structured summary with execution time and all computed metrics.
//
// Returns a wrapped error on any failure; the caller decides whether to abort
// the broader pipeline or continue to subsequent steps.
func (j *MarketRegimeInputsJob) RunMarketInputsJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	// ── 0. Data-freshness pre-flight check ────────────────────────────────────
	// Verify that candle ingest has produced reasonably fresh SPY data before
	// computing regime inputs.
	//
	// Skipped on weekends: markets are closed Saturday–Sunday so no new candle
	// is expected.  The production cron (Mon–Fri) never fires on weekends; this
	// guard only matters for the dev/test 2-minute schedule.
	//
	// On weekdays a 4-day grace window is used so that US public holidays
	// (where the previous session was up to 3 calendar days ago) don't
	// generate false positives.  Any gap larger than 4 days indicates a real
	// ingest failure.
	if j.freshness != nil && isWeekday(date) {
		latestSPY, err := j.freshness.GetLatestCandleDate(ctx, "SPY")
		if err != nil {
			j.log.Error("job failed: freshness check error",
				"job", jobName, "date", tag, "error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("MarketRegimeInputsJob [%s]: SPY freshness check: %w", tag, err)
		}

		cutoff := date.AddDate(0, 0, -4) // allow up to 4 calendar days gap
		if latestSPY.Before(cutoff) {
			err := fmt.Errorf(
				"SPY latest candle is %s, more than 4 days before %s — candle ingest may have failed",
				latestSPY.Format("2006-01-02"), tag,
			)
			j.log.Error("job failed: stale candle data",
				"job", jobName, "date", tag,
				"spy_latest_candle", latestSPY.Format("2006-01-02"),
				"cutoff", cutoff.Format("2006-01-02"),
				"error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("MarketRegimeInputsJob [%s]: %w", tag, err)
		}
	}

	// ── 1. Start ──────────────────────────────────────────────────────────────
	j.log.Info("job starting",
		"job", jobName,
		"date", tag,
	)

	// ── 2. Compute ────────────────────────────────────────────────────────────
	inputs, err := j.svc.BuildMarketInputs(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", jobName,
			"date", tag,
			"step", "compute",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeInputsJob [%s]: compute: %w", tag, err)
	}

	// ── 3. Persist (upsert — idempotent on conflict) ──────────────────────────
	row := marketInputsToModel(&inputs)
	if err := j.repo.InsertMarketInputs(ctx, &row); err != nil {
		j.log.Error("job failed",
			"job", jobName,
			"date", tag,
			"step", "persist",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeInputsJob [%s]: persist: %w", tag, err)
	}

	// ── 4. Summary log ────────────────────────────────────────────────────────
	j.log.Info("MarketInputsJob completed",
		// Identity
		"job", jobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		// SPY position (booleans for machine-readable downstream filtering)
		"spy_above_50", inputs.SpyAbove50,
		"spy_above_200", inputs.SpyAbove200,
		// QQQ position
		"qqq_above_50", inputs.QqqAbove50,
		"qqq_above_200", inputs.QqqAbove200,
		// Distribution pressure (IBD 20-session rolling count)
		"distribution_days", inputs.DistributionDays,
		// Market breadth (percentage of stocks above SMA, 0–100)
		"breadth_50", inputs.BreadthAbove50,
		"breadth_200", inputs.BreadthAbove200,
		// Relative-strength ratios (>1 = outperforming SPY)
		"qqq_vs_spy_rs", inputs.QqqvsspyRs,
		"iwm_vs_spy_rs", inputs.IwmvsspyRs,
	)

	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// marketInputsToModel converts a services.MarketInputs value to the DB entity.
// The conversion lives here to keep the models and services packages free of
// dependencies on each other.
func marketInputsToModel(m *services.MarketInputs) models.MarketInputsDaily {
	return models.MarketInputsDaily{
		Date:             m.Date,
		SpyAbove50:       m.SpyAbove50,
		SpyAbove200:      m.SpyAbove200,
		QqqAbove50:       m.QqqAbove50,
		QqqAbove200:      m.QqqAbove200,
		DistributionDays: m.DistributionDays,
		BreadthAbove50:   m.BreadthAbove50,
		BreadthAbove200:  m.BreadthAbove200,
		QQQvsSPYRS:       m.QqqvsspyRs,
		IWMvsSPYRS:       m.IwmvsspyRs,
		// v2 signals
		SpyPctFromSMA50:     m.SpyPctFromSMA50,
		SpyPctFromSMA200:    m.SpyPctFromSMA200,
		QqqPctFromSMA50:     m.QqqPctFromSMA50,
		QqqPctFromSMA200:    m.QqqPctFromSMA200,
		SpySMA50AboveSMA200: m.SpySMA50AboveSMA200,
		QqqSMA50AboveSMA200: m.QqqSMA50AboveSMA200,
		SpyDrawdownPct:      m.SpyDrawdownPct,
		// R-02 regime signal enrichment
		VIXLevel:          vixLevelToPtr(m.VIXLevel),
		VIXROCpct:         vixROCToPtr(m.VIXLevel, m.VIXROCpct),
		TickMinDaily:      tickMinToPtr(m.TickMinDaily),
		BreadthVelocity5d: m.BreadthVelocity5d,
	}
}

// isWeekday reports whether d falls on Monday–Friday.
// Used by the freshness check to skip the candle-staleness gate on weekends
// when markets are closed and no new candle is expected.
func isWeekday(d time.Time) bool {
	wd := d.Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// vixLevelToPtr converts a float64 VIX level to *float64.
// Returns nil when v is <= 0, meaning VIX data was unavailable.
// VIX is an index that is never 0 in practice, so 0 reliably signals "no data".
func vixLevelToPtr(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// vixROCToPtr converts the VIX ROC to *float64.
// Returns nil when VIX data was unavailable (level <= 0) and the
// computed ROC value is 0 (the zero value left by the unset path).
// When VIX data IS present but ROC happens to compute to 0.0
// (e.g. VIX didn't change intraday), a non-nil VIX level means we
// record the 0.0 ROC explicitly rather than treating it as missing.
func vixROCToPtr(vixLevel, vixROC float64) *float64 {
	if vixLevel <= 0 {
		return nil
	}
	return &vixROC
}

// tickMinToPtr converts a float64 to *float64.
// Returns nil when v is 0.0 (indicating no data available for $TICK).
// The classifier's ScoreTick handles nil by returning a neutral 0.25 score.
func tickMinToPtr(v float64) *float64 {
	if v == 0.0 {
		return nil
	}
	return &v
}
