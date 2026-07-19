package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

type mockEvalSource struct {
	evals map[string][]models.LLMListEvaluation // key: date string
}

func (m *mockEvalSource) GetEvaluationsByDate(_ context.Context, date time.Time) ([]models.LLMListEvaluation, error) {
	return m.evals[date.Format("2006-01-02")], nil
}

type mockOutcomeSource struct {
	outcomes map[string][]models.TradeOutcomeDaily
}

func (m *mockOutcomeSource) GetTradeOutcomes(_ context.Context, entryDate time.Time) ([]models.TradeOutcomeDaily, error) {
	return m.outcomes[entryDate.Format("2006-01-02")], nil
}

type mockAttributionCandleSource struct {
	candles map[string][]models.CandleDaily // key: ticker
}

func (m *mockAttributionCandleSource) GetCandles(_ context.Context, ticker string, _, _ time.Time) ([]models.CandleDaily, error) {
	return m.candles[ticker], nil
}

type mockPTOStorer struct {
	outcomes []*models.PromptTickerOutcome
}

func (m *mockPTOStorer) UpsertOutcome(_ context.Context, o *models.PromptTickerOutcome) error {
	m.outcomes = append(m.outcomes, o)
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestAttributionJob_HappyPath(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)

	parsedJSON := mustMarshal(t, models.EvaluationParsedOutput{
		Tickers: []models.EvaluationParsedTicker{
			{
				Ticker:    "NVDA",
				Rank:      1,
				Setup:     "TIGHT BASE",
				EntryLow:  floatPtr64(140.00),
				EntryHigh: floatPtr64(143.00),
				StopPrice: floatPtr64(136.80),
				Target1:   floatPtr64(155.00),
				Target2:   floatPtr64(168.00),
			},
			{
				Ticker: "AAPL",
				Rank:   2,
				Setup:  "VCP",
			},
		},
		ParseSuccess: true,
	})

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {
				{
					Date:          date,
					ListType:      models.ListTypeEP,
					PromptVersion: "v7-abc12345",
					InputTickers:  []string{"NVDA", "AAPL", "MSFT"},
					ParsedJSON:    parsedJSON,
				},
			},
		},
	}

	outcomeSrc := &mockOutcomeSource{
		outcomes: map[string][]models.TradeOutcomeDaily{
			"2026-04-20": {
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "NVDA", EntryPrice: 143.0, Return5D: floatPtr64(0.05), EvaluatedDays: 5},
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "AAPL", EntryPrice: 199.0, Return5D: floatPtr64(-0.02), EvaluatedDays: 5},
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "MSFT", EntryPrice: 420.0, Return5D: floatPtr64(0.03), EvaluatedDays: 5},
			},
		},
	}

	candleSrc := &mockAttributionCandleSource{
		candles: map[string][]models.CandleDaily{
			"NVDA": {
				{Date: date.AddDate(0, 0, 1), Low: 140.0, High: 150.0},
				{Date: date.AddDate(0, 0, 2), Low: 135.0, High: 156.0}, // stop hit (135 < 136.80), target 1 hit (156 > 155)
			},
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(evalSrc, outcomeSrc, candleSrc, storer, log)
	err := job.RunAttributionJob(context.Background(), today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 outcomes: NVDA (recommended), AAPL (recommended), MSFT (rejected).
	if len(storer.outcomes) != 3 {
		t.Fatalf("expected 3 outcomes, got %d", len(storer.outcomes))
	}

	// Find each outcome.
	outcomeMap := make(map[string]*models.PromptTickerOutcome)
	for _, o := range storer.outcomes {
		outcomeMap[o.Ticker] = o
	}

	// NVDA: recommended, stop hit, target 1 hit.
	nvda := outcomeMap["NVDA"]
	if nvda == nil {
		t.Fatal("missing NVDA outcome")
	}
	if !nvda.LLMRecommended {
		t.Error("NVDA should be recommended")
	}
	if nvda.StopHit == nil || !*nvda.StopHit {
		t.Error("NVDA stop should be hit")
	}
	if nvda.Target1Hit == nil || !*nvda.Target1Hit {
		t.Error("NVDA target 1 should be hit")
	}
	if nvda.ActualReturn5D == nil || *nvda.ActualReturn5D != 0.05 {
		t.Errorf("NVDA return_5d should be 0.05, got %v", nvda.ActualReturn5D)
	}

	// R03 path-aware exit assertions for NVDA:
	// Day 2 candle (2026-04-22): low=135 <= stop=136.80, high=156 >= t1=155 → stop exit + T1 whipsaw.
	// entry = (140+143)/2 = 141.50, actual_rr = (136.80-141.50)/(141.50-136.80) = -1.0
	if nvda.ExitType == nil || *nvda.ExitType != "stop" {
		t.Errorf("NVDA exit_type should be 'stop', got %v", nvda.ExitType)
	}
	if nvda.ExitPrice == nil || *nvda.ExitPrice != 136.80 {
		t.Errorf("NVDA exit_price should be 136.80, got %v", nvda.ExitPrice)
	}
	expectedExitDate := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	if nvda.ExitDate == nil || !nvda.ExitDate.Equal(expectedExitDate) {
		t.Errorf("NVDA exit_date should be 2026-04-22, got %v", nvda.ExitDate)
	}
	if nvda.ActualRRAchieved == nil || *nvda.ActualRRAchieved != -1.0 {
		t.Errorf("NVDA actual_rr_achieved should be -1.0, got %v", nvda.ActualRRAchieved)
	}
	if !nvda.T1Hit {
		t.Error("NVDA t1_hit should be true (T1 touched on stop day)")
	}
	if nvda.LevelsInvalid {
		t.Error("NVDA levels_invalid should be false (valid geometry)")
	}

	// AAPL: recommended but no stop/entry levels set → exit fields remain nil.
	aapl := outcomeMap["AAPL"]
	if aapl == nil {
		t.Fatal("missing AAPL outcome")
	}
	if !aapl.LLMRecommended {
		t.Error("AAPL should be recommended")
	}
	if aapl.ExitType != nil {
		t.Errorf("AAPL exit_type should be nil (no levels set), got %v", *aapl.ExitType)
	}
	if aapl.ExitPrice != nil {
		t.Errorf("AAPL exit_price should be nil (no levels set), got %v", *aapl.ExitPrice)
	}
	if aapl.ExitDate != nil {
		t.Errorf("AAPL exit_date should be nil (no levels set), got %v", *aapl.ExitDate)
	}
	if aapl.ActualRRAchieved != nil {
		t.Errorf("AAPL actual_rr_achieved should be nil (no levels set), got %v", *aapl.ActualRRAchieved)
	}
	if aapl.LevelsInvalid {
		t.Error("AAPL levels_invalid should be false (no levels to validate)")
	}

	// MSFT: rejected.
	msft := outcomeMap["MSFT"]
	if msft == nil {
		t.Fatal("missing MSFT outcome")
	}
	if msft.LLMRecommended {
		t.Error("MSFT should be rejected")
	}
	if msft.RecommendedSetup != nil {
		t.Error("rejected ticker should have nil setup")
	}
	if msft.ActualReturn5D == nil || *msft.ActualReturn5D != 0.03 {
		t.Errorf("MSFT return_5d should be 0.03 (copied from trade outcomes)")
	}
	// Rejected: exit fields should be nil (no simulation).
	if msft.ExitType != nil {
		t.Errorf("MSFT exit_type should be nil (rejected), got %v", *msft.ExitType)
	}
	if msft.ExitPrice != nil {
		t.Errorf("MSFT exit_price should be nil (rejected), got %v", *msft.ExitPrice)
	}
	if msft.ExitDate != nil {
		t.Errorf("MSFT exit_date should be nil (rejected), got %v", *msft.ExitDate)
	}
	if msft.ActualRRAchieved != nil {
		t.Errorf("MSFT actual_rr_achieved should be nil (rejected), got %v", *msft.ActualRRAchieved)
	}
	if msft.LevelsInvalid {
		t.Error("MSFT levels_invalid should be false (rejected)")
	}
}

func TestAttributionJob_NoParsedData(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {
				{
					Date:         date,
					ListType:     models.ListTypeEP,
					InputTickers: []string{"NVDA"},
					ParsedJSON:   json.RawMessage(`{}`),
				},
			},
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(
		evalSrc,
		&mockOutcomeSource{outcomes: map[string][]models.TradeOutcomeDaily{}},
		&mockAttributionCandleSource{candles: map[string][]models.CandleDaily{}},
		storer, log,
	)
	err := job.RunAttributionJob(context.Background(), today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storer.outcomes) != 0 {
		t.Errorf("expected 0 outcomes for empty parsed_json, got %d", len(storer.outcomes))
	}
}

func TestAttributionJob_Idempotent(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)

	parsedJSON := mustMarshal(t, models.EvaluationParsedOutput{
		Tickers:      []models.EvaluationParsedTicker{{Ticker: "NVDA", Rank: 1}},
		ParseSuccess: true,
	})

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {{
				Date: date, ListType: models.ListTypeEP,
				PromptVersion: "v7-test", InputTickers: []string{"NVDA"},
				ParsedJSON: parsedJSON,
			}},
		},
	}
	outcomeSrc := &mockOutcomeSource{
		outcomes: map[string][]models.TradeOutcomeDaily{
			"2026-04-20": {{
				EntryDate: date, ListType: models.ListTypeEP,
				Ticker: "NVDA", EntryPrice: 143.0, EvaluatedDays: 5,
			}},
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(
		evalSrc, outcomeSrc,
		&mockAttributionCandleSource{candles: map[string][]models.CandleDaily{}},
		storer, log,
	)

	// Run twice.
	_ = job.RunAttributionJob(context.Background(), today)
	_ = job.RunAttributionJob(context.Background(), today)

	// Storer accumulates (in a real DB the upsert would be idempotent).
	// The point is that no error occurs.
	if len(storer.outcomes) != 2 {
		t.Errorf("expected 2 upsert calls (idempotent), got %d", len(storer.outcomes))
	}
}

func TestAttributionJob_LevelsInvalid(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)

	// Ticker with stop >= entry_low → level validation fail.
	// entry_low=140, stop=145 → stop >= entry_low → levels_invalid=true.
	parsedJSON := mustMarshal(t, models.EvaluationParsedOutput{
		Tickers: []models.EvaluationParsedTicker{
			{
				Ticker:    "INVAL",
				Rank:      1,
				Setup:     "TIGHT BASE",
				EntryLow:  floatPtr64(140.00),
				EntryHigh: floatPtr64(143.00),
				StopPrice: floatPtr64(145.00), // stop >= entry_low → invalid!
				Target1:   floatPtr64(155.00),
				Target2:   floatPtr64(168.00),
			},
		},
		ParseSuccess: true,
	})

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {
				{
					Date:          date,
					ListType:      models.ListTypeEP,
					PromptVersion: "v7-test",
					InputTickers:  []string{"INVAL"},
					ParsedJSON:    parsedJSON,
				},
			},
		},
	}

	outcomeSrc := &mockOutcomeSource{
		outcomes: map[string][]models.TradeOutcomeDaily{
			"2026-04-20": {
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "INVAL", EntryPrice: 143.0, EvaluatedDays: 5},
			},
		},
	}

	// Candle source returning some data (EvaluatedDays > 0 so replay would run).
	candleSrc := &mockAttributionCandleSource{
		candles: map[string][]models.CandleDaily{
			"INVAL": {
				{Date: date.AddDate(0, 0, 1), Low: 140.0, High: 150.0},
			},
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(evalSrc, outcomeSrc, candleSrc, storer, log)
	err := job.RunAttributionJob(context.Background(), today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(storer.outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(storer.outcomes))
	}

	pt := storer.outcomes[0]
	if !pt.LevelsInvalid {
		t.Error("INVAL levels_invalid should be true (stop >= entry_low)")
	}
	// Exit fields should remain nil when levels are invalid (no simulation runs).
	if pt.ExitType != nil {
		t.Errorf("INVAL exit_type should be nil when levels_invalid, got %v", *pt.ExitType)
	}
	if pt.ExitPrice != nil {
		t.Errorf("INVAL exit_price should be nil when levels_invalid, got %v", *pt.ExitPrice)
	}
	if pt.ExitDate != nil {
		t.Errorf("INVAL exit_date should be nil when levels_invalid, got %v", *pt.ExitDate)
	}
	if pt.ActualRRAchieved != nil {
		t.Errorf("INVAL actual_rr_achieved should be nil when levels_invalid, got %v", *pt.ActualRRAchieved)
	}
}

func TestAttributionJob_T2Exit(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)

	parsedJSON := mustMarshal(t, models.EvaluationParsedOutput{
		Tickers: []models.EvaluationParsedTicker{
			{
				Ticker:    "T2TEST",
				Rank:      1,
				Setup:     "TIGHT BASE",
				EntryLow:  floatPtr64(100.00),
				EntryHigh: floatPtr64(105.00),
				StopPrice: floatPtr64(95.00),
				Target1:   floatPtr64(115.00),
				Target2:   floatPtr64(130.00),
			},
		},
		ParseSuccess: true,
	})

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {
				{
					Date:          date,
					ListType:      models.ListTypeEP,
					PromptVersion: "v7-test",
					InputTickers:  []string{"T2TEST"},
					ParsedJSON:    parsedJSON,
				},
			},
		},
	}

	outcomeSrc := &mockOutcomeSource{
		outcomes: map[string][]models.TradeOutcomeDaily{
			"2026-04-20": {
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "T2TEST", EntryPrice: 105.0, EvaluatedDays: 5},
			},
		},
	}

	// Candle sequence: day 1 hits T1 (high=120 >= t1=115), day 4 hits T2 (high=135 >= t2=130).
	// No stop hit. T1 is informational, T2 is full exit.
	candleSrc := &mockAttributionCandleSource{
		candles: map[string][]models.CandleDaily{
			"T2TEST": {
				{Date: date.AddDate(0, 0, 1), Low: 102.0, High: 120.0}, // T1 hit
				{Date: date.AddDate(0, 0, 2), Low: 108.0, High: 112.0}, // nothing
				{Date: date.AddDate(0, 0, 3), Low: 107.0, High: 114.0}, // nothing
				{Date: date.AddDate(0, 0, 4), Low: 125.0, High: 135.0}, // T2 hit (135 >= 130)
				{Date: date.AddDate(0, 0, 5), Low: 120.0, High: 140.0}, // beyond exit
			},
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(evalSrc, outcomeSrc, candleSrc, storer, log)
	err := job.RunAttributionJob(context.Background(), today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(storer.outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(storer.outcomes))
	}

	pt := storer.outcomes[0]
	if pt.LevelsInvalid {
		t.Error("T2TEST levels_invalid should be false")
	}
	if pt.ExitType == nil || *pt.ExitType != "target_2" {
		t.Errorf("T2TEST exit_type should be 'target_2', got %v", pt.ExitType)
	}
	if pt.ExitPrice == nil || *pt.ExitPrice != 130.0 {
		t.Errorf("T2TEST exit_price should be 130.0, got %v", pt.ExitPrice)
	}
	expectedExitDate := date.AddDate(0, 0, 4)
	if pt.ExitDate == nil || !pt.ExitDate.Equal(expectedExitDate) {
		t.Errorf("T2TEST exit_date should be %v, got %v", expectedExitDate, pt.ExitDate)
	}
	if !pt.T1Hit {
		t.Error("T2TEST t1_hit should be true (T1 was hit on day 1)")
	}
	// entry = (100+105)/2 = 102.50, exit_price = 130.0, stop = 95.0
	// actual_rr = (130 - 102.50) / (102.50 - 95.0) = 27.50 / 7.50 ≈ 3.6667
	if pt.ActualRRAchieved == nil || *pt.ActualRRAchieved < 3.6 || *pt.ActualRRAchieved > 3.7 {
		t.Errorf("T2TEST actual_rr_achieved should be ~3.6667, got %v", pt.ActualRRAchieved)
	}
	if pt.StopHit != nil && *pt.StopHit {
		t.Error("T2TEST stop_hit should be false")
	}
}

func TestAttributionJob_TimeExit(t *testing.T) {
	date := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)

	parsedJSON := mustMarshal(t, models.EvaluationParsedOutput{
		Tickers: []models.EvaluationParsedTicker{
			{
				Ticker:    "TIMEX",
				Rank:      1,
				Setup:     "VCP",
				EntryLow:  floatPtr64(50.00),
				EntryHigh: floatPtr64(55.00),
				StopPrice: floatPtr64(45.00),
				Target1:   floatPtr64(70.00),
				Target2:   floatPtr64(85.00),
			},
		},
		ParseSuccess: true,
	})

	evalSrc := &mockEvalSource{
		evals: map[string][]models.LLMListEvaluation{
			"2026-04-20": {
				{
					Date:          date,
					ListType:      models.ListTypeEP,
					PromptVersion: "v7-test",
					InputTickers:  []string{"TIMEX"},
					ParsedJSON:    parsedJSON,
				},
			},
		},
	}

	outcomeSrc := &mockOutcomeSource{
		outcomes: map[string][]models.TradeOutcomeDaily{
			"2026-04-20": {
				{EntryDate: date, ListType: models.ListTypeEP, Ticker: "TIMEX", EntryPrice: 55.0, EvaluatedDays: 20},
			},
		},
	}

	// 20 trading days with low/high never touching stop (45) or targets (70/85).
	candles := make([]models.CandleDaily, 20)
	for i := 0; i < 20; i++ {
		candles[i] = models.CandleDaily{
			Date:  date.AddDate(0, 0, i+1),
			Low:   52.0,
			High:  58.0,
			Close: 57.5,
		}
	}
	// Last day close = 57.5 → time exit price = 57.5.
	candles[19].Close = 57.5

	candleSrc := &mockAttributionCandleSource{
		candles: map[string][]models.CandleDaily{
			"TIMEX": candles,
		},
	}

	storer := &mockPTOStorer{}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	job := NewPromptOutcomeAttributionJobFromSources(evalSrc, outcomeSrc, candleSrc, storer, log)
	err := job.RunAttributionJob(context.Background(), today)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(storer.outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(storer.outcomes))
	}

	pt := storer.outcomes[0]
	if pt.LevelsInvalid {
		t.Error("TIMEX levels_invalid should be false")
	}
	if pt.ExitType == nil || *pt.ExitType != "time" {
		t.Errorf("TIMEX exit_type should be 'time', got %v", pt.ExitType)
	}
	if pt.ExitPrice == nil || *pt.ExitPrice != 57.5 {
		t.Errorf("TIMEX exit_price should be 57.5 (last day close), got %v", pt.ExitPrice)
	}
	expectedExitDate := date.AddDate(0, 0, 20)
	if pt.ExitDate == nil || !pt.ExitDate.Equal(expectedExitDate) {
		t.Errorf("TIMEX exit_date should be %v, got %v", expectedExitDate, pt.ExitDate)
	}
	if pt.T1Hit {
		t.Error("TIMEX t1_hit should be false (no levels touched)")
	}
	if pt.StopHit != nil && *pt.StopHit {
		t.Error("TIMEX stop_hit should be false")
	}
	if pt.Target1Hit != nil && *pt.Target1Hit {
		t.Error("TIMEX target_1_hit should be false")
	}
	// entry = (50+55)/2 = 52.50, exit_price = 57.5, stop = 45.0
	// actual_rr = (57.5 - 52.50) / (52.50 - 45.0) = 5.0 / 7.5 ≈ 0.6667
	if pt.ActualRRAchieved == nil || *pt.ActualRRAchieved < 0.66 || *pt.ActualRRAchieved > 0.67 {
		t.Errorf("TIMEX actual_rr_achieved should be ~0.6667, got %v", pt.ActualRRAchieved)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func floatPtr64(v float64) *float64 { return &v }

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
