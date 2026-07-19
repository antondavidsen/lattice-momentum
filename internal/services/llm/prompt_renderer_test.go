package llm

import (
	"strings"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

func TestSplitPrompt(t *testing.T) {
	raw := `SYSTEM
──────
You are a tactical swing trader.

You do not hallucinate data.

═══════════════════════════════════════════════════════════════════
USER
═══════════════════════════════════════════════════════════════════

## CONTEXT
The EP screener has fired.

## EP SCREENER OUTPUT — TOP 10
[PASTE 10 ROWS: ticker, price]

---

## YOUR ANALYSIS TASK
Do analysis.

═══════════════════════════════════════════════════════════════════`

	tmpl, err := splitPrompt(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(tmpl.System, "tactical swing trader") {
		t.Error("system prompt should contain persona")
	}
	if strings.Contains(tmpl.System, "USER") {
		t.Error("system prompt should not contain USER header")
	}

	if !strings.Contains(tmpl.User, "EP screener has fired") {
		t.Error("user prompt should contain context")
	}
	if !strings.Contains(tmpl.User, "[PASTE 10 ROWS:") {
		t.Error("user prompt should contain placeholder")
	}
}

func TestLoadPromptTemplate_AllListTypes(t *testing.T) {
	for _, lt := range []models.ListType{models.ListTypeMomentum} {
		tmpl, err := loadPromptTemplate(lt)
		if err != nil {
			t.Errorf("loadPromptTemplate(%s) error: %v", lt, err)
			continue
		}
		if tmpl.System == "" {
			t.Errorf("loadPromptTemplate(%s): empty system prompt", lt)
		}
		if tmpl.User == "" {
			t.Errorf("loadPromptTemplate(%s): empty user prompt", lt)
		}
	}
}

func TestRenderUserPrompt(t *testing.T) {
	tmpl := `## CONTEXT
Analysis for today.

## EP SCREENER OUTPUT — TOP 10
[PASTE 10 ROWS: ticker, price]

---

## YOUR ANALYSIS TASK
Do analysis.`

	c := 150.0
	relVol := 3.5
	perf3m := 0.25
	sector := "Technology"
	snap := models.TradingViewSnapshotDaily{
		Ticker:         "NVDA",
		Close:          &c,
		RelativeVolume: &relVol,
		Perf3M:         &perf3m,
		Sector:         &sector,
	}

	regime := &models.MarketRegimeDaily{
		Regime: "bull",
	}
	confidence := 75.0
	regime.Confidence = &confidence

	sectorScores := []models.SectorScoreDaily{
		{ETF: "XLK", Label: "LEADING", Score: 0.85},
	}

	result, tickerMap := renderUserPrompt(tmpl, time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC), models.ListTypeMomentum, []models.TradingViewSnapshotDaily{snap}, regime, sectorScores, nil, nil, "")

	// Should contain the date.
	if !strings.Contains(result, "2026-04-14") {
		t.Error("rendered prompt should contain date")
	}

	// Should contain the regime.
	if !strings.Contains(result, "BULL") {
		t.Error("rendered prompt should contain regime")
	}

	// Should contain the anonymous ID (not the real ticker).
	if !strings.Contains(result, "STOCK_A") {
		t.Error("rendered prompt should contain anonymous ID STOCK_A")
	}
	if strings.Contains(result, "NVDA") {
		t.Error("rendered prompt should NOT contain real ticker NVDA (ticker-blind)")
	}

	// Verify the ticker mapping.
	if tickerMap.ToAnon["NVDA"] != "STOCK_A" {
		t.Errorf("expected NVDA → STOCK_A, got %s", tickerMap.ToAnon["NVDA"])
	}
	if tickerMap.ToReal["STOCK_A"] != "NVDA" {
		t.Errorf("expected STOCK_A → NVDA, got %s", tickerMap.ToReal["STOCK_A"])
	}

	// Should NOT contain the placeholder anymore.
	if strings.Contains(result, "[PASTE 10 ROWS:") {
		t.Error("rendered prompt should have replaced the placeholder")
	}

	// Should contain sector score.
	if !strings.Contains(result, "XLK") {
		t.Error("rendered prompt should contain sector ETF")
	}
}

func TestFmtMarketCap(t *testing.T) {
	tests := []struct {
		input *float64
		want  string
	}{
		{nil, "[DATA MISSING]"},
		{floatP(3e12), "$3.0T"},
		{floatP(1.5e9), "$1.5B"},
		{floatP(500e6), "$500M"},
		{floatP(50000), "$50000"},
	}
	for _, tt := range tests {
		got := fmtMarketCap(tt.input)
		if got != tt.want {
			t.Errorf("fmtMarketCap(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func floatP(v float64) *float64 { return &v }

func TestDeAnonymizeResponse(t *testing.T) {
	mapping := TickerMapping{
		ToAnon: map[string]string{"NVDA": "STOCK_A", "AAPL": "STOCK_B", "MSFT": "STOCK_C"},
		ToReal: map[string]string{"STOCK_A": "NVDA", "STOCK_B": "AAPL", "STOCK_C": "MSFT"},
	}

	input := `**1. STOCK_A**
- Entry zone: $142.50
**2. STOCK_B**
- Entry zone: $198.00
| 1 | STOCK_A | TIGHT BASE |
| 2 | STOCK_B | VCP |
| 3 | STOCK_C | PULLBACK |`

	result := DeAnonymizeResponse(input, mapping)

	if strings.Contains(result, "STOCK_A") {
		t.Error("de-anonymised response should not contain STOCK_A")
	}
	if !strings.Contains(result, "NVDA") {
		t.Error("de-anonymised response should contain NVDA")
	}
	if !strings.Contains(result, "AAPL") {
		t.Error("de-anonymised response should contain AAPL")
	}
	if !strings.Contains(result, "MSFT") {
		t.Error("de-anonymised response should contain MSFT")
	}
}

func TestIndexToAnonID(t *testing.T) {
	if got := indexToAnonID(0); got != "STOCK_A" {
		t.Errorf("index 0 = %q, want STOCK_A", got)
	}
	if got := indexToAnonID(2); got != "STOCK_C" {
		t.Errorf("index 2 = %q, want STOCK_C", got)
	}
	if got := indexToAnonID(25); got != "STOCK_Z" {
		t.Errorf("index 25 = %q, want STOCK_Z", got)
	}
}
