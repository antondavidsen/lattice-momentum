package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	llmsvc "ai-stock-service/internal/services/llm"
)

const commercialReportJobName = "CommercialReportJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// commercialReportTransformer is the subset of CommercialReportService the job requires.
type commercialReportTransformer interface {
	Transform(
		ctx context.Context,
		date time.Time,
		evals []models.LLMListEvaluation,
		regime string,
	) (*llmsvc.CommercialReportResult, error)
}

// commercialReportStorer persists commercial report results.
type commercialReportStorer interface {
	UpsertCommercialReport(ctx context.Context, m *models.CommercialReport) error
	GetByDate(ctx context.Context, date time.Time) (*models.CommercialReport, error)
}

// evaluationLoader reads LLM evaluations from the database.
type evaluationLoader interface {
	GetEvaluationsByDate(ctx context.Context, date time.Time) ([]models.LLMListEvaluation, error)
}

// regimeLoader reads the market regime classification.
type regimeLoader interface {
	GetMarketRegimeDaily(ctx context.Context, date time.Time) (*models.MarketRegimeDaily, error)
}

// breadthDivergenceLoader reads the breadth divergence signal.
type breadthDivergenceLoader interface {
	GetBreadthDivergence(ctx context.Context, date time.Time) (float64, error)
}

// Compile-time assertions.
var _ commercialReportStorer = (*repository.CommercialReportRepo)(nil)
var _ evaluationLoader = (*repository.LLMListEvaluationRepo)(nil)
var _ regimeLoader = (*repository.MarketRegimeRepo)(nil)
var _ breadthDivergenceLoader = (*repository.MarketRegimeRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// CommercialReportJob transforms institutional memos into subscriber-friendly
// reports. It is Step 11 of the nightly pipeline (runs after LLM evaluation).
type CommercialReportJob struct {
	transformer commercialReportTransformer
	evalRepo    evaluationLoader
	reportRepo  commercialReportStorer
	regimeRepo  regimeLoader
	breadthRepo breadthDivergenceLoader // R-11: breadth divergence signal
	log         *slog.Logger
}

// NewCommercialReportJob constructs a job from production concrete types.
func NewCommercialReportJob(
	transformer *llmsvc.CommercialReportService,
	evalRepo *repository.LLMListEvaluationRepo,
	reportRepo *repository.CommercialReportRepo,
	regimeRepo *repository.MarketRegimeRepo,
	log *slog.Logger,
) *CommercialReportJob {
	return &CommercialReportJob{
		transformer: transformer,
		evalRepo:    evalRepo,
		reportRepo:  reportRepo,
		regimeRepo:  regimeRepo,
		log:         log,
	}
}

// NewCommercialReportJobFromSources constructs a job from interface values.
// Intended for tests.
func NewCommercialReportJobFromSources(
	transformer commercialReportTransformer,
	evalRepo evaluationLoader,
	reportRepo commercialReportStorer,
	regimeRepo regimeLoader,
	log *slog.Logger,
) *CommercialReportJob {
	return &CommercialReportJob{
		transformer: transformer,
		evalRepo:    evalRepo,
		reportRepo:  reportRepo,
		regimeRepo:  regimeRepo,
		log:         log,
	}
}

// WithBreadthDivergence attaches a breadthDivergenceLoader for the R-11
// distribution warning signal and returns the job for fluent chaining.
func (j *CommercialReportJob) WithBreadthDivergence(src breadthDivergenceLoader) *CommercialReportJob {
	j.breadthRepo = src
	return j
}

// RunCommercialReportJob executes the transformation for the given date.
//
// Flow:
//  1. Check idempotency — skip if commercial report already exists for date.
//  2. Load all LLM evaluations for the date.
//  3. Load market regime for context.
//  4. Call CommercialReportService.Transform with the raw memos.
//  5. Persist the result.
//
// gateLevel is the R-09 circuit breaker gate level ("full", "ep_only", "halt").
// When gateLevel != "full", a regime warning is prepended to the risk note.
func (j *CommercialReportJob) RunCommercialReportJob(ctx context.Context, date time.Time, gateLevel string) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	j.log.Info("job starting",
		"job", commercialReportJobName,
		"date", tag,
	)

	// ── 1. Idempotency check ──────────────────────────────────────────────────
	existing, err := j.reportRepo.GetByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("%s [%s]: idempotency check: %w", commercialReportJobName, tag, err)
	}
	if existing != nil {
		j.log.Info("commercial report already exists — skipping",
			"job", commercialReportJobName,
			"date", tag,
		)
		return nil
	}

	// ── 2. Load evaluations ───────────────────────────────────────────────────
	evals, err := j.evalRepo.GetEvaluationsByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("%s [%s]: load evaluations: %w", commercialReportJobName, tag, err)
	}
	if len(evals) == 0 {
		j.log.Warn("no LLM evaluations found for date — skipping commercial report",
			"job", commercialReportJobName,
			"date", tag,
		)
		return nil
	}

	// ── 3. Load regime ────────────────────────────────────────────────────────
	regime := "neutral"
	if regimeDaily, regimeErr := j.regimeRepo.GetMarketRegimeDaily(ctx, date); regimeErr != nil {
		j.log.Warn("could not load regime for commercial report, defaulting to neutral",
			"job", commercialReportJobName,
			"date", tag,
			"error", regimeErr,
		)
	} else {
		regime = regimeDaily.Regime
	}

	// ── 3b. Load breadth divergence signal (R-11) ─────────────────────────────
	breadthDivergence := 0.0
	if j.breadthRepo != nil {
		if bd, bdErr := j.breadthRepo.GetBreadthDivergence(ctx, date); bdErr != nil {
			j.log.Warn("could not load breadth divergence signal, defaulting to 0",
				"job", commercialReportJobName,
				"date", tag,
				"error", bdErr,
			)
		} else {
			breadthDivergence = bd
		}
	}

	// ── 4. Transform ──────────────────────────────────────────────────────────
	result, err := j.transformer.Transform(ctx, date, evals, regime)
	if err != nil {
		return fmt.Errorf("%s [%s]: transform: %w", commercialReportJobName, tag, err)
	}

	// ── 4b. Apply breadth divergence adjustments (R-11) ───────────────────────
	// When breadth_divergence_signal == 1.0, reduce all position sizes by 30%
	// and add a distribution warning to the risk note.
	if breadthDivergence == 1.0 {
		for i := range result.TradeCards {
			result.TradeCards[i].PositionSizeGuidance = applyPositionSizeReduction(result.TradeCards[i].PositionSizeGuidance)
		}
		if result.RiskNote != "" {
			result.RiskNote = "⚠️ DISTRIBUTION WARNING: Market breadth is diverging from price. " +
				"SPY is rising but fewer stocks are participating. " +
				"All position sizes reduced by 30%. " +
				result.RiskNote
		} else {
			result.RiskNote = "⚠️ DISTRIBUTION WARNING: Market breadth is diverging from price. " +
				"SPY is rising but fewer stocks are participating. " +
				"All position sizes reduced by 30%."
		}
		j.log.Warn("breadth divergence detected — position sizes reduced by 30%",
			"job", commercialReportJobName,
			"date", tag,
		)
	}

	// ── 4c. Apply regime warning (R-09) ──────────────────────────────────────
	// When gate level is not "full", prepend a regime warning to the risk note.
	if gateLevel != "" && gateLevel != "full" {
		var warning string
		switch gateLevel {
		case "ep_only":
			warning = "⚠️ MARKET CORRECTION: Only catalyst-driven (EP) list is shown. " +
				"Momentum and Leaders lists are suppressed due to elevated market risk. " +
				"Focus on high-conviction setups with defined catalysts."
		case "halt":
			warning = "🚫 MARKET CLOSED TO NEW IDEAS: No ranking lists are generated today. " +
				"The market is in a bear or high-correction regime with elevated VIX. " +
				"Prioritise risk management and wait for conditions to improve."
		}
		if warning != "" {
			if result.RiskNote != "" {
				result.RiskNote = warning + "\n\n" + result.RiskNote
			} else {
				result.RiskNote = warning
			}
		}
		j.log.Info("regime warning applied to commercial report",
			"job", commercialReportJobName,
			"date", tag,
			"gate_level", gateLevel,
		)
	}

	// ── 5. Persist ────────────────────────────────────────────────────────────
	row := result.ToModel()
	if err := j.reportRepo.UpsertCommercialReport(ctx, row); err != nil {
		return fmt.Errorf("%s [%s]: persist: %w", commercialReportJobName, tag, err)
	}

	j.log.Info("job completed",
		"job", commercialReportJobName,
		"date", tag,
		"headline", result.Headline,
		"trade_cards", len(result.TradeCards),
		"provider", result.Provider,
		"model", result.Model,
		"breadth_divergence", breadthDivergence,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return nil
}

// applyPositionSizeReduction reduces a position size guidance string by 30%.
// It parses common patterns like "5-10%", "8%", "Half position" and scales them.
// Returns the original string if no pattern is matched.
func applyPositionSizeReduction(guidance string) string {
	if guidance == "" {
		return ""
	}

	// Pattern: "X-Y%" range → reduce both bounds by 30%
	reRange := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*-\s*(\d+(?:\.\d+)?)\s*%`)
	if matches := reRange.FindStringSubmatch(guidance); len(matches) == 3 {
		low, _ := strconv.ParseFloat(matches[1], 64)
		high, _ := strconv.ParseFloat(matches[2], 64)
		low = math.Round(low*0.7*10) / 10
		high = math.Round(high*0.7*10) / 10
		return fmt.Sprintf("%.1f-%.1f%%", low, high)
	}

	// Pattern: "X%" single value
	reSingle := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`)
	if matches := reSingle.FindStringSubmatch(guidance); len(matches) == 2 {
		val, _ := strconv.ParseFloat(matches[1], 64)
		val = math.Round(val*0.7*10) / 10
		return fmt.Sprintf("%.1f%%", val)
	}

	// Pattern: "Half position" → "Third position" (roughly 30% reduction)
	if strings.Contains(strings.ToLower(guidance), "half") {
		return strings.Replace(guidance, "Half", "Third", 1)
	}
	if strings.Contains(strings.ToLower(guidance), "full") {
		return strings.Replace(guidance, "Full", "Two-thirds", 1)
	}

	// Fallback: append reduction note
	return guidance + " (reduced 30%)"
}
