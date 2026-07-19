package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/ranking"
)

// dailyRankListsJobName is emitted in every log line produced by this job.
const dailyRankListsJobName = "DailyRankListsJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// rankingEngine is a local alias for the ranking.Engine interface so
// that the job does not expose the concrete engine types.
type rankingEngine interface {
	Compute(ctx context.Context, date time.Time) ([]ranking.RankedTicker, error)
}

// rankListStorer is the subset of repository.RankListRepo that the job requires.
type rankListStorer interface {
	UpsertRankList(ctx context.Context, m *models.DailyRankList) error
}

// leadersLoader loads the set of sector leaders for a given date.
// Used by R-11 to apply a 5% score boost to leader tickers.
type leadersLoader interface {
	GetLeadersForDate(ctx context.Context, date time.Time) (map[string]bool, error)
}

// Compile-time assertions.
var _ rankListStorer = (*repository.RankListRepo)(nil)
var _ leadersLoader = (*repository.SectorLeadershipRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// DailyRankListsJob runs all three ranking engines and persists the results
// into the daily_rank_lists table.  It is designed to be constructed once at
// startup and called once per nightly pipeline run.
type DailyRankListsJob struct {
	ep            rankingEngine
	momentum      rankingEngine
	leaders       rankingEngine
	repo          rankListStorer
	leadersLoader leadersLoader // R-11: sector leadership boost
	log           *slog.Logger
}

// NewDailyRankListsJob constructs a DailyRankListsJob from the production
// concrete types.
func NewDailyRankListsJob(
	momentum *ranking.MomentumEngine,
	repo *repository.RankListRepo,
	log *slog.Logger,
) *DailyRankListsJob {
	return &DailyRankListsJob{
		momentum: momentum,
		repo:     repo,
		log:      log,
	}
}

// NewDailyRankListsJobFromSources constructs a DailyRankListsJob from any
// values that satisfy the engine and storer interfaces.  Intended for tests.
func NewDailyRankListsJobFromSources(
	ep rankingEngine,
	momentum rankingEngine,
	leaders rankingEngine,
	repo rankListStorer,
	log *slog.Logger,
) *DailyRankListsJob {
	return &DailyRankListsJob{
		ep:       ep,
		momentum: momentum,
		leaders:  leaders,
		repo:     repo,
		log:      log,
	}
}

// WithLeadershipBoost attaches a leadersLoader for the R-11 sector leadership
// boost and returns the job for fluent chaining.
func (j *DailyRankListsJob) WithLeadershipBoost(src leadersLoader) *DailyRankListsJob {
	j.leadersLoader = src
	return j
}

// RunDailyRankListsJob executes the full compute-and-persist cycle for all
// three ranking lists.
//
// Steps:
//  1. Log job start.
//  2. Run EP engine → persist results.
//  3. Run Momentum engine → persist results.
//  4. Run Leaders engine → persist results.
//  5. Log structured summary with duration.
func (j *DailyRankListsJob) RunDailyRankListsJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	// ── 1. Start ──────────────────────────────────────────────────────────────
	j.log.Info("job starting",
		"job", dailyRankListsJobName,
		"date", tag,
	)

	// ── 3. Momentum List ──────────────────────────────────────────────────────
	momResults, err := j.momentum.Compute(ctx, date)
	if err != nil {
		j.log.Error("job failed",
			"job", dailyRankListsJobName, "date", tag,
			"step", "momentum_compute", "error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return fmt.Errorf("%s [%s]: momentum_compute: %w", dailyRankListsJobName, tag, err)
	}

	// ── 4.6 R-11: Sector leadership boost ─────────────────────────────────────
	// Apply a 5% score boost to tickers flagged as sector leaders.
	if j.leadersLoader != nil {
		leaders, err := j.leadersLoader.GetLeadersForDate(ctx, date)
		if err != nil {
			j.log.Error("job failed",
				"job", dailyRankListsJobName, "date", tag,
				"step", "load_leaders", "error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: load_leaders: %w", dailyRankListsJobName, tag, err)
		}
		applyLeadershipBoost(momResults, leaders)
		j.log.Debug("leadership boost applied",
			"job", dailyRankListsJobName, "date", tag,
			"boosted_count", len(leaders),
		)
	}

	if err := j.persistList(ctx, date, models.ListTypeMomentum, momResults, tag, start); err != nil {
		return err
	}

	// ── 5. Summary ────────────────────────────────────────────────────────────
	j.log.Info("DailyRankListsJob completed",
		"job", dailyRankListsJobName,
		"date", tag,
		"duration_ms", time.Since(start).Milliseconds(),
		"momentum_count", len(momResults),
		"momentum_top", topTickerSummary(momResults),
	)

	return nil
}

// persistList upserts a ranked ticker list into daily_rank_lists.
func (j *DailyRankListsJob) persistList(
	ctx context.Context,
	date time.Time,
	listType models.ListType,
	results []ranking.RankedTicker,
	tag string,
	start time.Time,
) error {
	for i, r := range results {
		row := &models.DailyRankList{
			Date:     date,
			ListType: listType,
			Rank:     i + 1,
			Ticker:   r.Ticker,
			Score:    r.Score,
			Reason:   ranking.MustJSON(r.Reason),
		}
		if err := j.repo.UpsertRankList(ctx, row); err != nil {
			j.log.Error("job failed",
				"job", dailyRankListsJobName, "date", tag,
				"step", fmt.Sprintf("persist_%s", listType),
				"ticker", r.Ticker, "error", err,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return fmt.Errorf("%s [%s]: persist %s %s: %w",
				dailyRankListsJobName, tag, listType, r.Ticker, err)
		}
	}
	return nil
}

// topTickerSummary returns a formatted string of the #1 ticker and score for
// the completion log.
func topTickerSummary(results []ranking.RankedTicker) string {
	if len(results) == 0 {
		return "(empty)"
	}
	return fmt.Sprintf("%s score=%.1f", results[0].Ticker, results[0].Score)
}

// applyLeadershipBoost applies a 5% score boost to tickers flagged as sector
// leaders (R-11).  The boost is applied in-place to the target slice.
func applyLeadershipBoost(target []ranking.RankedTicker, leadersSet map[string]bool) {
	const leadershipBoost = 1.05
	for i := range target {
		if leadersSet[target[i].Ticker] {
			target[i].Score *= leadershipBoost
			target[i].Reason["leadership_boost"] = leadershipBoost
		}
	}
}
