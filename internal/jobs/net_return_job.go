package jobs

import (
	"context"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/outcomes"
)

// ── Interfaces ─────────────────────────────────────────────────────────────────

// outcomeReaderWriter is the subset of TradeOutcomeRepo needed by NetReturnJob.
type outcomeReaderWriter interface {
	GetByRegimeBucket(ctx context.Context, regimeBucket string, listType string, minEvaluatedDays int, limit int) ([]models.TradeOutcomeDaily, error)
	UpdateNetReturns(ctx context.Context, entryDate time.Time, listType string, ticker string, netReturn5D, netReturn10D, netReturn20D *float64, slippageTier *string, advCapApplied bool, advCapPct *float64, regimeLabel *string, exitType *string, stopSlippageBps *float64) error
	BackfillRegimeLabels(ctx context.Context) (int, error)
}

// tickerSnapshotSource returns price/ADV for a ticker on a specific date.
type tickerSnapshotSource interface {
	GetSnapshot(ctx context.Context, ticker string, date time.Time) (*models.TickerSnapshot, error)
}

// Compile-time assertions
var _ outcomeReaderWriter = (*repository.TradeOutcomeRepo)(nil)

// ── Job ─────────────────────────────────────────────────────────────────────────

// NetReturnJob computes net returns for trade outcomes missing them.
// Run after TradeOutcomeJob (which populates gross returns).
type NetReturnJob struct {
	repo           outcomeReaderWriter
	snapshotSource tickerSnapshotSource
	log            *slog.Logger
	// Default portfolio value for ADV cap computation.
	// If zero, ADV cap is not applied.
	PortfolioValue float64
}

// NewNetReturnJob constructs from production concretes.
func NewNetReturnJob(repo *repository.TradeOutcomeRepo, snapshotSource tickerSnapshotSource, log *slog.Logger) *NetReturnJob {
	return &NetReturnJob{
		repo:           repo,
		snapshotSource: snapshotSource,
		log:            log,
		PortfolioValue: 100_000, // default $100k portfolio
	}
}

// NewNetReturnJobFromSources constructs from interfaces (tests).
func NewNetReturnJobFromSources(repo outcomeReaderWriter, snapshotSource tickerSnapshotSource, log *slog.Logger) *NetReturnJob {
	return &NetReturnJob{
		repo:           repo,
		snapshotSource: snapshotSource,
		log:            log,
		PortfolioValue: 100_000,
	}
}

// RunNetReturnJob processes all trade_outcomes_daily rows that:
//   - have gross returns populated (return_5d IS NOT NULL)
//   - have net returns NULL OR slippage_tier IS NULL
//   - have evaluated_days >= 5 (minimum for net_5d to be meaningful)
//
// Steps per row:
//  1. Fetch ticker snapshot (price, ADV) for (ticker, entry_date).
//  2. If snapshot unavailable: log warning, skip row, continue.
//  3. Apply slippage model via outcomes.ApplySlippageModel (computes net returns,
//     slippage tier, ADV cap, and stop slippage).
//  4. If row has regime_label but no regime_bucket: compute and set.
//  5. Persist via repo.UpdateNetReturns.
func (j *NetReturnJob) RunNetReturnJob(ctx context.Context, date time.Time) error {
	start := time.Now()

	j.log.Info("job starting",
		"job", "NetReturnJob",
		"date", date.Format("2006-01-02"),
	)

	// Fetch all rows that need net return computation.
	// We iterate through regime buckets and list types to cover all outcomes.
	buckets := []string{"risk_on", "risk_off", "unknown", "all"}
	listTypes := []string{"ep", "momentum", "leaders", ""}

	var processed, skipped, errors int

	for _, bucket := range buckets {
		for _, lt := range listTypes {
			rows, err := j.repo.GetByRegimeBucket(ctx, bucket, lt, 5, 0)
			if err != nil {
				j.log.Warn("net return: fetch by regime bucket failed",
					"bucket", bucket,
					"list_type", lt,
					"error", err,
				)
				continue
			}

			for i := range rows {
				row := &rows[i]
				// Skip if already has net returns and slippage tier
				if row.NetReturn5D != nil && row.SlippageTier != nil {
					continue
				}

				// Skip if no gross return to work with
				if row.Return5D == nil {
					continue
				}

				processed++

				// Step 1: Fetch snapshot
				snap, err := j.snapshotSource.GetSnapshot(ctx, row.Ticker, row.EntryDate)
				if err != nil {
					j.log.Warn("net return: snapshot unavailable, skipping",
						"ticker", row.Ticker,
						"entry_date", row.EntryDate.Format("2006-01-02"),
						"error", err,
					)
					skipped++
					continue
				}
				if snap == nil {
					j.log.Warn("net return: nil snapshot, skipping",
						"ticker", row.Ticker,
						"entry_date", row.EntryDate.Format("2006-01-02"),
					)
					skipped++
					continue
				}

				// Step 2-3: Apply the full slippage model
				exitType := row.ExitType // may be nil
				if err := outcomes.ApplySlippageModel(
					row,
					snap.Price,
					snap.ADV,
					exitType,
					j.PortfolioValue*0.05, // kellySize: 5% of portfolio as default Kelly position
					j.PortfolioValue,
				); err != nil {
					j.log.Error("net return: apply slippage model failed",
						"ticker", row.Ticker,
						"entry_date", row.EntryDate.Format("2006-01-02"),
						"error", err,
					)
					errors++
					continue
				}

				// Step 4: Persist
				// regime_bucket is a GENERATED ALWAYS column derived from regime_label,
				// so it is NOT passed to UpdateNetReturns.
				tierStr := ""
				if row.SlippageTier != nil {
					tierStr = *row.SlippageTier
				}
				advCapPct := row.ADVCapPct

				if err := j.repo.UpdateNetReturns(
					ctx,
					row.EntryDate,
					string(row.ListType),
					row.Ticker,
					row.NetReturn5D,
					row.NetReturn10D,
					row.NetReturn20D,
					&tierStr,
					row.ADVCapApplied,
					advCapPct,
					row.RegimeLabel,
					row.ExitType,
					row.StopSlippageBps,
				); err != nil {
					j.log.Error("net return: update failed",
						"ticker", row.Ticker,
						"entry_date", row.EntryDate.Format("2006-01-02"),
						"error", err,
					)
					errors++
					continue
				}
			}
		}
	}

	j.log.Info("job complete",
		"job", "NetReturnJob",
		"date", date.Format("2006-01-02"),
		"duration_ms", time.Since(start).Milliseconds(),
		"processed", processed,
		"skipped", skipped,
		"errors", errors,
	)

	return nil
}

// ensure interface satisfaction
var _ outcomeReaderWriter = (*repository.TradeOutcomeRepo)(nil)
