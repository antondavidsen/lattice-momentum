package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

type ptoVersionSource interface {
	GetByVersion(ctx context.Context, promptVersion string) ([]models.PromptTickerOutcome, error)
	GetByDateRange(ctx context.Context, from, to time.Time) ([]models.PromptTickerOutcome, error)
	GetVersionSummary(ctx context.Context, promptVersion string) (*repository.VersionPerformanceSummary, error)
}

type promptExperimentStorer interface {
	Upsert(ctx context.Context, m *models.PromptExperimentResult) error
	GetAllResults(ctx context.Context) ([]models.PromptExperimentResult, error)
}

// variantActivator manages prompt variant activation/deactivation.
// TODO: Implement full PromptVariantRepo with DB-backed variant tracking.
// For now, the z-test logs the result but does not auto-promote.
type variantActivator interface {
	DeactivateVariant(ctx context.Context, version string) error
	UpsertVariant(ctx context.Context, version string, active bool) error
}

// Compile-time assertions.
var _ ptoVersionSource = (*repository.PromptTickerOutcomeRepo)(nil)
var _ promptExperimentStorer = (*repository.PromptExperimentRepo)(nil)

// NightlyPromptExperimentJob computes per-prompt-version performance metrics
// for the nightly evaluation pipeline (EP, Momentum, Leaders).
// Also runs A/B significance testing between prompt variants.
type NightlyPromptExperimentJob struct {
	ptoRepo     ptoVersionSource
	promptRepo  promptExperimentStorer
	variantRepo variantActivator // nil = no variant activation (z-test still runs)
	log         *slog.Logger
}

// NewNightlyPromptExperimentJob constructs a new job from production types.
func NewNightlyPromptExperimentJob(
	ptoRepo *repository.PromptTickerOutcomeRepo,
	promptRepo *repository.PromptExperimentRepo,
	log *slog.Logger,
) *NightlyPromptExperimentJob {
	return &NightlyPromptExperimentJob{
		ptoRepo:    ptoRepo,
		promptRepo: promptRepo,
		log:        log,
	}
}

// WithVariantActivator attaches a variantActivator for A/B promotion.
func (j *NightlyPromptExperimentJob) WithVariantActivator(v variantActivator) *NightlyPromptExperimentJob {
	j.variantRepo = v
	return j
}

// RunNightlyPromptExperimentJob computes per-version metrics and persists them.
func (j *NightlyPromptExperimentJob) RunNightlyPromptExperimentJob(ctx context.Context) error {
	start := time.Now()
	j.log.Info("nightly prompt experiment job starting")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -90)

	outcomes, err := j.ptoRepo.GetByDateRange(ctx, from, today)
	if err != nil {
		return fmt.Errorf("load outcomes: %w", err)
	}

	if len(outcomes) == 0 {
		j.log.Info("nightly prompt experiment: no outcome data yet")
		return nil
	}

	// Group by prompt_version + list_type.
	type groupKey struct {
		version  string
		listType models.ListType
	}
	type stats struct {
		returns5D  []float64
		returns10D []float64
		minDate    time.Time
		maxDate    time.Time
		total      int
	}
	groups := make(map[groupKey]*stats)

	for i := range outcomes {
		o := &outcomes[i]
		if !o.LLMRecommended || o.EvaluatedDays < 5 {
			continue
		}
		k := groupKey{version: o.PromptVersion, listType: o.ListType}
		s, ok := groups[k]
		if !ok {
			s = &stats{minDate: o.Date, maxDate: o.Date}
			groups[k] = s
		}
		s.total++
		if o.Date.Before(s.minDate) {
			s.minDate = o.Date
		}
		if o.Date.After(s.maxDate) {
			s.maxDate = o.Date
		}
		if o.ActualReturn5D != nil {
			s.returns5D = append(s.returns5D, *o.ActualReturn5D)
		}
		if o.ActualReturn10D != nil {
			s.returns10D = append(s.returns10D, *o.ActualReturn10D)
		}
	}

	// Persist results.
	for k, s := range groups {
		if s.total == 0 {
			continue
		}

		pipelineType := "nightly_" + string(k.listType)

		var wins5 int
		for _, r := range s.returns5D {
			if r > 0 {
				wins5++
			}
		}
		wr5 := 0.0
		if len(s.returns5D) > 0 {
			wr5 = float64(wins5) / float64(len(s.returns5D))
		}
		avg5 := meanFloat(s.returns5D)
		med5 := medianFloat(s.returns5D)

		result := &models.PromptExperimentResult{
			PromptVersion:        k.version,
			PipelineType:         pipelineType,
			EvaluationDate:       today,
			TotalPicks:           s.total,
			AvgIntradayReturn:    &avg5,
			MedianIntradayReturn: &med5,
			WinRate:              &wr5,
			TradeStartDate:       s.minDate,
			TradeEndDate:         s.maxDate,
		}

		if err := j.promptRepo.Upsert(ctx, result); err != nil {
			j.log.Warn("upsert nightly prompt experiment", "version", k.version, "error", err)
		} else {
			j.log.Info("nightly prompt experiment result",
				"version", k.version,
				"pipeline", pipelineType,
				"picks", s.total,
				"win_rate_5d", fmt.Sprintf("%.1f%%", wr5*100),
				"avg_return_5d", fmt.Sprintf("%.2f%%", avg5*100),
			)
		}
	}

	j.log.Info("nightly prompt experiment job complete",
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// ── A/B Significance Testing ──────────────────────────────────────────────────

// RunPromptABSignificanceTest reads prompt_experiment_results for the past 30
// days, groups by prompt_version, and runs a proportions z-test comparing the
// two most recent active variants (A vs B). If B's win rate > A's at p < 0.05,
// it logs the result and (if variantRepo is wired) promotes B.
//
// The z-test formula:
//
//	z = (p_B - p_A) / sqrt(p_pool * (1 - p_pool) * (1/n_A + 1/n_B))
//
// where p_pool = (wins_A + wins_B) / (n_A + n_B).
func (j *NightlyPromptExperimentJob) RunPromptABSignificanceTest(ctx context.Context) error {
	start := time.Now()
	j.log.Info("prompt A/B significance test starting")

	results, err := j.promptRepo.GetAllResults(ctx)
	if err != nil {
		return fmt.Errorf("RunPromptABSignificanceTest: load results: %w", err)
	}

	if len(results) == 0 {
		j.log.Info("prompt A/B test: no experiment results found")
		return nil
	}

	// Filter to last 30 days.
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	var recent []models.PromptExperimentResult
	for i := range results {
		r := &results[i]
		if !r.EvaluationDate.Before(cutoff) {
			recent = append(recent, *r)
		}
	}

	if len(recent) < 2 {
		j.log.Info("prompt A/B test: insufficient recent data (<2 results)")
		return nil
	}

	// Group by prompt_version, aggregate win rate stats.
	type versionStats struct {
		totalPicks int
		wins       int
	}
	versionMap := make(map[string]*versionStats)
	for i := range recent {
		r := &recent[i]
		vs, ok := versionMap[r.PromptVersion]
		if !ok {
			vs = &versionStats{}
			versionMap[r.PromptVersion] = vs
		}
		vs.totalPicks += r.TotalPicks
		if r.WinRate != nil {
			vs.wins += int(*r.WinRate * float64(r.TotalPicks))
		}
	}

	// Find the two most recent versions (A = control, B = challenger).
	// Sort versions by their most recent evaluation date.
	type versionEntry struct {
		version string
		stats   *versionStats
		latest  time.Time
	}
	var entries []versionEntry
	for i := range recent {
		r := &recent[i]
		vs := versionMap[r.PromptVersion]
		entry := versionEntry{
			version: r.PromptVersion,
			stats:   vs,
			latest:  r.EvaluationDate,
		}
		// Update latest if we already have this version.
		found := false
		for i, e := range entries {
			if e.version == r.PromptVersion {
				if r.EvaluationDate.After(e.latest) {
					entries[i].latest = r.EvaluationDate
				}
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, entry)
		}
	}

	if len(entries) < 2 {
		j.log.Info("prompt A/B test: fewer than 2 distinct versions found")
		return nil
	}

	// Sort by latest evaluation date descending.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].latest.After(entries[i].latest) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// A = most recent (control), B = second most recent (challenger).
	a := entries[0]
	b := entries[1]

	nA := a.stats.totalPicks
	nB := b.stats.totalPicks
	winsA := a.stats.wins
	winsB := b.stats.wins

	if nA == 0 || nB == 0 {
		j.log.Info("prompt A/B test: zero picks for one variant")
		return nil
	}

	pA := float64(winsA) / float64(nA)
	pB := float64(winsB) / float64(nB)
	pPool := float64(winsA+winsB) / float64(nA+nB)

	// z-test for proportions.
	se := math.Sqrt(pPool * (1 - pPool) * (1/float64(nA) + 1/float64(nB)))
	var z float64
	if se > 0 {
		z = (pB - pA) / se
	}

	// Two-tailed p-value approximation using the normal CDF.
	pValue := 2.0 * (1.0 - normalCDF(math.Abs(z)))

	promoted := false
	winner := b.version
	switch {
	case pB > pA && pValue < 0.05:
		j.log.Info("prompt A/B test: challenger wins",
			"winner", b.version,
			"loser", a.version,
			"p_value", fmt.Sprintf("%.4f", pValue),
			"z_score", fmt.Sprintf("%.3f", z),
			"challenger_win_rate", fmt.Sprintf("%.1f%%", pB*100),
			"control_win_rate", fmt.Sprintf("%.1f%%", pA*100),
			"challenger_picks", nB,
			"control_picks", nA,
		)

		if j.variantRepo != nil {
			if err := j.variantRepo.DeactivateVariant(ctx, a.version); err != nil {
				j.log.Warn("prompt A/B test: deactivate control failed", "version", a.version, "error", err)
			}
			if err := j.variantRepo.UpsertVariant(ctx, b.version, true); err != nil {
				j.log.Warn("prompt A/B test: activate challenger failed", "version", b.version, "error", err)
			} else {
				promoted = true
			}
		} else {
			j.log.Info("prompt A/B test: no variantActivator wired — promotion skipped (log only)")
		}
	case pB > pA:
		j.log.Info("prompt A/B test: challenger leads but not significant",
			"challenger", b.version,
			"control", a.version,
			"p_value", fmt.Sprintf("%.4f", pValue),
			"challenger_win_rate", fmt.Sprintf("%.1f%%", pB*100),
			"control_win_rate", fmt.Sprintf("%.1f%%", pA*100),
		)
	default:
		j.log.Info("prompt A/B test: control still ahead",
			"control", a.version,
			"challenger", b.version,
			"control_win_rate", fmt.Sprintf("%.1f%%", pA*100),
			"challenger_win_rate", fmt.Sprintf("%.1f%%", pB*100),
		)
	}

	j.log.Info("prompt A/B significance test complete",
		"duration_ms", time.Since(start).Milliseconds(),
		"winner", winner,
		"p_value", fmt.Sprintf("%.4f", pValue),
		"promoted", promoted,
	)
	return nil
}

// normalCDF approximates the standard normal CDF using the Abramowitz & Stegun
// approximation (error < 7.5e-8).
func normalCDF(x float64) float64 {
	if x < 0 {
		return 1 - normalCDF(-x)
	}
	// Constants for the approximation.
	b0 := 0.2316419
	b1 := 0.319381530
	b2 := -0.356563782
	b3 := 1.781477937
	b4 := -1.821255978
	b5 := 1.330274429

	t := 1.0 / (1.0 + b0*x)
	phi := 0.3989422804014327 * math.Exp(-0.5*x*x) // standard normal PDF
	return 1.0 - phi*(b1*t+b2*t*t+b3*t*t*t+b4*t*t*t*t+b5*t*t*t*t*t)
}

// ── helpers used by nightly_prompt_experiment_job.go (same package) ──────────

func medianFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func meanFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
