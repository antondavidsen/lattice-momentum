package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/outcomes"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

type evalSource interface {
	GetEvaluationsByDate(ctx context.Context, date time.Time) ([]models.LLMListEvaluation, error)
}

type outcomeSource interface {
	GetTradeOutcomes(ctx context.Context, entryDate time.Time) ([]models.TradeOutcomeDaily, error)
}

type attributionCandleSource interface {
	GetCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}

type ptoStorer interface {
	UpsertOutcome(ctx context.Context, m *models.PromptTickerOutcome) error
}

// Compile-time assertions.
var _ evalSource = (*repository.LLMListEvaluationRepo)(nil)
var _ outcomeSource = (*repository.TradeOutcomeRepo)(nil)
var _ ptoStorer = (*repository.PromptTickerOutcomeRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// PromptOutcomeAttributionJob joins parsed LLM recommendations with actual
// forward returns to measure prompt quality at the per-ticker level.
// R03: Uses path-aware OHLC replay for stop/target simulation instead of
// path-blind flag setting. Validates level geometry and R/R floors at write time.
type PromptOutcomeAttributionJob struct {
	evalRepo    evalSource
	outcomeRepo outcomeSource
	candleRepo  attributionCandleSource
	ptoRepo     ptoStorer
	log         *slog.Logger
}

// NewPromptOutcomeAttributionJob constructs a job from production concrete types.
func NewPromptOutcomeAttributionJob(
	evalRepo *repository.LLMListEvaluationRepo,
	outcomeRepo *repository.TradeOutcomeRepo,
	candleRepo *repository.CandlesDailyRepo,
	ptoRepo *repository.PromptTickerOutcomeRepo,
	log *slog.Logger,
) *PromptOutcomeAttributionJob {
	return &PromptOutcomeAttributionJob{
		evalRepo:    evalRepo,
		outcomeRepo: outcomeRepo,
		candleRepo:  candleRepo,
		ptoRepo:     ptoRepo,
		log:         log,
	}
}

// NewPromptOutcomeAttributionJobFromSources constructs from interfaces (for tests).
func NewPromptOutcomeAttributionJobFromSources(
	evalRepo evalSource,
	outcomeRepo outcomeSource,
	candleRepo attributionCandleSource,
	ptoRepo ptoStorer,
	log *slog.Logger,
) *PromptOutcomeAttributionJob {
	return &PromptOutcomeAttributionJob{
		evalRepo:    evalRepo,
		outcomeRepo: outcomeRepo,
		candleRepo:  candleRepo,
		ptoRepo:     ptoRepo,
		log:         log,
	}
}

// RunAttributionJob processes evaluations for the past 30 days.
// R03: Replaced path-blind flag-setting with sequenced OHLC replay + level validation.
func (j *PromptOutcomeAttributionJob) RunAttributionJob(ctx context.Context, today time.Time) error {
	start := time.Now()
	j.log.Info("prompt outcome attribution starting")

	var (
		upserted int
		skipped  int
		errored  int
	)

	for daysBack := 1; daysBack <= 30; daysBack++ {
		date := today.AddDate(0, 0, -daysBack)

		evaluations, err := j.evalRepo.GetEvaluationsByDate(ctx, date)
		if err != nil {
			j.log.Warn("attribution: load evaluations failed", "date", date, "error", err)
			continue
		}
		if len(evaluations) == 0 {
			continue
		}

		// Load trade outcomes for this date.
		tradeOutcomes, err := j.outcomeRepo.GetTradeOutcomes(ctx, date)
		if err != nil {
			j.log.Warn("attribution: load trade outcomes failed", "date", date, "error", err)
			continue
		}

		// Build outcome lookup map: (list_type, ticker) -> outcome.
		outcomeMap := make(map[string]models.TradeOutcomeDaily, len(tradeOutcomes))
		for i := range tradeOutcomes {
			o := &tradeOutcomes[i]
			key := string(o.ListType) + "|" + o.Ticker
			outcomeMap[key] = *o
		}

		for i := range evaluations {
			eval := &evaluations[i]
			// Parse the parsed_json.
			var parsed models.EvaluationParsedOutput
			if len(eval.ParsedJSON) == 0 || string(eval.ParsedJSON) == "{}" {
				skipped++
				continue
			}
			if err := json.Unmarshal(eval.ParsedJSON, &parsed); err != nil {
				j.log.Warn("attribution: unmarshal parsed_json", "date", date, "error", err)
				skipped++
				continue
			}
			if !parsed.ParseSuccess || len(parsed.Tickers) == 0 {
				skipped++
				continue
			}

			// Build recommended ticker set.
			recommendedSet := make(map[string]models.EvaluationParsedTicker, len(parsed.Tickers))
			for i := range parsed.Tickers {
				t := &parsed.Tickers[i]
				recommendedSet[t.Ticker] = *t
			}

			// Extract ml_feature_update from the outcome analyzer output (if present).
			var mlFeatureName *string
			var mlWeightDelta *float64
			var mlSuggestionConfidence *float64
			if parsed.MLFeatureUpdate != nil {
				mlFeatureName = &parsed.MLFeatureUpdate.FeatureName
				mlWeightDelta = &parsed.MLFeatureUpdate.SuggestedWeightDelta
				mlSuggestionConfidence = &parsed.MLFeatureUpdate.Confidence
			}

			// Process ALL input tickers (recommended + rejected).
			for _, ticker := range eval.InputTickers {
				recommended := false
				var parsedTicker models.EvaluationParsedTicker
				if pt, ok := recommendedSet[ticker]; ok {
					recommended = true
					parsedTicker = pt
				}

				pto := &models.PromptTickerOutcome{
					Date:                   eval.Date,
					ListType:               eval.ListType,
					Ticker:                 ticker,
					PromptVersion:          eval.PromptVersion,
					LLMRecommended:         recommended,
					MLFeatureName:          mlFeatureName,
					MLWeightDelta:          mlWeightDelta,
					MLSuggestionConfidence: mlSuggestionConfidence,
				}

				// Copy recommendation fields for recommended tickers.
				if recommended {
					pto.RecommendedSetup = strPtr(parsedTicker.Setup)
					pto.RecommendedEntryLow = parsedTicker.EntryLow
					pto.RecommendedEntryHigh = parsedTicker.EntryHigh
					pto.RecommendedStop = parsedTicker.StopPrice
					pto.RecommendedTarget1 = parsedTicker.Target1
					pto.RecommendedTarget2 = parsedTicker.Target2
					pto.RecommendedRR = parsedTicker.RiskReward
					pto.RecommendedSize = strPtr(parsedTicker.PositionSize)
					pto.RecommendedConviction = strPtr(parsedTicker.Conviction)

					// Disqualifier fields (R-06).
					pto.Disqualified = parsedTicker.Disqualified
					if parsedTicker.DisqualifierReason != "" {
						pto.DisqualifierReason = strPtr(parsedTicker.DisqualifierReason)
					}
				}

				// Copy actual outcomes from trade_outcomes_daily.
				key := string(eval.ListType) + "|" + ticker
				if outcome, ok := outcomeMap[key]; ok {
					pto.ActualEntryPrice = &outcome.EntryPrice
					pto.ActualReturn5D = outcome.Return5D
					pto.ActualReturn10D = outcome.Return10D
					pto.ActualReturn20D = outcome.Return20D
					pto.ActualMaxRunup = outcome.MaxRunup20D
					pto.ActualMaxDrawdown = outcome.MaxDrawdown20D
					pto.EvaluatedDays = outcome.EvaluatedDays
				}

				// R03: Level validation runs even without candle data (fresh trades).
				// Path-aware exit simulation requires candle data (EvaluatedDays > 0).
				if recommended {
					if parsedTicker.EntryLow != nil && parsedTicker.EntryHigh != nil && parsedTicker.StopPrice != nil {
						if err := outcomes.ValidateLevels(
							*parsedTicker.EntryLow, *parsedTicker.EntryHigh, *parsedTicker.StopPrice,
							parsedTicker.Target1, parsedTicker.Target2, parsedTicker.Setup,
						); err != nil {
							pto.LevelsInvalid = true
							j.log.Warn("attribution: level validation failed",
								"ticker", pto.Ticker, "date", pto.Date,
								"setup", parsedTicker.Setup, "error", err.Error())
						}
					}
					if pto.EvaluatedDays > 0 {
						j.computePathAwareExit(ctx, pto, &parsedTicker, date)
					}
				}

				if err := j.ptoRepo.UpsertOutcome(ctx, pto); err != nil {
					j.log.Warn("attribution: upsert failed",
						"date", date, "ticker", ticker, "error", err)
					errored++
				} else {
					upserted++
				}
			}
		}
	}

	j.log.Info("prompt outcome attribution complete",
		"upserted", upserted,
		"skipped", skipped,
		"errored", errored,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// computePathAwareExit runs path-aware OHLC replay for exit simulation.
// Level validation (ValidateLevels) is performed inline in RunAttributionJob
// before this function is called. pto.LevelsInvalid must be checked before
// using any exit fields populated here.
func (j *PromptOutcomeAttributionJob) computePathAwareExit(
	ctx context.Context,
	pto *models.PromptTickerOutcome,
	pt *models.EvaluationParsedTicker,
	signalDate time.Time,
) {
	// Extract prices from parsed ticker.
	entryLow := pt.EntryLow
	entryHigh := pt.EntryHigh
	stop := pt.StopPrice
	t1 := pt.Target1
	t2 := pt.Target2

	// Must have entry and stop to simulate.
	if entryLow == nil || entryHigh == nil || stop == nil {
		return
	}

	// Compute entry price as midpoint of entry range (T+1 open convention).
	entryPrice := (*entryLow + *entryHigh) / 2.0

	// Skip replay if levels were already flagged as invalid.
	if pto.LevelsInvalid {
		return
	}

	// ── Path-aware OHLC replay (R03) ──────────────────────────────────────────
	// Build candle loader from the job's candle repo (injectable via interface).
	loadCandles := func(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error) {
		return j.candleRepo.GetCandles(ctx, ticker, from, to)
	}

	// Dereference optional targets for ReplayExitSequence.
	// nil/zero T2 means no T2 is set (the simulator handles target2 > 0 checks).
	var t1Val, t2Val float64
	if t1 != nil {
		t1Val = *t1
	}
	if t2 != nil {
		t2Val = *t2
	}

	exitResult, err := outcomes.ReplayExitSequence(
		ctx,
		pto.Ticker,
		signalDate,
		entryPrice,
		*stop,
		t1Val,
		t2Val,
		loadCandles,
	)
	if err != nil {
		j.log.Warn("attribution: exit simulation failed",
			"ticker", pto.Ticker, "date", pto.Date, "error", err.Error())
		return
	}

	// ── Map ExitResult to PTO fields ──────────────────────────────────────────
	// Preserve old flag columns for backwards compatibility (read-only artifacts).
	pto.StopHit = &exitResult.StopHit
	pto.Target1Hit = &exitResult.T1Hit
	pto.Target2Hit = &exitResult.T2Hit
	if exitResult.ExitType != "" {
		pto.ExitType = &exitResult.ExitType
	}
	pto.ExitPrice = &exitResult.ExitPrice
	pto.ExitDate = &exitResult.ExitDate
	pto.T1Hit = exitResult.T1Hit

	// R03: actual_rr_achieved computed ONLY from sequenced ExitResult,
	// NOT from max_runup / max_drawdown (which are retained for other uses).
	if !math.IsNaN(exitResult.ActualRR) && !math.IsInf(exitResult.ActualRR, 0) {
		pto.ActualRRAchieved = &exitResult.ActualRR
	} else {
		// Fallback only if replay produced degenerate R/R.
		pto.ActualRRAchieved = nil
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
