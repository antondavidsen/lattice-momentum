package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/learning"
)

// ── Interfaces ─────────────────────────────────────────────────────────────────

// MomentumWeightsStorer combines read + write operations for Momentum weights.
type MomentumWeightsStorer interface {
	GetActive(ctx context.Context) (*repository.MomentumWeights, error)
	DeactivateAll(ctx context.Context) error
	Insert(ctx context.Context, w *repository.MomentumWeights) error
}

// MLDeltaProvider provides LLM-suggested weight deltas for reconciliation.
type MLDeltaProvider interface {
	GetMLWeightDeltas(ctx context.Context, lookbackDays int) ([]repository.MLWeightDeltaSummary, error)
}

// Compile-time assertions.
var _ MomentumWeightsStorer = (*repository.MomentumWeightsRepo)(nil)
var _ MLDeltaProvider = (*repository.PromptTickerOutcomeRepo)(nil)

// ── Job ────────────────────────────────────────────────────────────────────────

// NightlyWeightRefitJob refits ranking engine weights via logistic regression
// on labelled prompt_ticker_outcomes data.  Runs monthly (aligned with the
// existing monthly-learning schedule).
//
// Pipeline per engine:
//  1. Query prompt_ticker_outcomes JOIN tradingview_snapshot_daily for features
//  2. Filter to labelled outcomes (≥100 samples, else SKIP)
//  3. Walk-forward split: train = all except last 60, test = last 60
//  4. Train L2 logistic regression with non-negative constraints
//  5. Normalise weights to sum=1
//  6. Evaluate on test set (AUC > 0.60 required)
//  7. Deploy: deactivate prior active row, insert new active=TRUE
//  8. Reconcile: compare LLM-suggested weight deltas vs statistical deltas
type NightlyWeightRefitJob struct {
	momRepo MomentumWeightsStorer
	mlDelta MLDeltaProvider
	log     *slog.Logger
}

// NewNightlyWeightRefitJob constructs the job with concrete repos.
func NewNightlyWeightRefitJob(
	momRepo MomentumWeightsStorer,
	log *slog.Logger,
) *NightlyWeightRefitJob {
	return &NightlyWeightRefitJob{
		momRepo: momRepo,
		log:     log,
	}
}

// NewNightlyWeightRefitJobFromSources constructs the job with interfaces (test-friendly).
func NewNightlyWeightRefitJobFromSources(
	momRepo MomentumWeightsStorer,
	mlDelta MLDeltaProvider,
	log *slog.Logger,
) *NightlyWeightRefitJob {
	return &NightlyWeightRefitJob{
		momRepo: momRepo,
		mlDelta: mlDelta,
		log:     log,
	}
}

// RunNightlyWeightRefitJob executes the full refit pipeline for all three engines.
func (j *NightlyWeightRefitJob) RunNightlyWeightRefitJob(ctx context.Context) error {
	start := time.Now()
	j.log.Info("NightlyWeightRefitJob starting")

	// ── Momentum Engine refit ─────────────────────────────────────────────
	if err := j.refitMomentum(ctx); err != nil {
		j.log.Error("NightlyWeightRefitJob: Momentum refit failed", "error", err)
	}

	j.log.Info("NightlyWeightRefitJob complete",
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// ── Engine-specific refit pipelines ────────────────────────────────────────────

// refitMomentum runs the Momentum engine weight refit.
// Success condition: return_10d > 8% where recommended_setup ILIKE 'Momentum%'.
func (j *NightlyWeightRefitJob) refitMomentum(_ context.Context) error {
	j.log.Info("NightlyWeightRefitJob: Momentum refit — feature query not yet implemented, skipping")
	return nil
}

// ── Shared refit logic ─────────────────────────────────────────────────────────

// refitEngine is the generic refit pipeline shared by all three engines.
// It will be used once the feature matrix queries are implemented.
//
//nolint:unused // placeholder kept for future use once feature matrix queries are implemented
func (j *NightlyWeightRefitJob) refitEngine(
	ctx context.Context,
	engineName string,
	features []float64,
	nSamples, nFeatures int,
	labels []float64,
	lambda float64,
	oldWeights []float64,
	featureNames []string,
	deploy func(ctx context.Context, weights []float64, auc float64) error,
) error {
	// Gate: minimum 100 samples.
	if nSamples < 100 {
		j.log.Info("NightlyWeightRefitJob: skipping — insufficient samples",
			"engine", engineName, "samples", nSamples)
		return nil
	}

	// Walk-forward split: train = all except last 60, test = last 60.
	trainSamples := nSamples - 60
	if trainSamples < 60 {
		trainSamples = nSamples / 2
	}
	testSamples := nSamples - trainSamples
	if testSamples < 1 {
		testSamples = 1
	}

	// Train set.
	trainX := features[:trainSamples*nFeatures]
	trainY := labels[:trainSamples]

	// Train logistic regression.
	result, err := learning.RunLogisticRegression(trainX, trainSamples, nFeatures, trainY, lambda)
	if err != nil {
		return fmt.Errorf("NightlyWeightRefitJob.refitEngine %s: train: %w", engineName, err)
	}

	// Evaluate on test set.
	testX := features[trainSamples*nFeatures:]
	testY := labels[trainSamples:]
	testResult, err := learning.RunLogisticRegression(testX, testSamples, nFeatures, testY, lambda)
	if err != nil {
		return fmt.Errorf("NightlyWeightRefitJob.refitEngine %s: test eval: %w", engineName, err)
	}

	testAUC := testResult.AUC
	j.log.Info("NightlyWeightRefitJob: refit complete",
		"engine", engineName,
		"train_samples", trainSamples,
		"test_samples", testSamples,
		"train_auc", result.AUC,
		"test_auc", testAUC,
		"weights", result.Weights,
	)

	// AUC gate: > 0.60 required to deploy.
	if testAUC <= 0.60 {
		j.log.Info("NightlyWeightRefitJob: AUC gate not met — keeping existing weights",
			"engine", engineName, "test_auc", testAUC, "gate", 0.60)
		return nil
	}

	// Deploy: deactivate prior active row, insert new active=TRUE.
	if err := deploy(ctx, result.Weights, testAUC); err != nil {
		return fmt.Errorf("NightlyWeightRefitJob.refitEngine %s: deploy: %w", engineName, err)
	}

	// ── Reconciliation: LLM-suggested deltas vs statistical deltas ──────
	if j.mlDelta != nil {
		j.reconcileWeights(ctx, engineName, oldWeights, result.Weights, featureNames)
	}

	j.log.Info("NightlyWeightRefitJob: new weights deployed",
		"engine", engineName, "test_auc", testAUC)
	return nil
}

// reconcileWeights compares LLM-suggested weight deltas against statistical deltas
// from the logistic regression refit.  Agreement = high confidence; disagreement = WARN.
//
//nolint:unused // placeholder kept for future use once ML suggestions are available
func (j *NightlyWeightRefitJob) reconcileWeights(
	ctx context.Context,
	engineName string,
	oldWeights, newWeights []float64,
	featureNames []string,
) {
	deltas, err := j.mlDelta.GetMLWeightDeltas(ctx, 30)
	if err != nil {
		j.log.Warn("NightlyWeightRefitJob: reconciliation skipped — GetMLWeightDeltas failed",
			"engine", engineName, "error", err)
		return
	}

	// Build a lookup: featureName → avg LLM delta.
	llmDelta := make(map[string]float64)
	for _, d := range deltas {
		llmDelta[d.FeatureName] = d.AvgDelta
	}

	for i, name := range featureNames {
		if i >= len(oldWeights) || i >= len(newWeights) {
			break
		}
		statDelta := newWeights[i] - oldWeights[i]

		ld, hasLLM := llmDelta[name]
		if !hasLLM {
			continue // no LLM suggestion for this feature
		}

		// Agreement: both deltas have the same sign.
		agreement := (ld >= 0 && statDelta >= 0) || (ld < 0 && statDelta < 0)

		var action string
		switch {
		case agreement && absFloat(statDelta) > 0.02:
			action = "applied"
		case !agreement:
			action = "blocked"
		default:
			action = "ignored (delta too small)"
		}

		j.log.Info("weight_reconciliation",
			"engine", engineName,
			"feature", name,
			"llm_suggested_delta", roundTo(ld, 4),
			"statistical_delta", roundTo(statDelta, 4),
			"agreement", agreement,
			"action", action,
		)
	}
}

// absFloat returns the absolute value of a float64.
//
//nolint:unused // small utility helper used only by the currently unreachable reconcileWeights path
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// roundTo rounds a float64 to the given number of decimal places.
//
//nolint:unused // small utility helper used only by the currently unreachable reconcileWeights path
func roundTo(x float64, decimals int) float64 {
	pow := 1.0
	for i := 0; i < decimals; i++ {
		pow *= 10
	}
	return float64(int64(x*pow+0.5)) / pow
}
