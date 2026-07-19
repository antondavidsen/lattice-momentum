package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/scoring"
)

// sectorScoringJobName is emitted in every log line produced by this job.
const sectorScoringJobName = "SectorScoringJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// sectorScoringBuilder is the subset of scoring.ScoringService that the job
// requires.  Using an interface allows tests to inject a mock.
type sectorScoringBuilder interface {
	BuildSectorScores(ctx context.Context, date time.Time) ([]models.SectorScoreDaily, error)
}

// sectorScoringStorer is the subset of repository.SectorScoresRepo that the
// job requires.
type sectorScoringStorer interface {
	UpsertSectorScore(ctx context.Context, m *models.SectorScoreDaily) error
}

// Compile-time assertions.
var _ sectorScoringBuilder = (*scoring.Service)(nil)
var _ sectorScoringStorer = (*repository.SectorScoresRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// SectorScoringJob computes and persists daily sector scores.
// Designed to be constructed once at startup and called once per nightly
// pipeline run.
type SectorScoringJob struct {
	svc  sectorScoringBuilder
	repo sectorScoringStorer
	log  *slog.Logger
}

// NewSectorScoringJob constructs a SectorScoringJob from the production
// concrete types.
func NewSectorScoringJob(
	svc *scoring.Service,
	repo *repository.SectorScoresRepo,
	log *slog.Logger,
) *SectorScoringJob {
	return &SectorScoringJob{svc: svc, repo: repo, log: log}
}

// NewSectorScoringJobFromSources constructs a SectorScoringJob from any
// values that satisfy the builder and storer interfaces.  Intended for tests.
func NewSectorScoringJobFromSources(
	svc sectorScoringBuilder,
	repo sectorScoringStorer,
	log *slog.Logger,
) *SectorScoringJob {
	return &SectorScoringJob{svc: svc, repo: repo, log: log}
}

// RunSectorScoringJob executes the full compute-and-persist cycle for date.
//
// Steps:
//  1. Log job start.
//  2. Call ScoringService.BuildSectorScores to derive all sector metrics.
//  3. Upsert each score into sector_scores_daily (idempotent — safe to rerun).
//  4. Log a structured summary with leader, laggard, and duration.
func (j *SectorScoringJob) RunSectorScoringJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	// ── 1. Start ──────────────────────────────────────────────────────────────
	j.log.Info("job starting",
		"job", sectorScoringJobName,
		"date", tag,
	)

	// ── 2. Compute ────────────────────────────────────────────────────────────
	scores, err := j.svc.BuildSectorScores(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", sectorScoringJobName,
			"date", tag,
			"step", "compute",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("%s [%s]: compute: %w", sectorScoringJobName, tag, err)
	}

	// ── 3. Persist ────────────────────────────────────────────────────────────
	for i := range scores {
		if err := j.repo.UpsertSectorScore(ctx, &scores[i]); err != nil {
			j.log.Error("job failed",
				"job", sectorScoringJobName,
				"date", tag,
				"step", "persist",
				"etf", scores[i].ETF,
				"error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: persist %s: %w", sectorScoringJobName, tag, scores[i].ETF, err)
		}
	}

	// ── 4. Summary ────────────────────────────────────────────────────────────
	leader, laggard := scoringSummary(scores)
	j.log.Info("SectorScoringJob completed",
		"job", sectorScoringJobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		"sectors_scored", len(scores),
		"leader", leader,
		"laggard", laggard,
	)

	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// scoringSummary returns formatted leader and laggard strings for the
// completion log.  Scores are already sorted descending by BuildSectorScores.
func scoringSummary(scores []models.SectorScoreDaily) (leader, laggard string) {
	if len(scores) == 0 {
		return "(none)", "(none)"
	}
	best := scores[0]
	worst := scores[len(scores)-1]
	leader = fmt.Sprintf("%s score=%.2f", best.ETF, best.Score)
	laggard = fmt.Sprintf("%s score=%.2f", worst.ETF, worst.Score)
	return leader, laggard
}
