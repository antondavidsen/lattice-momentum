package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/regime"
)

// classificationJobName is emitted in every log line produced by this job.
const classificationJobName = "MarketRegimeClassificationJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// marketInputsLoader is the read subset of repository.MarketInputsRepo that
// the classification job requires.
type marketInputsLoader interface {
	GetMarketInputs(ctx context.Context, date time.Time) (*models.MarketInputsDaily, error)
}

// marketRegimeStorer covers all read/write operations the job performs against
// the regime tables:
//   - GetLatestSmoothedBullStrength — reads previous EMA state for smoothing
//   - UpsertMarketRegimeDaily       — archives the classified result
//   - UpsertMarketRegime            — keeps the legacy singleton current
type marketRegimeStorer interface {
	GetLatestSmoothedBullStrength(ctx context.Context) (float64, error)
	UpsertMarketRegimeDaily(ctx context.Context, m *models.MarketRegimeDaily) error
	UpsertMarketRegime(ctx context.Context, m *models.MarketRegime) error
}

// Compile-time assertions: production types must satisfy the interfaces.
var _ marketInputsLoader = (*repository.MarketInputsRepo)(nil)
var _ marketRegimeStorer = (*repository.MarketRegimeRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// MarketRegimeClassificationJob reads today's pre-computed market signals,
// applies the v2 classifier (continuous SMA distance, RS ratios, golden cross,
// EMA smoothing, drawdown cap), and persists the result to both
// market_regime_daily and the legacy market_regime table.
type MarketRegimeClassificationJob struct {
	inputs marketInputsLoader
	repo   marketRegimeStorer
	log    *slog.Logger
}

// NewMarketRegimeClassificationJob constructs a job from the production types.
func NewMarketRegimeClassificationJob(
	inputs *repository.MarketInputsRepo,
	repo *repository.MarketRegimeRepo,
	log *slog.Logger,
) *MarketRegimeClassificationJob {
	return &MarketRegimeClassificationJob{inputs: inputs, repo: repo, log: log}
}

// NewMarketRegimeClassificationJobFromSources constructs a job from any values
// that satisfy the loader and storer interfaces.  Intended for tests.
func NewMarketRegimeClassificationJobFromSources(
	inputs marketInputsLoader,
	repo marketRegimeStorer,
	log *slog.Logger,
) *MarketRegimeClassificationJob {
	return &MarketRegimeClassificationJob{inputs: inputs, repo: repo, log: log}
}

// RunMarketRegimeClassificationJob executes the classify-and-persist cycle.
//
// Steps:
//  1. Load today's signals from market_inputs_daily.
//  2. Read the previous session's smoothed_bull_strength (for EMA continuity).
//  3. Run regime.ClassifyRegime with EMA smoothing + drawdown cap.
//  4. Upsert result into market_regime_daily (historical archive).
//  5. Upsert result into market_regime (legacy singleton).
//  6. Emit a structured summary log.
func (j *MarketRegimeClassificationJob) RunMarketRegimeClassificationJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	j.log.Info("job starting", "job", classificationJobName, "date", tag)

	// ── 1. Load today's signals ───────────────────────────────────────────────
	mInputs, err := j.inputs.GetMarketInputs(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", classificationJobName, "date", tag,
			"step", "load_inputs", "error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeClassificationJob [%s]: load inputs: %w", tag, err)
	}

	// ── 2. Load previous smoothed bull strength (EMA seed) ────────────────────
	prevSmoothed, err := j.repo.GetLatestSmoothedBullStrength(ctx)
	if err != nil {
		j.log.Error("job failed",
			"job", classificationJobName, "date", tag,
			"step", "load_prev_smoothed", "error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeClassificationJob [%s]: load prev smoothed: %w", tag, err)
	}

	// ── 3. Classify ───────────────────────────────────────────────────────────
	result := regime.ClassifyRegime(mInputs, prevSmoothed)

	// confidence (0–100) stores the smoothed bull strength so callers that
	// read only the confidence column get the smoothed value directly.
	bullConfidence := result.SmoothedBullStrength * 100.0

	// ── 4. Persist: market_regime_daily ───────────────────────────────────────
	daily := &models.MarketRegimeDaily{
		Date:                 date,
		Regime:               result.Label.String(),
		Confidence:           &bullConfidence,
		RawBullStrength:      &result.RawBullStrength,
		SmoothedBullStrength: &result.SmoothedBullStrength,
	}
	if err := j.repo.UpsertMarketRegimeDaily(ctx, daily); err != nil {
		j.log.Error("job failed",
			"job", classificationJobName, "date", tag,
			"step", "persist_daily", "error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeClassificationJob [%s]: persist market_regime_daily: %w", tag, err)
	}

	// ── 5. Persist: market_regime (legacy singleton) ──────────────────────────
	// Raw price / SMA columns are not available in the classification job
	// (they live in candles_daily, not market_inputs_daily), so they remain
	// NULL.  The boolean above-SMA flags ARE available and are populated so
	// the table stays useful for simple regime queries.
	latest := &models.MarketRegime{
		Date:           date,
		Regime:         result.Label.String(),
		SPYAboveSMA50:  &mInputs.SpyAbove50,
		SPYAboveSMA200: &mInputs.SpyAbove200,
		QQQAboveSMA50:  &mInputs.QqqAbove50,
		QQQAboveSMA200: &mInputs.QqqAbove200,
	}
	if err := j.repo.UpsertMarketRegime(ctx, latest); err != nil {
		j.log.Error("job failed",
			"job", classificationJobName, "date", tag,
			"step", "persist_singleton", "error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("MarketRegimeClassificationJob [%s]: persist market_regime: %w", tag, err)
	}

	// ── 6. Summary log ────────────────────────────────────────────────────────
	j.log.Info("MarketRegimeClassificationJob completed",
		"job", classificationJobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		"regime", result.Label,
		"raw_bull_strength", fmt.Sprintf("%.4f", result.RawBullStrength),
		"smoothed_bull_strength", fmt.Sprintf("%.4f", result.SmoothedBullStrength),
		"risk_score", fmt.Sprintf("%.4f", result.RiskScore),
		"prev_smoothed", fmt.Sprintf("%.4f", prevSmoothed),
		"spy_drawdown_pct", fmt.Sprintf("%.2f%%", mInputs.SpyDrawdownPct),
		"spy_above_50", mInputs.SpyAbove50,
		"spy_above_200", mInputs.SpyAbove200,
		"distribution_days", mInputs.DistributionDays,
		"breadth_50", fmt.Sprintf("%.1f%%", mInputs.BreadthAbove50),
		"qqq_vs_spy_rs", fmt.Sprintf("%.4f", mInputs.QQQvsSPYRS),
		"iwm_vs_spy_rs", fmt.Sprintf("%.4f", mInputs.IWMvsSPYRS),
	)

	return nil
}
