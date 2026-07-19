package outcomes_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services/outcomes"
)

// ── mocks ─────────────────────────────────────────────────────────────────────

type mockRankListReader struct {
	lists []models.DailyRankList
	err   error
}

func (m *mockRankListReader) GetAllRankLists(_ context.Context, _ time.Time) ([]models.DailyRankList, error) {
	return m.lists, m.err
}

type mockCandleReader struct {
	candles map[string][]models.CandleDaily // key: ticker
	err     error
}

func (m *mockCandleReader) GetCandles(_ context.Context, ticker string, _, _ time.Time) ([]models.CandleDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.candles[ticker], nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func day(base time.Time, offset int) time.Time {
	return base.AddDate(0, 0, offset)
}

// makeCandle builds a single candle for a given date with explicit OHLC.
func makeCandle(ticker string, date time.Time, open, high, low, c float64) models.CandleDaily {
	return models.CandleDaily{
		Ticker: ticker, Date: date,
		Open: open, High: high, Low: low, Close: c,
		Volume: 1_000_000,
	}
}

// makeCandleSeries builds candles from base date.
// Day i: open = startOpen + i*step, close = open + closeDelta,
// high = close+1, low = open-1.
func makeCandleSeries(ticker string, base time.Time, startOpen, closeDelta, step float64, count int) []models.CandleDaily {
	out := make([]models.CandleDaily, count)
	for i := 0; i < count; i++ {
		o := startOpen + float64(i)*step
		c := o + closeDelta
		out[i] = models.CandleDaily{
			Ticker: ticker,
			Date:   day(base, i),
			Open:   o,
			High:   c + 1.0,
			Low:    o - 1.0,
			Close:  c,
			Volume: 1_000_000,
		}
	}
	return out
}

// farFuture is used as the "today" parameter in tests so that the closed-session
// cap never clips the test candle data.
var farFuture = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

// ── tests ─────────────────────────────────────────────────────────────────────

// TestComputeOutcomes_Full20DayWindow verifies the canonical entry model:
//
//	Signal on T=Mar 1.  Entry = T+1 Open.  Tracking = T+1..T+20.
func TestComputeOutcomes_Full20DayWindow(t *testing.T) {
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeEP, Rank: 1, Ticker: "AAPL"},
		},
	}

	// 21 candles: T+0 (signal day, skipped for entry) + T+1..T+20 (tracking).
	// Day i: open = 100 + i, close = 100.5 + i, high = close+1, low = open-1.
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{
			"AAPL": makeCandleSeries("AAPL", signalDate, 100.0, 0.5, 1.0, 21),
		},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]

	// Entry price = T+1 Open = 100 + 1 = 101.0
	wantEntry := 101.0
	if r.EntryPrice != wantEntry {
		t.Errorf("entry_price = %f, want %f", r.EntryPrice, wantEntry)
	}
	if r.EvaluatedDays != 20 {
		t.Errorf("evaluated_days = %d, want 20", r.EvaluatedDays)
	}

	// Return5D: tracking day 5 = T+5, close = 100.5 + 5 = 105.5
	// (105.5 - 101) / 101 ≈ 0.04455
	if r.Return5D == nil {
		t.Fatal("return_5d is nil")
	}
	assertInRange(t, "return_5d", *r.Return5D, 0.044, 0.046)

	// Return10D: tracking day 10 = T+10, close = 110.5
	// (110.5 - 101) / 101 ≈ 0.09406
	if r.Return10D == nil {
		t.Fatal("return_10d is nil")
	}
	assertInRange(t, "return_10d", *r.Return10D, 0.093, 0.095)

	// Return20D: tracking day 20 = T+20, close = 120.5
	// (120.5 - 101) / 101 ≈ 0.19307
	if r.Return20D == nil {
		t.Fatal("return_20d is nil")
	}
	assertInRange(t, "return_20d", *r.Return20D, 0.192, 0.194)

	// MRU: highest high across T+1..T+20 = high of T+20 = 120.5 + 1 = 121.5
	// (121.5 - 101) / 101 ≈ 0.20297
	if r.MaxRunup20D == nil {
		t.Fatal("max_runup_20d is nil")
	}
	assertInRange(t, "max_runup_20d", *r.MaxRunup20D, 0.202, 0.204)

	// MDD: lowest low across T+1..T+20 = low of T+1 = open(T+1) - 1 = 100
	// (100 - 101) / 101 ≈ -0.00990
	if r.MaxDrawdown20D == nil {
		t.Fatal("max_drawdown_20d is nil")
	}
	assertInRange(t, "max_drawdown_20d", *r.MaxDrawdown20D, -0.010, -0.009)
}

func TestComputeOutcomes_PartialWindow(t *testing.T) {
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeMomentum, Rank: 1, Ticker: "MSFT"},
		},
	}

	// 8 candles: T+0 (signal) + T+1..T+7.  Only 7 tracking days.
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{
			"MSFT": makeCandleSeries("MSFT", signalDate, 200.0, 0.5, 2.0, 8),
		},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.EvaluatedDays != 7 {
		t.Errorf("evaluated_days = %d, want 7", r.EvaluatedDays)
	}
	if r.Return5D == nil {
		t.Fatal("return_5d should be populated for 7 tracking days")
	}
	if r.Return10D != nil {
		t.Error("return_10d should be nil for 7 tracking days")
	}
	if r.Return20D != nil {
		t.Error("return_20d should be nil for 7 tracking days")
	}
	if r.MaxRunup20D == nil {
		t.Error("max_runup_20d should be populated")
	}
	if r.MaxDrawdown20D == nil {
		t.Error("max_drawdown_20d should be populated")
	}
}

func TestComputeOutcomes_NoCandles(t *testing.T) {
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeEP, Rank: 1, Ticker: "AAPL"},
		},
	}
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results (no candle data), got %d", len(results))
	}
}

func TestComputeOutcomes_OnlySignalDayCandle(t *testing.T) {
	// We only have T+0 (the signal day). No T+1 candle → no entry price.
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeEP, Rank: 1, Ticker: "AAPL"},
		},
	}
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{
			"AAPL": {makeCandle("AAPL", signalDate, 100.0, 101.0, 99.0, 100.5)},
		},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results when only T+0 candle exists, got %d", len(results))
	}
}

func TestComputeOutcomes_EmptyRankList(t *testing.T) {
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{lists: nil}
	candles := &mockCandleReader{}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty rank list, got %d", len(results))
	}
}

func TestComputeOutcomes_IncludesTodayCandles(t *testing.T) {
	// Signal on T=Apr 10.  Candles exist for T+0..T+3.
	// Today = T+3 → included (pipeline runs after market close, so today's
	// bar is a complete session).  T+1, T+2, T+3 = 3 tracking days.
	signalDate := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeEP, Rank: 1, Ticker: "AAPL"},
		},
	}
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{
			"AAPL": makeCandleSeries("AAPL", signalDate, 100.0, 0.5, 1.0, 4),
		},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.EvaluatedDays != 3 {
		t.Errorf("evaluated_days = %d, want 3", r.EvaluatedDays)
	}
	if r.Return5D != nil {
		t.Error("return_5d should be nil with only 3 tracking days")
	}
	if r.EntryPrice != 101.0 {
		t.Errorf("entry_price = %f, want 101.0", r.EntryPrice)
	}
}

func TestComputeOutcomes_EntryIsT1Open_NotT0Close(t *testing.T) {
	// Verify specifically that entry price is T+1 Open, not T+0 Close.
	signalDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	ranks := &mockRankListReader{
		lists: []models.DailyRankList{
			{Date: signalDate, ListType: models.ListTypeEP, Rank: 1, Ticker: "AAPL"},
		},
	}

	// T+0: close = 150 (should NOT be entry price)
	// T+1: open = 152 (SHOULD be entry price — gap up overnight)
	candles := &mockCandleReader{
		candles: map[string][]models.CandleDaily{
			"AAPL": {
				makeCandle("AAPL", day(signalDate, 0), 148.0, 151.0, 147.0, 150.0),
				makeCandle("AAPL", day(signalDate, 1), 152.0, 155.0, 151.0, 154.0),
				makeCandle("AAPL", day(signalDate, 2), 153.0, 156.0, 152.0, 155.0),
			},
		},
	}

	svc := outcomes.NewTradeOutcomeServiceFromSources(ranks, candles, quietLogger())
	results, err := svc.ComputeOutcomes(context.Background(), signalDate, farFuture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.EntryPrice != 152.0 {
		t.Errorf("entry_price = %f, want 152.0 (T+1 Open, not T+0 Close)", r.EntryPrice)
	}
	if r.EvaluatedDays != 2 {
		t.Errorf("evaluated_days = %d, want 2", r.EvaluatedDays)
	}

	// MRU: highest high = max(155, 156) = 156 → (156-152)/152 ≈ 0.02632
	if r.MaxRunup20D == nil {
		t.Fatal("max_runup_20d is nil")
	}
	assertInRange(t, "max_runup_20d", *r.MaxRunup20D, 0.026, 0.027)

	// MDD: lowest low = min(151, 152) = 151 → (151-152)/152 ≈ -0.00658
	if r.MaxDrawdown20D == nil {
		t.Fatal("max_drawdown_20d is nil")
	}
	assertInRange(t, "max_drawdown_20d", *r.MaxDrawdown20D, -0.007, -0.006)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertInRange(t *testing.T, name string, got, lo, hi float64) {
	t.Helper()
	if got < lo || got > hi {
		t.Errorf("%s = %f, want in [%f, %f]", name, got, lo, hi)
	}
}
