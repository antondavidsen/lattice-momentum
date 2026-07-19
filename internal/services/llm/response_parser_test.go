package llm

import (
	"testing"
)

// ── Realistic EP response sample ──────────────────────────────────────────────

const sampleEPResponse = `
## ANALYSIS

**1. NVDA — NVIDIA Corporation**
- **Setup**: TIGHT BASE
- **Entry zone**: $142.50–$145.00
- **Stop**: $136.80, Risk 4.2%
- **T1 / T2**: $155.00 / $168.00
- **R/R**: 3.2:1
- **Size**: STANDARD (20%)
- **Hold period**: 1-2 weeks
- **A/D**: ACCUMULATION

**2. AAPL — Apple Inc.**
- **Setup**: VCP
- **Entry zone**: $198.00–$200.50
- **Stop**: $192.00, Risk 3.5%
- **T1 / T2**: $210.00 / $220.00
- **R/R**: 3.0:1
- **Size**: FULL (25%)
- **Conviction**: HIGH

**3. MSFT — Microsoft Corporation**
- **Setup**: PULLBACK
- **Entry zone**: $425.00-$430.00
- **Stop**: $415.00, Risk 2.3%
- **T1 / T2**: $450.00 / $475.00
- **R/R**: 4.1:1
- **Size**: REDUCED (10%)

## SUMMARY TABLE

| Rank | Ticker | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------|-------|----|----|-----|------|
| 1 | NVDA | TIGHT BASE | $142.50–$145.00 | $136.80 | 4.2% | $155.00 | $168.00 | 3.2:1 | STANDARD |
| 2 | AAPL | VCP | $198.00–$200.50 | $192.00 | 3.5% | $210.00 | $220.00 | 3.0:1 | FULL |
| 3 | MSFT | PULLBACK | $425.00–$430.00 | $415.00 | 2.3% | $450.00 | $475.00 | 4.1:1 | REDUCED |
`

const sampleMomentumResponse = `
## MOMENTUM ANALYSIS

**1. AMD — Advanced Micro Devices**
- **Setup**: BREAKOUT
- **Entry zone**: $178.50–$180.00
- **Stop**: $172.00
- **T1 / T2**: $195.00 / $210.00
- **R/R**: 2.8:1
- **Size**: STANDARD

**2. SMCI — Super Micro Computer**
- **Setup**: HTF
- **Entry zone**: $890.00 to $920.00
- **Stop**: $850.00
- **T1 / T2**: $1,000.00 / $1,100.00
- **R/R**: 2.3:1
- **Size**: STARTER

## SUMMARY TABLE

| Rank | Ticker | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------|-------|----|----|-----|------|
| 1 | AMD | BREAKOUT | $178.50–$180.00 | $172.00 | 3.6% | $195.00 | $210.00 | 2.8:1 | STANDARD |
| 2 | SMCI | HTF | $890.00–$920.00 | $850.00 | 4.4% | $1,000.00 | $1,100.00 | 2.3:1 | STARTER |
`

const sampleLeadersResponse = `
## MARKET LEADERS ANALYSIS

**1. COST — Costco Wholesale**
- **Setup**: EP TIER 1
- **Conviction**: HIGH
- **Entry zone**: $785.00–$790.00
- **Stop**: $770.00, Risk 2.0%
- **T1 / T2**: $820.00 / $850.00
- **R/R**: 3.5:1
- **Size**: FULL

**2. LLY — Eli Lilly**
- **Setup**: EP TIER 2
- **Conviction**: MEDIUM
- **Entry zone**: $820.00–$830.00
- **Stop**: $800.00
- **T1 / T2**: $870.00 / $900.00
- **R/R**: 2.4:1
- **Size**: STANDARD

| Rank | Ticker | Setup | Conviction | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------------|------|-------|----|----|-----|------|
| 1 | COST | EP TIER 1 | HIGH | $785.00–$790.00 | $770.00 | 2.0% | $820.00 | $850.00 | 3.5:1 | FULL |
| 2 | LLY | EP TIER 2 | MEDIUM | $820.00–$830.00 | $800.00 | 2.4% | $870.00 | $900.00 | 2.4:1 | STANDARD |
`

func TestParseEvaluationResponse_EP(t *testing.T) {
	result := ParseEvaluationResponse(sampleEPResponse)

	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if result.ParserVersion != "regex-v1" {
		t.Errorf("expected parser version regex-v1, got %s", result.ParserVersion)
	}
	if len(result.Tickers) != 3 {
		t.Fatalf("expected 3 tickers, got %d", len(result.Tickers))
	}

	// Check NVDA (first ticker).
	nvda := result.Tickers[0]
	if nvda.Ticker != "NVDA" {
		t.Errorf("expected ticker NVDA, got %s", nvda.Ticker)
	}
	if nvda.Rank != 1 {
		t.Errorf("expected rank 1, got %d", nvda.Rank)
	}
	if nvda.Setup != "TIGHT BASE" {
		t.Errorf("expected setup TIGHT BASE, got %s", nvda.Setup)
	}
	assertFloat(t, "NVDA.EntryLow", nvda.EntryLow, 142.50)
	assertFloat(t, "NVDA.EntryHigh", nvda.EntryHigh, 145.00)
	assertFloat(t, "NVDA.StopPrice", nvda.StopPrice, 136.80)
	assertFloat(t, "NVDA.Target1", nvda.Target1, 155.00)
	assertFloat(t, "NVDA.Target2", nvda.Target2, 168.00)
	assertFloat(t, "NVDA.RiskReward", nvda.RiskReward, 3.2)
	if nvda.PositionSize != "STANDARD" {
		t.Errorf("expected size STANDARD, got %s", nvda.PositionSize)
	}

	// Check AAPL.
	aapl := result.Tickers[1]
	if aapl.Ticker != "AAPL" {
		t.Errorf("expected ticker AAPL, got %s", aapl.Ticker)
	}
	if aapl.Conviction != "HIGH" {
		t.Errorf("expected conviction HIGH, got %s", aapl.Conviction)
	}
	assertFloat(t, "AAPL.EntryLow", aapl.EntryLow, 198.00)
	assertFloat(t, "AAPL.StopPrice", aapl.StopPrice, 192.00)
}

func TestParseEvaluationResponse_Momentum(t *testing.T) {
	result := ParseEvaluationResponse(sampleMomentumResponse)

	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 2 {
		t.Fatalf("expected 2 tickers, got %d", len(result.Tickers))
	}

	smci := result.Tickers[1]
	if smci.Ticker != "SMCI" {
		t.Errorf("expected SMCI, got %s", smci.Ticker)
	}
	assertFloat(t, "SMCI.Target1", smci.Target1, 1000.00)
	assertFloat(t, "SMCI.EntryLow", smci.EntryLow, 890.00)
	if smci.PositionSize != "STARTER" {
		t.Errorf("expected STARTER, got %s", smci.PositionSize)
	}
}

func TestParseEvaluationResponse_Leaders(t *testing.T) {
	result := ParseEvaluationResponse(sampleLeadersResponse)

	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 2 {
		t.Fatalf("expected 2 tickers, got %d", len(result.Tickers))
	}

	cost := result.Tickers[0]
	if cost.Conviction != "HIGH" {
		t.Errorf("expected HIGH conviction, got %s", cost.Conviction)
	}
	if cost.Setup != "EP TIER 1" {
		t.Errorf("expected EP TIER 1, got %s", cost.Setup)
	}
}

func TestParseEvaluationResponse_Empty(t *testing.T) {
	result := ParseEvaluationResponse("")
	if result.ParseSuccess {
		t.Error("expected parse failure on empty input")
	}
	if len(result.ParseErrors) == 0 {
		t.Error("expected at least one parse error")
	}
}

func TestParseEvaluationResponse_Garbage(t *testing.T) {
	result := ParseEvaluationResponse("This is not a valid LLM response with random text.")
	if result.ParseSuccess {
		t.Error("expected parse failure on garbage input")
	}
}

func TestParsePrice(t *testing.T) {
	tests := []struct {
		input string
		want  *float64
	}{
		{"$142.50", floatPtr(142.50)},
		{"142.50", floatPtr(142.50)},
		{"$1,234.56", floatPtr(1234.56)},
		{"", nil},
		{"[INSUFFICIENT DATA]", nil},
		{"-", nil},
		{"N/A", nil},
	}

	for _, tt := range tests {
		got := parsePrice(tt.input)
		if tt.want == nil {
			if got != nil {
				t.Errorf("parsePrice(%q) = %v, want nil", tt.input, *got)
			}
			continue
		}
		if got == nil {
			t.Errorf("parsePrice(%q) = nil, want %v", tt.input, *tt.want)
			continue
		}
		if *got != *tt.want {
			t.Errorf("parsePrice(%q) = %v, want %v", tt.input, *got, *tt.want)
		}
	}
}

func TestParseEntryZone(t *testing.T) {
	tests := []struct {
		input    string
		wantLow  *float64
		wantHigh *float64
	}{
		{"$142.50–$145.00", floatPtr(142.50), floatPtr(145.00)},
		{"$142.50-$145.00", floatPtr(142.50), floatPtr(145.00)},
		{"$142.50 to $145.00", floatPtr(142.50), floatPtr(145.00)},
		{"$142.50", floatPtr(142.50), floatPtr(142.50)},
		{"", nil, nil},
	}

	for _, tt := range tests {
		gotLow, gotHigh := parseEntryZone(tt.input)
		assertFloatP(t, "low("+tt.input+")", gotLow, tt.wantLow)
		assertFloatP(t, "high("+tt.input+")", gotHigh, tt.wantHigh)
	}
}

func TestParseRiskReward(t *testing.T) {
	tests := []struct {
		input string
		want  *float64
	}{
		{"3.2:1", floatPtr(3.2)},
		{"3:1", floatPtr(3.0)},
		{"4.1:1", floatPtr(4.1)},
		{"", nil},
	}

	for _, tt := range tests {
		got := parseRiskReward(tt.input)
		assertFloatP(t, "rr("+tt.input+")", got, tt.want)
	}
}

func TestParseSummaryTable_Only(t *testing.T) {
	tableOnly := `
| Rank | Ticker | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------|-------|----|----|-----|------|
| 1 | TSLA | BREAKOUT | $250.00–$255.00 | $240.00 | 4.0% | $275.00 | $300.00 | 2.5:1 | STANDARD |
`
	result := ParseEvaluationResponse(tableOnly)
	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 1 {
		t.Fatalf("expected 1 ticker, got %d", len(result.Tickers))
	}
	if result.Tickers[0].Ticker != "TSLA" {
		t.Errorf("expected TSLA, got %s", result.Tickers[0].Ticker)
	}
	assertFloat(t, "TSLA.EntryLow", result.Tickers[0].EntryLow, 250.00)
}

// ── DISQUALIFIER PARSER TESTS (R-06) ──────────────────────────────────────────

const sampleDisqualifiedBlockFormat = `
## ANALYSIS

**1. NVDA — NVIDIA Corporation**
- **Setup**: TIGHT BASE
- **Conviction**: DISQUALIFIED
- **Disqualifier reason**: Overextended — price > 25% above SMA50 with RSI > 80 and no institutional confirmation
- **Entry zone**: $142.50–$145.00
- **Stop**: $136.80, Risk 4.2%
- **T1 / T2**: $155.00 / $168.00
- **R/R**: 3.2:1
- **Size**: STANDARD (20%)

**2. AAPL — Apple Inc.**
- **Setup**: VCP
- **Conviction**: HIGH
- **Entry zone**: $198.00–$200.50
- **Stop**: $192.00, Risk 3.5%
- **T1 / T2**: $210.00 / $220.00
- **R/R**: 3.0:1
- **Size**: FULL (25%)

## SUMMARY TABLE
| Rank | Ticker | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------|-------|----|----|-----|------|
| 1 | NVDA | TIGHT BASE | $142.50–$145.00 | $136.80 | 4.2% | $155.00 | $168.00 | 3.2:1 | STANDARD |
| 2 | AAPL | VCP | $198.00–$200.50 | $192.00 | 3.5% | $210.00 | $220.00 | 3.0:1 | FULL |
`

const sampleDisqualifiedSummaryTable = `
## ANALYSIS

**1. AMD — Advanced Micro Devices**
- **Setup**: BREAKOUT
- **Conviction**: DISQUALIFIED
- **Entry zone**: $178.50–$180.00
- **Stop**: $172.00
- **T1 / T2**: $195.00 / $210.00
- **R/R**: 2.8:1
- **Size**: STANDARD

## SUMMARY TABLE
| Rank | Ticker | Setup | Conviction | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------------|------|-------|----|----|-----|------|
| 1 | AMD | BREAKOUT | DISQUALIFIED | $178.50–$180.00 | $172.00 | 3.6% | $195.00 | $210.00 | 2.8:1 | STANDARD |
`

const sampleDisqualifiedMissingReason = `
## ANALYSIS

**1. TSLA — Tesla Inc.**
- **Setup**: BREAKOUT
- **Conviction**: DISQUALIFIED
- **Entry zone**: $250.00–$255.00
- **Stop**: $240.00, Risk 4.0%
- **T1 / T2**: $275.00 / $300.00
- **R/R**: 2.5:1
- **Size**: STANDARD

## SUMMARY TABLE
| Rank | Ticker | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size |
|------|--------|-------|------------|------|-------|----|----|-----|------|
| 1 | TSLA | BREAKOUT | $250.00–$255.00 | $240.00 | 4.0% | $275.00 | $300.00 | 2.5:1 | STANDARD |
`

func TestParseEvaluationResponse_Disqualified_BlockFormat(t *testing.T) {
	result := ParseEvaluationResponse(sampleDisqualifiedBlockFormat)

	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 2 {
		t.Fatalf("expected 2 tickers, got %d", len(result.Tickers))
	}

	// NVDA should be disqualified.
	nvda := result.Tickers[0]
	if nvda.Ticker != "NVDA" {
		t.Errorf("expected NVDA, got %s", nvda.Ticker)
	}
	if !nvda.Disqualified {
		t.Error("expected NVDA to be disqualified")
	}
	if nvda.DisqualifierReason == "" {
		t.Error("expected NVDA to have a disqualifier reason")
	}
	if nvda.Conviction != "DISQUALIFIED" {
		t.Errorf("expected conviction DISQUALIFIED, got %s", nvda.Conviction)
	}

	// AAPL should NOT be disqualified.
	aapl := result.Tickers[1]
	if aapl.Ticker != "AAPL" {
		t.Errorf("expected AAPL, got %s", aapl.Ticker)
	}
	if aapl.Disqualified {
		t.Error("expected AAPL to NOT be disqualified")
	}
	if aapl.Conviction != "HIGH" {
		t.Errorf("expected conviction HIGH, got %s", aapl.Conviction)
	}
}

func TestParseEvaluationResponse_Disqualified_SummaryTable(t *testing.T) {
	result := ParseEvaluationResponse(sampleDisqualifiedSummaryTable)

	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 1 {
		t.Fatalf("expected 1 ticker, got %d", len(result.Tickers))
	}

	amd := result.Tickers[0]
	if amd.Ticker != "AMD" {
		t.Errorf("expected AMD, got %s", amd.Ticker)
	}
	if !amd.Disqualified {
		t.Error("expected AMD to be disqualified")
	}
	if amd.Conviction != "DISQUALIFIED" {
		t.Errorf("expected conviction DISQUALIFIED, got %s", amd.Conviction)
	}
}

func TestParseEvaluationResponse_Disqualified_MissingReason(t *testing.T) {
	result := ParseEvaluationResponse(sampleDisqualifiedMissingReason)

	// The parser should still succeed — missing disqualifier_reason is a validation
	// concern, not a parse failure. The Disqualified flag should be set.
	if !result.ParseSuccess {
		t.Fatalf("expected parse success, got errors: %v", result.ParseErrors)
	}
	if len(result.Tickers) != 1 {
		t.Fatalf("expected 1 ticker, got %d", len(result.Tickers))
	}

	tsla := result.Tickers[0]
	if tsla.Ticker != "TSLA" {
		t.Errorf("expected TSLA, got %s", tsla.Ticker)
	}
	if !tsla.Disqualified {
		t.Error("expected TSLA to be disqualified (conviction=DISQUALIFIED)")
	}
	if tsla.Conviction != "DISQUALIFIED" {
		t.Errorf("expected conviction DISQUALIFIED, got %s", tsla.Conviction)
	}
	// DisqualifierReason may be empty — that's a validation error, not a parse error.
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func floatPtr(v float64) *float64 { return &v }

func assertFloat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %v, want %v", name, *got, want)
	}
}

func assertFloatP(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s: got %v, want nil", name, *got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s: got nil, want %v", name, *want)
		return
	}
	if *got != *want {
		t.Errorf("%s: got %v, want %v", name, *got, *want)
	}
}
