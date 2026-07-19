// File: internal/jobs/nightly_prompt_experiment_job_test.go
package jobs

import (
	"ai-stock-service/internal/repository"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

// ── Mock Types ─────────────────────────────────────────────────────────────────

type mockPTOVersionSource struct {
	outcomes       []models.PromptTickerOutcome
	getByDateErr   error
	getByVersionFn func(ctx context.Context, promptVersion string) ([]models.PromptTickerOutcome, error)
}

func (m *mockPTOVersionSource) GetByVersion(ctx context.Context, promptVersion string) ([]models.PromptTickerOutcome, error) {
	if m.getByVersionFn != nil {
		return m.getByVersionFn(ctx, promptVersion)
	}
	return m.outcomes, m.getByDateErr
}

func (m *mockPTOVersionSource) GetByDateRange(_ context.Context, _, _ time.Time) ([]models.PromptTickerOutcome, error) {
	return m.outcomes, m.getByDateErr
}

// GetVersionSummary returns nil summary — unused by these tests.
func (m *mockPTOVersionSource) GetVersionSummary(_ context.Context, _ string) (*repository.VersionPerformanceSummary, error) {
	return nil, m.getByDateErr
}

type mockPromptExperimentStorer struct {
	upsertedResults  []*models.PromptExperimentResult
	getAllResultsErr error
	getAllResultsRet []models.PromptExperimentResult
	upsertErr        error
}

func (m *mockPromptExperimentStorer) Upsert(_ context.Context, res *models.PromptExperimentResult) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upsertedResults = append(m.upsertedResults, res)
	return nil
}

func (m *mockPromptExperimentStorer) GetAllResults(_ context.Context) ([]models.PromptExperimentResult, error) {
	return m.getAllResultsRet, m.getAllResultsErr
}

type mockVariantActivator struct {
	deactivatedVersions []string
	upsertedVersions    map[string]bool // version → active
}

func (m *mockVariantActivator) DeactivateVariant(_ context.Context, version string) error {
	m.deactivatedVersions = append(m.deactivatedVersions, version)
	return nil
}

func (m *mockVariantActivator) UpsertVariant(_ context.Context, version string, active bool) error {
	if m.upsertedVersions == nil {
		m.upsertedVersions = make(map[string]bool)
	}
	m.upsertedVersions[version] = active
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func makeOutcome(date time.Time, version string, listType models.ListType, recommended bool, return5D, return10D *float64, days int) models.PromptTickerOutcome {
	return models.PromptTickerOutcome{
		Date:            date,
		PromptVersion:   version,
		ListType:        listType,
		Ticker:          "TEST",
		LLMRecommended:  recommended,
		ActualReturn5D:  return5D,
		ActualReturn10D: return10D,
		EvaluatedDays:   days,
	}
}

func makeFloat(v float64) *float64 { return &v }

// ── Tests: RunNightlyPromptExperimentJob ────────────────────────────────────────

// TestNightlyPromptExperimentJob_RunHappyPath validates the full compute cycle
// with multiple outcomes grouped by version+listType.
func TestNightlyPromptExperimentJob_RunHappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	outcomes := []models.PromptTickerOutcome{
		makeOutcome(now.AddDate(0, 0, -5), "v1", models.ListTypeEP, true, makeFloat(5.0), makeFloat(8.0), 10),
		makeOutcome(now.AddDate(0, 0, -4), "v1", models.ListTypeEP, true, makeFloat(-2.0), makeFloat(1.0), 10),
		makeOutcome(now.AddDate(0, 0, -3), "v1", models.ListTypeEP, true, makeFloat(3.0), makeFloat(4.0), 10),
		makeOutcome(now.AddDate(0, 0, -2), "v2", models.ListTypeMomentum, true, makeFloat(10.0), makeFloat(15.0), 10),
		makeOutcome(now.AddDate(0, 0, -1), "v2", models.ListTypeMomentum, true, makeFloat(-5.0), makeFloat(-2.0), 10),
		// Non-recommended — should be filtered out.
		makeOutcome(now.AddDate(0, 0, -1), "v1", models.ListTypeEP, false, makeFloat(0.0), makeFloat(0.0), 10),
		// Insufficient evaluated days — should be filtered out.
		makeOutcome(now.AddDate(0, 0, -1), "v1", models.ListTypeEP, true, makeFloat(1.0), makeFloat(2.0), 3),
	}

	storer := &mockPromptExperimentStorer{}
	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{outcomes: outcomes},
		promptRepo: storer,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunNightlyPromptExperimentJob(context.Background())
	if err != nil {
		t.Fatalf("RunNightlyPromptExperimentJob() unexpected error: %v", err)
	}

	// Should have upserted 2 results: v1/EP (3 outcomes) and v2/Momentum (2 outcomes).
	if len(storer.upsertedResults) != 2 {
		t.Fatalf("expected 2 upserted results, got %d", len(storer.upsertedResults))
	}

	// Check v1/EP.
	epResult := findResultByPipeline(storer.upsertedResults, "nightly_ep")
	if epResult == nil {
		t.Fatal("expected EP pipeline result")
	}
	if epResult.TotalPicks != 3 {
		t.Errorf("expected 3 picks for v1/EP, got %d", epResult.TotalPicks)
	}
	if epResult.WinRate == nil {
		t.Fatal("expected non-nil WinRate")
	}
	expectedWR := 2.0 / 3.0 // 2 out of 3 positive
	if math.Abs(*epResult.WinRate-expectedWR) > 0.001 {
		t.Errorf("expected WinRate ~%.4f, got %.4f", expectedWR, *epResult.WinRate)
	}

	// Check v2/Momentum.
	momResult := findResultByPipeline(storer.upsertedResults, "nightly_momentum")
	if momResult == nil {
		t.Fatal("expected Momentum pipeline result")
	}
	if momResult.TotalPicks != 2 {
		t.Errorf("expected 2 picks for v2/Momentum, got %d", momResult.TotalPicks)
	}
}

func findResultByPipeline(results []*models.PromptExperimentResult, pipelineType string) *models.PromptExperimentResult {
	for _, r := range results {
		if r.PipelineType == pipelineType {
			return r
		}
	}
	return nil
}

// TestNightlyPromptExperimentJob_NoData validates that empty outcomes produce a
// nil error and no upserts.
func TestNightlyPromptExperimentJob_NoData(t *testing.T) {
	t.Parallel()

	storer := &mockPromptExperimentStorer{}
	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{},
		promptRepo: storer,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunNightlyPromptExperimentJob(context.Background())
	if err != nil {
		t.Fatalf("expected nil for empty outcomes, got: %v", err)
	}
	if len(storer.upsertedResults) != 0 {
		t.Errorf("expected 0 upserts, got %d", len(storer.upsertedResults))
	}
}

// TestNightlyPromptExperimentJob_LoadError validates that load errors propagate.
func TestNightlyPromptExperimentJob_LoadError(t *testing.T) {
	t.Parallel()

	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{getByDateErr: errors.New("db connection lost")},
		promptRepo: &mockPromptExperimentStorer{},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunNightlyPromptExperimentJob(context.Background())
	if err == nil {
		t.Fatal("expected error from GetByDateRange, got nil")
	}
}

// TestNightlyPromptExperimentJob_UpsertError validates that upsert warns but
// does not abort the job.
func TestNightlyPromptExperimentJob_UpsertError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	outcomes := []models.PromptTickerOutcome{
		makeOutcome(now.AddDate(0, 0, -1), "v1", models.ListTypeEP, true, makeFloat(5.0), makeFloat(8.0), 10),
	}

	job := &NightlyPromptExperimentJob{
		ptoRepo: &mockPTOVersionSource{outcomes: outcomes},
		promptRepo: &mockPromptExperimentStorer{
			upsertErr: errors.New("upsert failed"),
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunNightlyPromptExperimentJob(context.Background())
	if err != nil {
		t.Fatalf("expected nil (upsert warns, not fatal), got: %v", err)
	}
}

// ── Tests: RunPromptABSignificanceTest ─────────────────────────────────────────

func makeExperimentResult(version, pipelineType string, date time.Time, totalPicks int, winRate float64) models.PromptExperimentResult {
	return models.PromptExperimentResult{
		PromptVersion:  version,
		PipelineType:   pipelineType,
		EvaluationDate: date,
		TotalPicks:     totalPicks,
		WinRate:        &winRate,
	}
}

// TestPromptABSignificanceTest_Significant validates the z-test logic when
// challenger (v2) significantly outperforms control (v1).
// v1 is control (more recent), v2 is challenger (less recent).
func TestPromptABSignificanceTest_Significant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	results := []models.PromptExperimentResult{
		makeExperimentResult("v1", "nightly_ep", now, 100, 0.45), // v1 = control (most recent)
		makeExperimentResult("v2", "nightly_ep", now.AddDate(0, 0, -1), 100, 0.62),
	}

	activator := &mockVariantActivator{}
	job := &NightlyPromptExperimentJob{
		ptoRepo:     &mockPTOVersionSource{},
		promptRepo:  &mockPromptExperimentStorer{getAllResultsRet: results},
		variantRepo: activator,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err != nil {
		t.Fatalf("RunPromptABSignificanceTest() unexpected error: %v", err)
	}

	// Check that challenger was promoted.
	if len(activator.deactivatedVersions) != 1 || activator.deactivatedVersions[0] != "v1" {
		t.Errorf("expected v1 deactivated, got %v", activator.deactivatedVersions)
	}
	if !activator.upsertedVersions["v2"] {
		t.Error("expected v2 upserted as active")
	}
}

// TestPromptABSignificanceTest_NotSignificant validates that when challenger's
// win rate is higher but p >= 0.05, no promotion occurs.
func TestPromptABSignificanceTest_NotSignificant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	// Small sample sizes: v2 leads but insufficient for significance.
	results := []models.PromptExperimentResult{
		makeExperimentResult("v1", "nightly_ep", now.AddDate(0, 0, -1), 10, 0.40),
		makeExperimentResult("v2", "nightly_ep", now, 10, 0.60),
	}

	activator := &mockVariantActivator{}
	job := &NightlyPromptExperimentJob{
		ptoRepo:     &mockPTOVersionSource{},
		promptRepo:  &mockPromptExperimentStorer{getAllResultsRet: results},
		variantRepo: activator,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err != nil {
		t.Fatalf("RunPromptABSignificanceTest() unexpected error: %v", err)
	}

	if len(activator.deactivatedVersions) != 0 {
		t.Errorf("expected no deactivations, got %v", activator.deactivatedVersions)
	}
}

// TestPromptABSignificanceTest_NoResults validates that empty results produce
// a nil error.
func TestPromptABSignificanceTest_NoResults(t *testing.T) {
	t.Parallel()

	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{},
		promptRepo: &mockPromptExperimentStorer{},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err != nil {
		t.Fatalf("expected nil for no results, got: %v", err)
	}
}

// TestPromptABSignificanceTest_LoadError validates that load errors propagate.
func TestPromptABSignificanceTest_LoadError(t *testing.T) {
	t.Parallel()

	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{},
		promptRepo: &mockPromptExperimentStorer{getAllResultsErr: errors.New("load failed")},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestPromptABSignificanceTest_SingleVersion validates that <2 versions is handled.
func TestPromptABSignificanceTest_SingleVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	results := []models.PromptExperimentResult{
		makeExperimentResult("v1", "nightly_ep", now, 100, 0.50),
	}

	job := &NightlyPromptExperimentJob{
		ptoRepo:    &mockPTOVersionSource{},
		promptRepo: &mockPromptExperimentStorer{getAllResultsRet: results},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err != nil {
		t.Fatalf("expected nil for single version, got: %v", err)
	}
}

// TestPromptABSignificanceTest_ControlAhead validates that when control has
// higher win rate than challenger, no promotion occurs.
func TestPromptABSignificanceTest_ControlAhead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	results := []models.PromptExperimentResult{
		makeExperimentResult("v1", "nightly_ep", now, 100, 0.55),
		makeExperimentResult("v2", "nightly_ep", now.AddDate(0, 0, -1), 100, 0.45),
	}

	// Note: v1 is the most recent (control), v2 is older (challenger).
	// Since v1 (control) has higher win rate, the test should log "control still ahead".

	activator := &mockVariantActivator{}
	job := &NightlyPromptExperimentJob{
		ptoRepo:     &mockPTOVersionSource{},
		promptRepo:  &mockPromptExperimentStorer{getAllResultsRet: results},
		variantRepo: activator,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := job.RunPromptABSignificanceTest(context.Background())
	if err != nil {
		t.Fatalf("RunPromptABSignificanceTest() unexpected error: %v", err)
	}

	if len(activator.deactivatedVersions) != 0 {
		t.Errorf("expected no deactivations when control ahead, got %v", activator.deactivatedVersions)
	}
}

// TestNormalCDF validates the normalCDF approximation.
func TestNormalCDF(t *testing.T) {
	t.Parallel()

	// Known values for standard normal CDF (approx):
	// Φ(0) = 0.5
	// Φ(1) ≈ 0.8413
	// Φ(2) ≈ 0.9772
	// Φ(3) ≈ 0.9987
	tests := []struct {
		x    float64
		want float64
	}{
		{0.0, 0.5},
		{1.0, 0.841344746},
		{2.0, 0.977249868},
		{3.0, 0.998650102},
		{-1.0, 0.158655254}, // symmetric
		{-2.0, 0.022750132},
	}

	for _, tc := range tests {
		got := normalCDF(tc.x)
		if math.Abs(got-tc.want) > 0.001 {
			t.Errorf("normalCDF(%f) = %f, want ~%f", tc.x, got, tc.want)
		}
	}
}

// TestNormalCDF_Monotonic validates that the CDF is monotonically increasing.
func TestNormalCDF_Monotonic(t *testing.T) {
	t.Parallel()

	prev := 0.0
	for x := -4.0; x <= 4.0; x += 0.1 {
		cdf := normalCDF(x)
		if cdf < prev {
			t.Errorf("normalCDF not monotonic at x=%.1f: %f < %f", x, cdf, prev)
		}
		prev = cdf
	}
}

// TestNormalCDF_Range validates that CDF values lie in [0,1].
func TestNormalCDF_Range(t *testing.T) {
	t.Parallel()

	for x := -10.0; x <= 10.0; x += 0.5 {
		cdf := normalCDF(x)
		if cdf < 0 || cdf > 1 {
			t.Errorf("normalCDF(%f) = %f, out of [0,1]", x, cdf)
		}
	}
}

// TestNewNightlyPromptExperimentJob validates the constructor.
func TestNewNightlyPromptExperimentJob(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	job := NewNightlyPromptExperimentJob(nil, nil, logger)
	if job == nil {
		t.Fatal("NewNightlyPromptExperimentJob returned nil")
	}
	if job.log != logger {
		t.Error("logger not set")
	}

	// With variant activator.
	activator := &mockVariantActivator{}
	job2 := NewNightlyPromptExperimentJob(nil, nil, logger).WithVariantActivator(activator)
	if job2.variantRepo != activator {
		t.Error("variantRepo not set via WithVariantActivator")
	}
}
