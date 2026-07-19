package jobs_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/jobs"
	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// ── mocks ─────────────────────────────────────────────────────────────────────

type mockMemoryStorer struct {
	upserted []*models.PromptMemory
	pending  []models.PromptMemory
	updated  []updatedMemory
	err      error
}

type updatedMemory struct {
	id        uuid.UUID
	status    string
	return5d  *float64
	stopHit   *bool
	targetHit *bool
	summary   *string
}

func (m *mockMemoryStorer) UpsertMemory(_ context.Context, mem *models.PromptMemory) error {
	if m.err != nil {
		return m.err
	}
	m.upserted = append(m.upserted, mem)
	return nil
}

func (m *mockMemoryStorer) GetPendingOutcomes(_ context.Context) ([]models.PromptMemory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.pending, nil
}

func (m *mockMemoryStorer) UpdateOutcome(_ context.Context, id uuid.UUID, status string, return5d *float64, stopHit, targetHit *bool, summary *string) error {
	if m.err != nil {
		return m.err
	}
	m.updated = append(m.updated, updatedMemory{
		id: id, status: status, return5d: return5d,
		stopHit: stopHit, targetHit: targetHit, summary: summary,
	})
	return nil
}

type mockEvalSource struct {
	evals []models.LLMListEvaluation
	err   error
}

func (m *mockEvalSource) GetEvaluationsByDate(_ context.Context, _ time.Time) ([]models.LLMListEvaluation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.evals, nil
}

type mockPTOVersionSource struct {
	outcomes []models.PromptTickerOutcome
	err      error
}

func (m *mockPTOVersionSource) GetByDateRange(_ context.Context, _, _ time.Time) ([]models.PromptTickerOutcome, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.outcomes, nil
}

func (m *mockPTOVersionSource) GetByVersion(_ context.Context, _ string) ([]models.PromptTickerOutcome, error) {
	return nil, nil //nolint:nilnil // mock method always returns empty response
}

func (m *mockPTOVersionSource) GetVersionSummary(_ context.Context, _ string) (*repository.VersionPerformanceSummary, error) {
	return nil, nil //nolint:nilnil // mock method always returns empty response
}

type mockEmbedder struct {
	vec  pgvector.Vector
	err  error
	call int
}

// Embed returns a fixed vector or an error, and counts the number of calls.
func (m *mockEmbedder) Embed(_ context.Context, _ string) (pgvector.Vector, error) {
	m.call++
	if m.err != nil {
		return pgvector.Vector{}, m.err
	}
	return m.vec, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func pmQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func floatPtr(v float64) *float64 {
	p := new(float64)
	*p = v
	return p
}

func fixedDate() time.Time {
	return time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)
}

// ── StoreMemories tests ───────────────────────────────────────────────────────

func TestPromptMemoryJob_StoreMemories_HappyPath(t *testing.T) {
	date := fixedDate()
	embedVec := pgvector.NewVector([]float32{0.1, 0.2, 0.3})

	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON: []byte(`{
				"tickers": [
					{"ticker": "AAPL", "rank": 1, "setup": "breakout", "conviction": "high"},
					{"ticker": "MSFT", "rank": 2, "setup": "pullback", "conviction": "medium"}
				]
			}`),
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{vec: embedVec}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 2 {
		t.Fatalf("expected 2 upserted memories, got %d", got)
	}

	m1 := memStorer.upserted[0]
	if m1.Ticker != "AAPL" {
		t.Errorf("ticker = %q, want AAPL", m1.Ticker)
	}
	if m1.ListType != models.ListTypeEP {
		t.Errorf("list_type = %q, want ep", m1.ListType)
	}
	if m1.PromptVersion != "v1" {
		t.Errorf("prompt_version = %q, want v1", m1.PromptVersion)
	}
	if m1.OutcomeStatus != "pending" {
		t.Errorf("outcome_status = %q, want pending", m1.OutcomeStatus)
	}
	if m1.LLMSetup == nil || *m1.LLMSetup != "breakout" {
		t.Errorf("llm_setup = %v, want breakout", m1.LLMSetup)
	}
	if m1.LLMConviction == nil || *m1.LLMConviction != "high" {
		t.Errorf("llm_conviction = %v, want high", m1.LLMConviction)
	}
	if m1.ContextSummary != "AAPL|ep|setup_breakout" {
		t.Errorf("context_summary = %q, want AAPL|ep|setup_breakout", m1.ContextSummary)
	}

	m2 := memStorer.upserted[1]
	if m2.Ticker != "MSFT" {
		t.Errorf("ticker = %q, want MSFT", m2.Ticker)
	}
	if m2.LLMSetup == nil || *m2.LLMSetup != "pullback" {
		t.Errorf("llm_setup = %v, want pullback", m2.LLMSetup)
	}
	if m2.LLMConviction == nil || *m2.LLMConviction != "medium" {
		t.Errorf("llm_conviction = %v, want medium", m2.LLMConviction)
	}
	if m2.ContextSummary != "MSFT|ep|setup_pullback" {
		t.Errorf("context_summary = %q, want MSFT|ep|setup_pullback", m2.ContextSummary)
	}

	if embedder.call != 2 {
		t.Errorf("embedder called %d times, want 2", embedder.call)
	}
}

func TestPromptMemoryJob_StoreMemories_AllOptionalFields(t *testing.T) {
	date := fixedDate()
	embedVec := pgvector.NewVector([]float32{0.1, 0.2, 0.3})

	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeMomentum,
			PromptVersion: "v2",
			ParsedJSON: []byte(`{
				"tickers": [{
					"ticker": "NVDA",
					"rank": 1,
					"setup": "flag",
					"conviction": "strong",
					"entry_low": 100.0,
					"entry_high": 105.0,
					"stop_price": 95.0
				}]
			}`),
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{vec: embedVec}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 1 {
		t.Fatalf("expected 1 upserted memory, got %d", got)
	}

	m := memStorer.upserted[0]
	if m.LLMSetup == nil || *m.LLMSetup != "flag" {
		t.Errorf("llm_setup = %v, want flag", m.LLMSetup)
	}
	if m.LLMConviction == nil || *m.LLMConviction != "strong" {
		t.Errorf("llm_conviction = %v, want strong", m.LLMConviction)
	}
	if m.LLMEntry == nil || *m.LLMEntry != "$100.00-$105.00" {
		t.Errorf("llm_entry = %v, want $100.00-$105.00", m.LLMEntry)
	}
	if m.LLMStop == nil || *m.LLMStop != "$95.00" {
		t.Errorf("llm_stop = %v, want $95.00", m.LLMStop)
	}
}

func TestPromptMemoryJob_StoreMemories_EmptyParsedJSON(t *testing.T) {
	date := fixedDate()
	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON:    []byte("{}"),
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories, got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_EmptyParsedJSONBytes(t *testing.T) {
	date := fixedDate()
	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON:    []byte{},
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories, got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_InvalidJSON(t *testing.T) {
	date := fixedDate()
	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON:    []byte("{invalid}"),
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories, got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_EmbeddingError(t *testing.T) {
	date := fixedDate()
	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON: []byte(`{
				"tickers": [
					{"ticker": "AAPL", "rank": 1, "setup": "breakout"},
					{"ticker": "MSFT", "rank": 2, "setup": "pullback"}
				]
			}`),
		},
	}

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{err: fmt.Errorf("api down")}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories (embedding failed), got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_UpsertError(t *testing.T) {
	date := fixedDate()
	embedVec := pgvector.NewVector([]float32{0.1, 0.2, 0.3})

	evals := []models.LLMListEvaluation{
		{
			Date:          date,
			ListType:      models.ListTypeEP,
			PromptVersion: "v1",
			ParsedJSON: []byte(`{
				"tickers": [
					{"ticker": "AAPL", "rank": 1, "setup": "breakout"}
				]
			}`),
		},
	}

	memStorer := &mockMemoryStorer{err: fmt.Errorf("db write failed")}
	evalSrc := &mockEvalSource{evals: evals}
	embedder := &mockEmbedder{vec: embedVec}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories (upsert failed), got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_NoEvaluations(t *testing.T) {
	date := fixedDate()

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.upserted); got != 0 {
		t.Errorf("expected 0 upserted memories, got %d", got)
	}
}

func TestPromptMemoryJob_StoreMemories_EvalSourceError(t *testing.T) {
	date := fixedDate()

	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{err: fmt.Errorf("db connection lost")}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.StoreMemories(context.Background(), date)
	if err == nil {
		t.Fatal("expected error from eval source, got nil")
	}
}

// ── UpdateOutcomes tests ──────────────────────────────────────────────────────

func TestPromptMemoryJob_UpdateOutcomes_HappyPath(t *testing.T) {
	date := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	r5 := 0.05
	stopHit := true
	targetHit := true
	recStop := 95.0

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	outcomes := []models.PromptTickerOutcome{
		{
			Ticker:          "AAPL",
			ListType:        models.ListTypeEP,
			ActualReturn5D:  floatPtr(r5),
			StopHit:         &stopHit,
			Target1Hit:      &targetHit,
			RecommendedStop: &recStop,
			EvaluatedDays:   10,
		},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	ptoSrc := &mockPTOVersionSource{outcomes: outcomes}
	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, ptoSrc, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 1 {
		t.Fatalf("expected 1 updated memory, got %d", got)
	}

	u := memStorer.updated[0]
	if u.status != "verified" {
		t.Errorf("status = %q, want verified", u.status)
	}
	if u.return5d == nil || *u.return5d != 0.05 {
		t.Errorf("return_5d = %v, want 0.05", u.return5d)
	}
	if u.stopHit == nil || *u.stopHit != true {
		t.Errorf("stop_hit = %v, want true", u.stopHit)
	}
	if u.targetHit == nil || *u.targetHit != true {
		t.Errorf("target_hit = %v, want true", u.targetHit)
	}
	if u.summary == nil {
		t.Fatal("expected non-nil summary")
	}
	expectedSummary := "Stop hit at $95.00. Target 1 reached. 5D return: +5.0%. [AAPL]"
	if *u.summary != expectedSummary {
		t.Errorf("summary = %q, want %q", *u.summary, expectedSummary)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_TooRecent(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 0 {
		t.Errorf("expected 0 updated (too recent), got %d", got)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_NoMatchFound(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-14 * 24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 0 {
		t.Errorf("expected 0 updated (no match, < 30 days), got %d", got)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_InsufficientData(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-45 * 24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 1 {
		t.Fatalf("expected 1 updated (insufficient_data), got %d", got)
	}
	if memStorer.updated[0].status != "insufficient_data" {
		t.Errorf("status = %q, want insufficient_data", memStorer.updated[0].status)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_NoPending(t *testing.T) {
	memStorer := &mockMemoryStorer{}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 0 {
		t.Errorf("expected 0 updated, got %d", got)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_GetPendingError(t *testing.T) {
	memStorer := &mockMemoryStorer{err: fmt.Errorf("db down")}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err == nil {
		t.Fatal("expected error from GetPendingOutcomes, got nil")
	}
}

func TestPromptMemoryJob_UpdateOutcomes_PTOLookupError(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-14 * 24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, &mockPTOVersionSource{}, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error (pto error is logged, not returned): %v", err)
	}

	if got := len(memStorer.updated); got != 0 {
		t.Errorf("expected 0 updated (pto lookup failed), got %d", got)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_MatchedByTickerAndListType(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-14 * 24 * time.Hour)
	r5 := 0.03

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	outcomes := []models.PromptTickerOutcome{
		{Ticker: "MSFT", ListType: models.ListTypeEP, EvaluatedDays: 10},
		{Ticker: "AAPL", ListType: models.ListTypeMomentum, EvaluatedDays: 10},
		{Ticker: "AAPL", ListType: models.ListTypeEP, ActualReturn5D: floatPtr(r5), EvaluatedDays: 10},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	ptoSrc := &mockPTOVersionSource{outcomes: outcomes}
	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, ptoSrc, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 1 {
		t.Fatalf("expected 1 updated, got %d", got)
	}

	u := memStorer.updated[0]
	if u.return5d == nil || *u.return5d != 0.03 {
		t.Errorf("return_5d = %v, want 0.03", u.return5d)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_EvaluatedDaysTooFew(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-14 * 24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	outcomes := []models.PromptTickerOutcome{
		{Ticker: "AAPL", ListType: models.ListTypeEP, EvaluatedDays: 3},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	ptoSrc := &mockPTOVersionSource{outcomes: outcomes}
	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, ptoSrc, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 0 {
		t.Errorf("expected 0 updated (evaluated_days < 5), got %d", got)
	}
}

func TestPromptMemoryJob_UpdateOutcomes_EvaluatedDaysTooFewOld(t *testing.T) {
	date := time.Now().UTC().Truncate(24 * time.Hour).Add(-45 * 24 * time.Hour)

	pending := []models.PromptMemory{
		{ID: uuid.New(), Date: date, Ticker: "AAPL", ListType: models.ListTypeEP},
	}

	outcomes := []models.PromptTickerOutcome{
		{Ticker: "AAPL", ListType: models.ListTypeEP, EvaluatedDays: 3},
	}

	memStorer := &mockMemoryStorer{pending: pending}
	evalSrc := &mockEvalSource{}
	embedder := &mockEmbedder{}

	ptoSrc := &mockPTOVersionSource{outcomes: outcomes}
	job := jobs.NewPromptMemoryJobFromSources(memStorer, evalSrc, ptoSrc, embedder, pmQuietLogger())
	err := job.UpdateOutcomes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(memStorer.updated); got != 1 {
		t.Fatalf("expected 1 updated (insufficient_data), got %d", got)
	}
	if memStorer.updated[0].status != "insufficient_data" {
		t.Errorf("status = %q, want insufficient_data", memStorer.updated[0].status)
	}
}

// ── generateOutcomeSummary tests ──────────────────────────────────────────────

func TestGenerateOutcomeSummary_StopHitOnly(t *testing.T) {
	stopHit := true
	recStop := 100.0

	mem := models.PromptMemory{Ticker: "AAPL"}
	pto := &models.PromptTickerOutcome{
		StopHit:         &stopHit,
		RecommendedStop: &recStop,
	}

	summary := jobs.GenerateOutcomeSummary(&mem, pto)
	expected := "Stop hit at $100.00. [AAPL]"
	if summary != expected {
		t.Errorf("summary = %q, want %q", summary, expected)
	}
}

func TestGenerateOutcomeSummary_TargetHitOnly(t *testing.T) {
	targetHit := true

	mem := models.PromptMemory{Ticker: "MSFT"}
	pto := &models.PromptTickerOutcome{
		Target1Hit: &targetHit,
	}

	summary := jobs.GenerateOutcomeSummary(&mem, pto)
	expected := "Target 1 reached. [MSFT]"
	if summary != expected {
		t.Errorf("summary = %q, want %q", summary, expected)
	}
}

func TestGenerateOutcomeSummary_ReturnOnly(t *testing.T) {
	r5 := -0.03

	mem := models.PromptMemory{Ticker: "NVDA"}
	pto := &models.PromptTickerOutcome{
		ActualReturn5D: floatPtr(r5),
	}

	summary := jobs.GenerateOutcomeSummary(&mem, pto)
	expected := "5D return: -3.0%. [NVDA]"
	if summary != expected {
		t.Errorf("summary = %q, want %q", summary, expected)
	}
}

func TestGenerateOutcomeSummary_NoData(t *testing.T) {
	mem := models.PromptMemory{Ticker: "GOOGL"}
	pto := &models.PromptTickerOutcome{}

	summary := jobs.GenerateOutcomeSummary(&mem, pto)
	expected := "Outcome recorded. [GOOGL]"
	if summary != expected {
		t.Errorf("summary = %q, want %q", summary, expected)
	}
}

func TestGenerateOutcomeSummary_StopHitUnknownLevel(t *testing.T) {
	stopHit := true

	mem := models.PromptMemory{Ticker: "AAPL"}
	pto := &models.PromptTickerOutcome{
		StopHit: &stopHit,
	}

	summary := jobs.GenerateOutcomeSummary(&mem, pto)
	expected := "Stop hit at unknown level. [AAPL]"
	if summary != expected {
		t.Errorf("summary = %q, want %q", summary, expected)
	}
}
