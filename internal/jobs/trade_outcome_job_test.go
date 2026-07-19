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
	"ai-stock-service/internal/services/outcomes"
)

// mockOutcomeAdjuster is a no-op adjuster that passes through raw returns.
type mockOutcomeAdjuster struct{}

func (m *mockOutcomeAdjuster) AdjustForCorporateActions(_ context.Context, _ string, _ time.Time, rawReturn float64, _ int) (adjustedReturn float64, actionCount int, err error) {
	return rawReturn, 0, nil
}

func (m *mockOutcomeAdjuster) PlausibilityCheck(_ *outcomes.TradeOutcomeResult) string {
	return ""
}

// ── mocks ─────────────────────────────────────────────────────────────────────

type mockOutcomeComputer struct {
	results map[string][]outcomes.TradeOutcomeResult // key: date string
	err     error
}

func (m *mockOutcomeComputer) ComputeOutcomes(_ context.Context, signalDate, _ time.Time) ([]outcomes.TradeOutcomeResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results[signalDate.Format("2006-01-02")], nil
}

type mockOutcomeStorer struct {
	upserted []*models.TradeOutcomeDaily
	err      error
}

func (m *mockOutcomeStorer) UpsertTradeOutcome(_ context.Context, row *models.TradeOutcomeDaily) error {
	if m.err != nil {
		return m.err
	}
	m.upserted = append(m.upserted, row)
	return nil
}

type mockPendingDateFinder struct {
	dates []time.Time
	err   error
}

func (m *mockPendingDateFinder) GetPendingSignalDates(_ context.Context, _ time.Time, _ int) ([]time.Time, error) {
	return m.dates, m.err
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestTradeOutcomeJob_HappyPath(t *testing.T) {
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	r5 := 0.05
	r10 := 0.08
	mru := 0.12
	mdd := -0.03

	results := map[string][]outcomes.TradeOutcomeResult{
		"2026-03-20": {
			{
				EntryDate:      date,
				ListType:       models.ListTypeEP,
				Ticker:         "AAPL",
				Rank:           1,
				EntryPrice:     150.0,
				Return5D:       &r5,
				Return10D:      &r10,
				MaxRunup20D:    &mru,
				MaxDrawdown20D: &mdd,
				EvaluatedDays:  12,
			},
			{
				EntryDate:     date,
				ListType:      models.ListTypeMomentum,
				Ticker:        "MSFT",
				Rank:          1,
				EntryPrice:    400.0,
				Return5D:      &r5,
				EvaluatedDays: 7,
			},
		},
	}

	finder := &mockPendingDateFinder{dates: []time.Time{date}}
	computer := &mockOutcomeComputer{results: results}
	storer := &mockOutcomeStorer{}

	job := jobs.NewTradeOutcomeJobFromSources(computer, &mockOutcomeAdjuster{}, storer, finder, quietLogger())

	err := job.RunTradeOutcomeJob(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(storer.upserted) != 2 {
		t.Fatalf("expected 2 upserted rows, got %d", len(storer.upserted))
	}
	if storer.upserted[0].Ticker != "AAPL" {
		t.Errorf("first upserted ticker = %q, want AAPL", storer.upserted[0].Ticker)
	}
	if storer.upserted[1].Ticker != "MSFT" {
		t.Errorf("second upserted ticker = %q, want MSFT", storer.upserted[1].Ticker)
	}
	if storer.upserted[0].EvaluatedDays != 12 {
		t.Errorf("AAPL evaluated_days = %d, want 12", storer.upserted[0].EvaluatedDays)
	}
}

func TestTradeOutcomeJob_NoPendingDates(t *testing.T) {
	finder := &mockPendingDateFinder{dates: nil}
	computer := &mockOutcomeComputer{}
	storer := &mockOutcomeStorer{}

	job := jobs.NewTradeOutcomeJobFromSources(computer, &mockOutcomeAdjuster{}, storer, finder, quietLogger())

	err := job.RunTradeOutcomeJob(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storer.upserted) != 0 {
		t.Errorf("expected 0 upserted rows, got %d", len(storer.upserted))
	}
}

func TestTradeOutcomeJob_FinderError(t *testing.T) {
	finder := &mockPendingDateFinder{err: fmt.Errorf("db connection lost")}
	computer := &mockOutcomeComputer{}
	storer := &mockOutcomeStorer{}

	job := jobs.NewTradeOutcomeJobFromSources(computer, &mockOutcomeAdjuster{}, storer, finder, quietLogger())

	err := job.RunTradeOutcomeJob(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error from finder failure, got nil")
	}
}

func TestTradeOutcomeJob_ComputeError(t *testing.T) {
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	finder := &mockPendingDateFinder{dates: []time.Time{date}}
	computer := &mockOutcomeComputer{err: fmt.Errorf("compute failure")}
	storer := &mockOutcomeStorer{}

	job := jobs.NewTradeOutcomeJobFromSources(computer, &mockOutcomeAdjuster{}, storer, finder, quietLogger())

	err := job.RunTradeOutcomeJob(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error from compute failure, got nil")
	}
}

func TestTradeOutcomeJob_PersistError(t *testing.T) {
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	r5 := 0.05

	results := map[string][]outcomes.TradeOutcomeResult{
		"2026-03-20": {
			{
				EntryDate:     date,
				ListType:      models.ListTypeEP,
				Ticker:        "AAPL",
				Rank:          1,
				EntryPrice:    150.0,
				Return5D:      &r5,
				EvaluatedDays: 7,
			},
		},
	}

	finder := &mockPendingDateFinder{dates: []time.Time{date}}
	computer := &mockOutcomeComputer{results: results}
	storer := &mockOutcomeStorer{err: fmt.Errorf("db write failed")}

	job := jobs.NewTradeOutcomeJobFromSources(computer, &mockOutcomeAdjuster{}, storer, finder, quietLogger())

	err := job.RunTradeOutcomeJob(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error from persist failure, got nil")
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
