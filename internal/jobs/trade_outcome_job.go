package jobs

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/outcomes"
)

// tradeOutcomeJobName is emitted in every log line produced by this job.
const tradeOutcomeJobName = "TradeOutcomeJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// outcomeComputer is the subset of outcomes.TradeOutcomeService the job requires.
type outcomeComputer interface {
	ComputeOutcomes(ctx context.Context, signalDate time.Time, today time.Time) ([]outcomes.TradeOutcomeResult, error)
}

// outcomeResultAdjuster handles corporate action adjustment and plausibility.
type outcomeResultAdjuster interface {
	AdjustForCorporateActions(ctx context.Context, ticker string, entryDate time.Time, rawReturn float64, horizonDays int) (float64, int, error)
	PlausibilityCheck(result *outcomes.TradeOutcomeResult) string
}

// outcomeStorer is the subset of repository.TradeOutcomeRepo the job requires.
type outcomeStorer interface {
	UpsertTradeOutcome(ctx context.Context, m *models.TradeOutcomeDaily) error
}

// primaryObservationChecker checks and updates is_primary_observation.
type primaryObservationChecker interface {
	GetPriorPrimaryObservation(ctx context.Context, ticker, listType string, entryDate time.Time) (*models.TradeOutcomeDaily, error)
	GetCrossListPriorTicker(ctx context.Context, ticker string, entryDate time.Time) (*models.TradeOutcomeDaily, error)
	MarkCrossListDuplicate(ctx context.Context, entryDate time.Time, listType, ticker string) error
	UpdateClusterID(ctx context.Context, entryDate time.Time, listType, ticker string, clusterID int64) error
}

// quarantineStorer writes quarantined trade outcomes.
type quarantineStorer interface {
	UpsertQuarantine(ctx context.Context, q *repository.QuarantineTradeOutcome) error
}

// duplicateDetector is the subset of repository.TradeOutcomeRepo that finds
// and marks duplicate signals.
type duplicateDetector interface {
	FindDuplicateSignals(ctx context.Context, windowDays int) ([]repository.DuplicateSignalPair, error)
	MarkDuplicateSignal(ctx context.Context, entryDate time.Time, listType string, ticker string, daysSincePrior int) error
}

// pendingDateFinder is the subset of repository.TradeOutcomeRepo that finds
// signal dates still requiring evaluation.
type pendingDateFinder interface {
	GetPendingSignalDates(ctx context.Context, today time.Time, lookback int) ([]time.Time, error)
}

// effectiveSampleReporter reports effective sample size.
type effectiveSampleReporter interface {
	GetEffectiveSampleSize(ctx context.Context, scope string) (rawN, effectiveN int, err error)
}

// Compile-time assertions.
var _ outcomeComputer = (*outcomes.TradeOutcomeService)(nil)
var _ outcomeResultAdjuster = (*outcomes.TradeOutcomeService)(nil)
var _ outcomeStorer = (*repository.TradeOutcomeRepo)(nil)
var _ quarantineStorer = (*repository.TradeOutcomeQuarantineRepo)(nil)
var _ pendingDateFinder = (*repository.TradeOutcomeRepo)(nil)
var _ duplicateDetector = (*repository.TradeOutcomeRepo)(nil)
var _ primaryObservationChecker = (*repository.TradeOutcomeRepo)(nil)
var _ effectiveSampleReporter = (*repository.TradeOutcomeRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// TradeOutcomeJob computes and persists forward-performance metrics for all
// published trade signals.  Designed to be constructed once at startup and
// called once per nightly pipeline run.
type TradeOutcomeJob struct {
	svc            outcomeComputer
	adjuster       outcomeResultAdjuster
	repo           outcomeStorer
	quarantineRepo quarantineStorer
	finder         pendingDateFinder
	duplicates     duplicateDetector
	primaryCheck   primaryObservationChecker
	effectiveRep   effectiveSampleReporter
	log            *slog.Logger
	lookback       int // calendar-day lookback for pending signal dates (0 = unlimited)
}

// DefaultLookbackDays is used when no explicit lookback is set.
// 60 calendar days ≈ ~42 trading days, enough to cover the 20-day evaluation
// window plus some buffer.
const DefaultLookbackDays = 60

// NewTradeOutcomeJob constructs a TradeOutcomeJob from the production concrete types.
func NewTradeOutcomeJob(
	svc *outcomes.TradeOutcomeService,
	repo *repository.TradeOutcomeRepo,
	log *slog.Logger,
) *TradeOutcomeJob {
	return &TradeOutcomeJob{
		svc:          svc,
		adjuster:     svc,
		repo:         repo,
		finder:       repo,
		duplicates:   repo,
		primaryCheck: repo,
		effectiveRep: repo,
		log:          log,
		lookback:     DefaultLookbackDays,
	}
}

// NewTradeOutcomeJobFromSources constructs a TradeOutcomeJob from any values
// that satisfy the required interfaces.  Intended for tests.
func NewTradeOutcomeJobFromSources(
	svc outcomeComputer,
	adjuster outcomeResultAdjuster,
	repo outcomeStorer,
	finder pendingDateFinder,
	log *slog.Logger,
) *TradeOutcomeJob {
	return &TradeOutcomeJob{
		svc:      svc,
		adjuster: adjuster,
		repo:     repo,
		finder:   finder,
		log:      log,
		lookback: DefaultLookbackDays,
	}
}

// WithLookback overrides the default lookback window (in calendar days).
func (j *TradeOutcomeJob) WithLookback(days int) *TradeOutcomeJob {
	j.lookback = days
	return j
}

// WithQuarantineRepo sets the quarantine repository for writing quarantined outcomes.
func (j *TradeOutcomeJob) WithQuarantineRepo(qr quarantineStorer) *TradeOutcomeJob {
	j.quarantineRepo = qr
	return j
}

// RunTradeOutcomeJob executes the full compute-and-persist cycle.
//
// Steps:
//  1. Find signal dates that still need evaluation.
//  2. For each date, compute forward performance for all signals.
//  3. Adjust returns for corporate actions and check plausibility.
//  4. Upsert plausible results into trade_outcomes_daily, setting
//     is_primary_observation and cross_list_duplicate flags per R04.
//  5. Write implausible results into trade_outcomes_quarantine.
//  6. Detect and flag duplicate signals.
//  7. Log effective sample size (raw_N vs effective_N).
//  8. Log a structured summary.
func (j *TradeOutcomeJob) RunTradeOutcomeJob(ctx context.Context, today time.Time) error {
	start := time.Now()

	// ── 1. Find pending dates ─────────────────────────────────────────────────
	j.log.Info("job starting",
		"job", tradeOutcomeJobName,
		"today", today.Format("2006-01-02"),
		"lookback_days", j.lookback,
	)

	pendingDates, err := j.finder.GetPendingSignalDates(ctx, today, j.lookback)
	if err != nil {
		j.log.Error("job failed",
			"job", tradeOutcomeJobName,
			"step", "find_pending",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("%s: find pending dates: %w", tradeOutcomeJobName, err)
	}

	if len(pendingDates) == 0 {
		j.log.Info("job completed — no pending signal dates",
			"job", tradeOutcomeJobName,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil
	}

	j.log.Info("pending signal dates found",
		"job", tradeOutcomeJobName,
		"count", len(pendingDates),
	)

	// ── 2-5. Compute, adjust, plausibility-check, persist per date ────────────
	var totalUpserted int
	var totalQuarantined int
	for _, signalDate := range pendingDates {
		tag := signalDate.Format("2006-01-02")

		results, err := j.svc.ComputeOutcomes(ctx, signalDate, today)
		if err != nil {
			j.log.Error("job failed",
				"job", tradeOutcomeJobName,
				"step", "compute",
				"signal_date", tag,
				"error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: compute: %w", tradeOutcomeJobName, tag, err)
		}

		for i := range results {
			j.log.Info("processing ticker", "ticker", results[i].Ticker, "list_type", string(results[i].ListType), "signal_date", tag)
			row := outcomeResultToModel(&results[i])

			// Apply corporate action adjustment to all return horizons
			adjustedRow := adjustRowForCorporateActions(ctx, j.adjuster, &row)

			// ── STORY-R04: Determine is_primary_observation and cross-list dedup ─
			adjustedRow.IsPrimaryObservation = true
			adjustedRow.CrossListDuplicate = false

			// Compute cluster_id from ticker+date as an int64 hash.
			// R05 will replace this with proper cluster assignment for cluster-robust SEs.
			clusterID := fmt.Sprintf("%s_%s", adjustedRow.Ticker, adjustedRow.EntryDate.Format("2006-01-02"))
			h := fnv.New64a()
			_, err := h.Write([]byte(clusterID))
			if err != nil {
				return err
			}
			// Safe conversion: mask to int64 max to avoid overflow.
			cid := int64(h.Sum64() & math.MaxInt64)
			adjustedRow.ClusterID = &cid

			if j.primaryCheck != nil {
				// Pattern 1: temporal duplication — prior observation within 20 trading days
				prior, err := j.primaryCheck.GetPriorPrimaryObservation(
					ctx, adjustedRow.Ticker, string(adjustedRow.ListType), adjustedRow.EntryDate,
				)
				if err != nil {
					j.log.Warn("failed to check prior primary observation",
						"job", tradeOutcomeJobName,
						"ticker", adjustedRow.Ticker,
						"list_type", string(adjustedRow.ListType),
						"error", err,
					)
				} else if prior != nil {
					adjustedRow.IsPrimaryObservation = false
				}

				// Pattern 2: cross-list duplication — same ticker/date, higher-priority list exists
				if adjustedRow.IsPrimaryObservation {
					crossPrior, err := j.primaryCheck.GetCrossListPriorTicker(
						ctx, adjustedRow.Ticker, adjustedRow.EntryDate,
					)
					if err != nil {
						j.log.Warn("failed to check cross-list duplicate",
							"job", tradeOutcomeJobName,
							"ticker", adjustedRow.Ticker,
							"error", err,
						)
					} else if crossPrior != nil && string(crossPrior.ListType) != string(adjustedRow.ListType) {
						adjustedRow.IsPrimaryObservation = false
						adjustedRow.CrossListDuplicate = true
						// Persist the cross-list duplicate flag immediately
						if err := j.primaryCheck.MarkCrossListDuplicate(
							ctx, adjustedRow.EntryDate, string(adjustedRow.ListType), adjustedRow.Ticker,
						); err != nil {
							j.log.Warn("failed to mark cross-list duplicate",
								"job", tradeOutcomeJobName,
								"ticker", adjustedRow.Ticker,
								"list_type", string(adjustedRow.ListType),
								"error", err,
							)
						}
					}
				}
			}

			// Plausibility check
			resultForCheck := modelToOutcomeResult(adjustedRow)
			if reason := j.adjuster.PlausibilityCheck(resultForCheck); reason != "" {
				// Quarantine: write to quarantine table
				if j.quarantineRepo != nil {
					q := buildQuarantineEntry(adjustedRow, reason)
					if err := j.quarantineRepo.UpsertQuarantine(ctx, &q); err != nil {
						j.log.Error("quarantine upsert failed",
							"job", tradeOutcomeJobName,
							"step", "quarantine",
							"signal_date", tag,
							"ticker", results[i].Ticker,
							"reason", reason,
							"error", err,
						)
					}
				}
				j.log.Warn("outcome quarantined",
					"job", tradeOutcomeJobName,
					"ticker", results[i].Ticker,
					"signal_date", tag,
					"list_type", string(results[i].ListType),
					"reason", reason,
					"return_1d", adjustedRow.Return1D,
					"return_20d", adjustedRow.Return20D,
					"corporate_action_count", adjustedRow.CorporateActionCount,
				)
				totalQuarantined++
				continue
			}

			// Plausible: upsert normally
			if err := j.repo.UpsertTradeOutcome(ctx, adjustedRow); err != nil {
				j.log.Error("job failed",
					"job", tradeOutcomeJobName,
					"step", "persist",
					"signal_date", tag,
					"ticker", results[i].Ticker,
					"error", err,
					"duration_ms", time.Since(start).Milliseconds(),
				)
				return fmt.Errorf("%s [%s]: persist %s: %w", tradeOutcomeJobName, tag, results[i].Ticker, err)
			}
			totalUpserted++
		}

		j.log.Info("signal date evaluated",
			"job", tradeOutcomeJobName,
			"signal_date", tag,
			"signals", len(results),
		)
	}

	// ── 6. Detect and flag duplicate signals ──────────────────────────────────
	if j.duplicates != nil {
		const duplicateWindowDays = 3
		pairs, err := j.duplicates.FindDuplicateSignals(ctx, duplicateWindowDays)
		if err != nil {
			j.log.Warn("duplicate signal detection failed",
				"job", tradeOutcomeJobName,
				"error", err,
			)
		} else if len(pairs) > 0 {
			marked := 0
			for _, p := range pairs {
				if err := j.duplicates.MarkDuplicateSignal(ctx, p.NewDate, p.NewListType, p.NewTicker, p.TradingDaysBetween); err != nil {
					j.log.Warn("mark duplicate signal failed",
						"job", tradeOutcomeJobName,
						"ticker", p.NewTicker,
						"date", p.NewDate.Format("2006-01-02"),
						"error", err,
					)
				} else {
					marked++
				}
			}
			j.log.Info("duplicate signals flagged",
				"job", tradeOutcomeJobName,
				"pairs_found", len(pairs),
				"marked", marked,
			)
		}
	}

	// ── 7. Summary ────────────────────────────────────────────────────────────
	j.log.Info("TradeOutcomeJob completed",
		"job", tradeOutcomeJobName,
		"duration_ms", time.Since(start).Milliseconds(),
		"dates_evaluated", len(pendingDates),
		"outcomes_upserted", totalUpserted,
		"outcomes_quarantined", totalQuarantined,
	)

	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// adjustRowForCorporateActions applies AdjustForCorporateActions to all return
// horizons in a TradeOutcomeDaily row, returning an updated copy.
func adjustRowForCorporateActions(ctx context.Context, adjuster outcomeResultAdjuster, row *models.TradeOutcomeDaily) *models.TradeOutcomeDaily {
	adjusted := *row
	var totalActions int

	// All return horizons to adjust
	type horizon struct {
		field **float64
		days  int
	}
	h := []horizon{
		{field: &adjusted.Return1D, days: 1},
		{field: &adjusted.Return2D, days: 2},
		{field: &adjusted.Return3D, days: 3},
		{field: &adjusted.Return4D, days: 4},
		{field: &adjusted.Return5D, days: 5},
		{field: &adjusted.Return10D, days: 10},
		{field: &adjusted.Return20D, days: 20},
		{field: &adjusted.MaxRunup20D, days: 20},
		{field: &adjusted.MaxDrawdown20D, days: 20},
	}

	for _, hv := range h {
		if *hv.field == nil {
			continue
		}
		adjustedRet, actionCount, err := adjuster.AdjustForCorporateActions(ctx, row.Ticker, row.EntryDate, **hv.field, hv.days)
		if err != nil {
			// Log but don't fail; keep raw return
			slog.Warn("corporate action adjustment failed",
				"ticker", row.Ticker,
				"entry_date", row.EntryDate.Format("2006-01-02"),
				"horizon", hv.days,
				"error", err,
			)
			continue
		}
		**hv.field = adjustedRet
		if actionCount > totalActions {
			totalActions = actionCount
		}
	}

	adjusted.CorporateActionCount = totalActions
	return &adjusted
}

// modelToOutcomeResult converts a models.TradeOutcomeDaily back to a
// outcomes.TradeOutcomeResult for plausibility checking.
func modelToOutcomeResult(m *models.TradeOutcomeDaily) *outcomes.TradeOutcomeResult {
	return &outcomes.TradeOutcomeResult{
		EntryDate:      m.EntryDate,
		ListType:       m.ListType,
		Ticker:         m.Ticker,
		Rank:           m.Rank,
		EntryPrice:     m.EntryPrice,
		Return1D:       m.Return1D,
		Return2D:       m.Return2D,
		Return3D:       m.Return3D,
		Return4D:       m.Return4D,
		Return5D:       m.Return5D,
		Return10D:      m.Return10D,
		Return20D:      m.Return20D,
		MaxRunup20D:    m.MaxRunup20D,
		MaxDrawdown20D: m.MaxDrawdown20D,
		EvaluatedDays:  m.EvaluatedDays,
	}
}

// buildQuarantineEntry converts an adjusted TradeOutcomeDaily row into a
// QuarantineTradeOutcome with a "pending" resolution.
func buildQuarantineEntry(m *models.TradeOutcomeDaily, reason string) repository.QuarantineTradeOutcome {
	return repository.QuarantineTradeOutcome{
		EntryDate:            m.EntryDate,
		ListType:             string(m.ListType),
		Ticker:               m.Ticker,
		Rank:                 m.Rank,
		EntryPrice:           m.EntryPrice,
		Return1D:             m.Return1D,
		Return2D:             m.Return2D,
		Return3D:             m.Return3D,
		Return4D:             m.Return4D,
		Return5D:             m.Return5D,
		Return10D:            m.Return10D,
		Return20D:            m.Return20D,
		MaxRunup20D:          m.MaxRunup20D,
		MaxDrawdown20D:       m.MaxDrawdown20D,
		EvaluatedDays:        m.EvaluatedDays,
		QuarantineReason:     reason,
		CorporateActionCount: m.CorporateActionCount,
		Resolution:           "pending",
	}
}

// outcomeResultToModel converts a service result to the DB entity.
func outcomeResultToModel(r *outcomes.TradeOutcomeResult) models.TradeOutcomeDaily {
	return models.TradeOutcomeDaily{
		EntryDate:      r.EntryDate,
		ListType:       r.ListType,
		Ticker:         r.Ticker,
		Rank:           r.Rank,
		EntryPrice:     r.EntryPrice,
		Return1D:       r.Return1D,
		Return2D:       r.Return2D,
		Return3D:       r.Return3D,
		Return4D:       r.Return4D,
		Return5D:       r.Return5D,
		Return10D:      r.Return10D,
		Return20D:      r.Return20D,
		MaxRunup20D:    r.MaxRunup20D,
		MaxDrawdown20D: r.MaxDrawdown20D,
		EvaluatedDays:  r.EvaluatedDays,
	}
}
