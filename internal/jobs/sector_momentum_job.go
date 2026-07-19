package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/sector"
)

// sectorMomentumJobName is emitted in every log line produced by this job.
const sectorMomentumJobName = "SectorMomentumJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// sectorScoreBuilder is the subset of sector.MomentumService that the job
// requires.  Using an interface allows tests to inject a mock.
type sectorScoreBuilder interface {
	BuildSectorScores(ctx context.Context, date time.Time) ([]sector.Score, error)
}

// sectorScoreStorer is the subset of repository.SectorScoresRepo that the job
// requires.
type sectorScoreStorer interface {
	UpsertSectorScore(ctx context.Context, m *models.SectorScoreDaily) error
}

// Compile-time assertions.
var _ sectorScoreBuilder = (*sector.MomentumService)(nil)
var _ sectorScoreStorer = (*repository.SectorScoresRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// SectorMomentumJob computes and persists daily sector momentum scores.
// Designed to be constructed once at startup and called once per nightly
// pipeline run.
type SectorMomentumJob struct {
	svc  sectorScoreBuilder
	repo sectorScoreStorer
	log  *slog.Logger
}

// NewSectorMomentumJob constructs a SectorMomentumJob from the production
// concrete types.
func NewSectorMomentumJob(
	svc *sector.MomentumService,
	repo *repository.SectorScoresRepo,
	log *slog.Logger,
) *SectorMomentumJob {
	return &SectorMomentumJob{svc: svc, repo: repo, log: log}
}

// NewSectorMomentumJobFromSources constructs a SectorMomentumJob from any
// values that satisfy the builder and storer interfaces.  Intended for tests.
func NewSectorMomentumJobFromSources(
	svc sectorScoreBuilder,
	repo sectorScoreStorer,
	log *slog.Logger,
) *SectorMomentumJob {
	return &SectorMomentumJob{svc: svc, repo: repo, log: log}
}

// RunSectorMomentumJob executes the full compute-and-persist cycle for date.
//
// Steps:
//  1. Log job start.
//  2. Call MomentumService.BuildSectorScores to derive all sector metrics.
//  3. Upsert each score into sector_scores_daily (idempotent — safe to rerun).
//  4. Log a structured summary.
func (j *SectorMomentumJob) RunSectorMomentumJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	// ── 1. Start ──────────────────────────────────────────────────────────────
	j.log.Info("job starting",
		"job", sectorMomentumJobName,
		"date", tag,
	)

	// ── 2. Compute ────────────────────────────────────────────────────────────
	scores, err := j.svc.BuildSectorScores(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", sectorMomentumJobName,
			"date", tag,
			"step", "compute",
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("%s [%s]: compute: %w", sectorMomentumJobName, tag, err)
	}

	// ── 3. Persist ────────────────────────────────────────────────────────────
	for i := range scores {
		row := sectorScoreToModel(&scores[i])
		if err := j.repo.UpsertSectorScore(ctx, &row); err != nil {
			j.log.Error("job failed",
				"job", sectorMomentumJobName,
				"date", tag,
				"step", "persist",
				"etf", scores[i].ETF,
				"error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: persist %s: %w", sectorMomentumJobName, tag, scores[i].ETF, err)
		}
	}

	// ── 4. Summary ────────────────────────────────────────────────────────────
	leading, lagging := summariseExtremes(scores)
	j.log.Info("SectorMomentumJob completed",
		"job", sectorMomentumJobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		"sectors_scored", len(scores),
		"leading", leading,
		"lagging", lagging,
	)

	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// sectorScoreToModel converts a service SectorScore to the DB entity.
func sectorScoreToModel(s *sector.Score) models.SectorScoreDaily {
	return models.SectorScoreDaily{
		Date:        s.Date,
		ETF:         s.ETF,
		Perf1M:      s.Perf1M,
		Perf3M:      s.Perf3M,
		RSvsSPY3M:   s.RSvsSPY3M,
		AboveSMA50:  s.AboveSMA50,
		AboveSMA200: s.AboveSMA200,
		TrendScore:  s.TrendScore,
		Score:       s.Score,
		Label:       s.Label,
	}
}

// summariseExtremes returns comma-separated lists of ETFs with LEADING and
// LAGGING labels, used for the summary log line.
func summariseExtremes(scores []sector.Score) (leading, lagging string) {
	for _, s := range scores {
		switch s.Label {
		case models.SectorLabelLeading:
			if leading != "" {
				leading += ","
			}
			leading += s.ETF
		case models.SectorLabelLagging:
			if lagging != "" {
				lagging += ","
			}
			lagging += s.ETF
		}
	}
	if leading == "" {
		leading = "(none)"
	}
	if lagging == "" {
		lagging = "(none)"
	}
	return leading, lagging
}
