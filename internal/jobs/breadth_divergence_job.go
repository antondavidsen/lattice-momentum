// Package jobs implements the BreadthDivergenceJob, which computes and persists
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/regime"
)

const breadthDivergenceJobName = "BreadthDivergenceJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// breadthDivergenceComputer is the subset of regime.BreadthDivergenceService
// that the job requires.
type breadthDivergenceComputer interface {
	Compute(ctx context.Context, date time.Time) (float64, error)
}

// breadthDivergenceUpdater is the subset of repository.MarketRegimeRepo that
// the job requires for persisting the signal.
type breadthDivergenceUpdater interface {
	UpdateBreadthDivergence(ctx context.Context, date time.Time, signal float64) error
}

// Compile-time assertions.
var _ breadthDivergenceComputer = (*regime.BreadthDivergenceService)(nil)
var _ breadthDivergenceUpdater = (*repository.MarketRegimeRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// BreadthDivergenceJob computes and persists the market breadth divergence
// signal. It runs after market regime classification (Step 6).
type BreadthDivergenceJob struct {
	svc  breadthDivergenceComputer
	repo breadthDivergenceUpdater
	log  *slog.Logger
}

// NewBreadthDivergenceJob constructs a BreadthDivergenceJob from production
// concrete types.
func NewBreadthDivergenceJob(
	svc *regime.BreadthDivergenceService,
	repo *repository.MarketRegimeRepo,
	log *slog.Logger,
) *BreadthDivergenceJob {
	return &BreadthDivergenceJob{svc: svc, repo: repo, log: log}
}

// NewBreadthDivergenceJobFromSources constructs a BreadthDivergenceJob from any
// values that satisfy the computer and updater interfaces. Intended for tests.
func NewBreadthDivergenceJobFromSources(
	svc breadthDivergenceComputer,
	repo breadthDivergenceUpdater,
	log *slog.Logger,
) *BreadthDivergenceJob {
	return &BreadthDivergenceJob{svc: svc, repo: repo, log: log}
}

// RunBreadthDivergenceJob executes the compute-and-persist cycle for date.
//
// Steps:
//  1. Log job start.
//  2. Call BreadthDivergenceService.Compute to derive the signal.
//  3. Persist the signal into market_regime_daily.breadth_divergence_signal.
//  4. Log a structured summary.
func (j *BreadthDivergenceJob) RunBreadthDivergenceJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	j.log.Info("job starting",
		"job", breadthDivergenceJobName,
		"date", tag,
	)

	// ── 2. Compute ────────────────────────────────────────────────────────────
	signal, err := j.svc.Compute(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", breadthDivergenceJobName,
			"date", tag,
			"step", "compute",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("%s [%s]: compute: %w", breadthDivergenceJobName, tag, err)
	}

	// ── 3. Persist ────────────────────────────────────────────────────────────
	if err := j.repo.UpdateBreadthDivergence(ctx, date, signal); err != nil {
		if !errors.Is(err, repository.ErrNoMarketRegimeRow) {
			j.log.Error("job failed",
				"job", breadthDivergenceJobName,
				"date", tag,
				"step", "persist",
				"error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: persist: %w", breadthDivergenceJobName, tag, err)
		}
		j.log.Warn("breadth divergence: regime row not yet available — skipping persist",
			"job", breadthDivergenceJobName,
			"date", tag,
			"signal", signal,
		)
	}

	// ── 4. Summary ────────────────────────────────────────────────────────────
	j.log.Info("BreadthDivergenceJob completed",
		"job", breadthDivergenceJobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		"breadth_divergence_signal", signal,
	)

	return nil
}
