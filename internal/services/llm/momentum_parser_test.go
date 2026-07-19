package llm

import (
	"strings"
	"testing"
)

// ── Test fixtures ─────────────────────────────────────────────────────────────

var testIDToTicker = map[string]string{
	"STOCK_A": "FIGS",
	"STOCK_B": "AEHR",
	"STOCK_C": "ONON",
	"STOCK_D": "CELH",
	"STOCK_E": "DUOL",
}

const validJSONBlock = "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 5,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "TIGHT_BASE",
      "accumulation": "ACCUMULATION",
      "rs_composite": 8.2,
      "entry_zone_low": 15.10,
      "entry_zone_high": 15.50,
      "stop": 14.65,
      "stop_anchor": "2026-04-23 Low",
      "risk_pct": 3.0,
      "target_1": 16.80,
      "target_1_anchor": "52W high 16.80",
      "target_2": 18.50,
      "rr_ratio": 5.7,
      "size": "FULL",
      "size_reason": "tight base near highs in leading sector",
      "hold_days": 2,
      "earnings_date": "2026-05-15",
      "earnings_overlap": false,
      "confirmation_price": 15.55,
      "confirmation_volume": 2500000,
      "failure_price": 14.60,
      "conviction": "HIGH",
      "extension_pct": 3.2,
      "adr_pct": 4.5,
      "base_rate_win_pct": 68.0,
      "base_rate_sample_size": 47,
      "base_rate_source": "TIGHT_BASE"
    },
    {
      "rank": 2,
      "stock_id": "STOCK_B",
      "setup": "VCP",
      "accumulation": "NEUTRAL",
      "rs_composite": 7.1,
      "entry_zone_low": 22.30,
      "entry_zone_high": 22.80,
      "stop": 21.50,
      "stop_anchor": "2026-04-21 Low",
      "risk_pct": 3.6,
      "target_1": 24.90,
      "target_1_anchor": "prior resistance candle 2026-04-16 high",
      "target_2": 27.00,
      "rr_ratio": 4.1,
      "size": "STANDARD",
      "size_reason": "good setup but neutral accumulation",
      "hold_days": 3,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 22.85,
      "confirmation_volume": 1800000,
      "failure_price": 21.40,
      "conviction": "MEDIUM",
      "extension_pct": 5.1,
      "adr_pct": 3.8,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    },
    {
      "rank": 3,
      "stock_id": "STOCK_C",
      "setup": "PULLBACK",
      "accumulation": "ACCUMULATION",
      "rs_composite": 6.8,
      "entry_zone_low": 48.00,
      "entry_zone_high": 49.00,
      "stop": 46.50,
      "stop_anchor": "2026-04-22 Low",
      "risk_pct": 3.1,
      "target_1": 52.00,
      "target_1_anchor": "measured move from gap",
      "target_2": 55.00,
      "rr_ratio": 3.5,
      "size": "STANDARD",
      "size_reason": "solid RS rank with accumulation",
      "hold_days": 2,
      "earnings_date": "2026-05-20",
      "earnings_overlap": false,
      "confirmation_price": 49.10,
      "confirmation_volume": 3000000,
      "failure_price": 46.00,
      "conviction": "MEDIUM",
      "extension_pct": 4.0,
      "adr_pct": 5.2,
      "base_rate_win_pct": 55.0,
      "base_rate_sample_size": 32,
      "base_rate_source": "PULLBACK"
    }
  ],
  "eliminations": [
    {
      "stock_id": "STOCK_D",
      "step": 1,
      "rule": "SMA50 < SMA150 or SMA150 < SMA200",
      "value": "SMA50=32.10 < SMA150=34.80"
    },
    {
      "stock_id": "STOCK_E",
      "step": 5,
      "rule": "risk_pct > 8.0",
      "value": "risk_pct=9.2"
    }
  ]
}` + "\n```\n\n### STEP 1 — STAGE 2 VERIFICATION\n\nFIGS (STOCK_A): passes all checks...\n"

const noJSONResponse = `### STEP 1 — STAGE 2 VERIFICATION

| Rank | ID | Setup | Entry zone | Stop | Risk% | T1 | T2 | R/R | Size | Earnings risk | Accum/Distrib |
|------|--------|-------|------------|------|-------|----|----|-----|------|---------------|---------------|
| 1 | STOCK_A | VCP | 15.10–15.50 | 14.65 | 3.0% | 16.80 | 18.50 | 5.7:1 | FULL | None | Accum |
`

const highRiskJSON = "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 2,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "TIGHT_BASE",
      "accumulation": "ACCUMULATION",
      "rs_composite": 8.2,
      "entry_zone_low": 15.10,
      "entry_zone_high": 15.50,
      "stop": 14.65,
      "stop_anchor": "2026-04-23 Low",
      "risk_pct": 12.5,
      "target_1": 16.80,
      "target_1_anchor": "52W high",
      "target_2": 18.50,
      "rr_ratio": 5.7,
      "size": "FULL",
      "size_reason": "tight base",
      "hold_days": 2,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 15.55,
      "confirmation_volume": 2500000,
      "failure_price": 14.60,
      "conviction": "HIGH",
      "extension_pct": 3.2,
      "adr_pct": 4.5,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    }
  ],
  "eliminations": []
}` + "\n```"

const unknownIDJSON = "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 1,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_Z",
      "setup": "VCP",
      "accumulation": "NEUTRAL",
      "rs_composite": 7.0,
      "entry_zone_low": 30.00,
      "entry_zone_high": 31.00,
      "stop": 28.50,
      "stop_anchor": "2026-04-22 Low",
      "risk_pct": 5.0,
      "target_1": 35.00,
      "target_1_anchor": "52W high",
      "target_2": 38.00,
      "rr_ratio": 3.3,
      "size": "STANDARD",
      "size_reason": "good setup",
      "hold_days": 2,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 31.10,
      "confirmation_volume": 1500000,
      "failure_price": 28.00,
      "conviction": "MEDIUM",
      "extension_pct": 4.0,
      "adr_pct": 3.5,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    }
  ],
  "eliminations": []
}` + "\n```"

const emptyStopAnchorJSON = "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 1,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "VCP",
      "accumulation": "NEUTRAL",
      "rs_composite": 7.0,
      "entry_zone_low": 15.00,
      "entry_zone_high": 15.50,
      "stop": 14.50,
      "stop_anchor": "",
      "risk_pct": 3.3,
      "target_1": 17.00,
      "target_1_anchor": "52W high",
      "target_2": 18.00,
      "rr_ratio": 4.0,
      "size": "STANDARD",
      "size_reason": "decent setup",
      "hold_days": 2,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 15.55,
      "confirmation_volume": 1000000,
      "failure_price": 14.40,
      "conviction": "MEDIUM",
      "extension_pct": 3.0,
      "adr_pct": 4.0,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    }
  ],
  "eliminations": []
}` + "\n```"

const truncatedJSON = "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 1,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "VCP"`

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestParseMomentumResponse_HappyPath(t *testing.T) {
	result := ParseMomentumResponse(validJSONBlock, testIDToTicker, "")

	if !result.ParseSuccess {
		t.Fatalf("expected parse_success=true, got errors: %v", result.ParseErrors)
	}
	if result.ParserVersion != "json-v1" {
		t.Errorf("expected parser version json-v1, got %s", result.ParserVersion)
	}
	if len(result.Tickers) != 3 {
		t.Fatalf("expected 3 tickers, got %d", len(result.Tickers))
	}
	if result.Tickers[0] != "FIGS" {
		t.Errorf("expected first ticker FIGS, got %s", result.Tickers[0])
	}
	if result.Tickers[1] != "AEHR" {
		t.Errorf("expected second ticker AEHR, got %s", result.Tickers[1])
	}
	if result.Tickers[2] != "ONON" {
		t.Errorf("expected third ticker ONON, got %s", result.Tickers[2])
	}

	// Verify selections.
	if len(result.Selections) != 3 {
		t.Fatalf("expected 3 selections, got %d", len(result.Selections))
	}
	sel0 := result.Selections[0]
	if sel0.Ticker != "FIGS" {
		t.Errorf("expected resolved ticker FIGS, got %s", sel0.Ticker)
	}
	if sel0.Stop != 14.65 {
		t.Errorf("expected stop 14.65, got %f", sel0.Stop)
	}
	if sel0.RiskPct > 10.0 {
		t.Errorf("risk_pct should be <= 10, got %f", sel0.RiskPct)
	}

	// Verify eliminations.
	if len(result.Eliminations) != 2 {
		t.Fatalf("expected 2 eliminations, got %d", len(result.Eliminations))
	}
	if result.Eliminations[0].Ticker != "CELH" {
		t.Errorf("expected elimination ticker CELH, got %s", result.Eliminations[0].Ticker)
	}

	if result.RawJSON == "" {
		t.Error("expected non-empty RawJSON")
	}
}

func TestParseMomentumResponse_JSONBlockMissing(t *testing.T) {
	result := ParseMomentumResponse(noJSONResponse, testIDToTicker, "")

	if result.ParseSuccess {
		t.Error("expected parse_success=false for missing JSON block")
	}
	found := false
	for _, e := range result.ParseErrors {
		if e == "json_block_not_found" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'json_block_not_found' in ParseErrors, got: %v", result.ParseErrors)
	}
}

func TestParseMomentumResponse_RiskValidation(t *testing.T) {
	result := ParseMomentumResponse(highRiskJSON, testIDToTicker, "")

	if !result.ParseSuccess {
		t.Fatalf("expected parse_success=true (selection valid despite high risk), errors: %v", result.ParseErrors)
	}
	if len(result.Selections) != 1 {
		t.Fatalf("expected 1 selection, got %d", len(result.Selections))
	}
	if result.Selections[0].ValidationWarning != "risk_pct exceeds 10" {
		t.Errorf("expected validation_warning 'risk_pct exceeds 10', got %q", result.Selections[0].ValidationWarning)
	}
}

func TestParseMomentumResponse_UnknownStockID(t *testing.T) {
	result := ParseMomentumResponse(unknownIDJSON, testIDToTicker, "")

	if result.ParseSuccess {
		t.Error("expected parse_success=false for unknown stock_id")
	}
	found := false
	for _, e := range result.ParseErrors {
		if contains(e, "STOCK_Z") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ParseErrors to contain unknown stock_id STOCK_Z, got: %v", result.ParseErrors)
	}
}

func TestParseMomentumResponse_EmptyStopAnchor(t *testing.T) {
	result := ParseMomentumResponse(emptyStopAnchorJSON, testIDToTicker, "")

	if result.ParseSuccess {
		t.Error("expected parse_success=false for empty stop_anchor")
	}
	found := false
	for _, e := range result.ParseErrors {
		if contains(e, "empty stop_anchor") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ParseErrors to contain 'empty stop_anchor', got: %v", result.ParseErrors)
	}
}

func TestParseMomentumResponse_TruncatedJSON(t *testing.T) {
	result := ParseMomentumResponse(truncatedJSON, testIDToTicker, "")

	if result.ParseSuccess {
		t.Error("expected parse_success=false for truncated JSON")
	}
	found := false
	for _, e := range result.ParseErrors {
		if contains(e, "json_unmarshal_failed") || contains(e, "json_block_not_found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'json_unmarshal_failed' or 'json_block_not_found' in ParseErrors, got: %v", result.ParseErrors)
	}
}

func TestParseMomentumResponse_IDResolution(t *testing.T) {
	idMap := map[string]string{
		"STOCK_A": "FIGS",
		"STOCK_C": "AEHR",
	}

	// Build a minimal JSON with STOCK_A and STOCK_C.
	input := "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 2,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "TIGHT_BASE",
      "accumulation": "ACCUMULATION",
      "rs_composite": 8.0,
      "entry_zone_low": 15.00,
      "entry_zone_high": 15.50,
      "stop": 14.50,
      "stop_anchor": "2026-04-23 Low",
      "risk_pct": 3.3,
      "target_1": 17.00,
      "target_1_anchor": "52W high",
      "target_2": 18.00,
      "rr_ratio": 4.0,
      "size": "FULL",
      "size_reason": "tight base in leader",
      "hold_days": 2,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 15.55,
      "confirmation_volume": 2000000,
      "failure_price": 14.40,
      "conviction": "HIGH",
      "extension_pct": 3.0,
      "adr_pct": 4.5,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    },
    {
      "rank": 2,
      "stock_id": "STOCK_C",
      "setup": "VCP",
      "accumulation": "NEUTRAL",
      "rs_composite": 7.0,
      "entry_zone_low": 22.00,
      "entry_zone_high": 22.50,
      "stop": 21.20,
      "stop_anchor": "2026-04-21 Low",
      "risk_pct": 3.6,
      "target_1": 24.00,
      "target_1_anchor": "prior resistance",
      "target_2": 26.00,
      "rr_ratio": 3.5,
      "size": "STANDARD",
      "size_reason": "good VCP setup",
      "hold_days": 3,
      "earnings_date": null,
      "earnings_overlap": false,
      "confirmation_price": 22.55,
      "confirmation_volume": 1500000,
      "failure_price": 21.00,
      "conviction": "MEDIUM",
      "extension_pct": 5.0,
      "adr_pct": 3.8,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    }
  ],
  "eliminations": []
}` + "\n```"

	result := ParseMomentumResponse(input, idMap, "")

	if !result.ParseSuccess {
		t.Fatalf("expected parse_success=true, got errors: %v", result.ParseErrors)
	}
	if len(result.Selections) != 2 {
		t.Fatalf("expected 2 selections, got %d", len(result.Selections))
	}
	if result.Selections[0].Ticker != "FIGS" {
		t.Errorf("expected FIGS, got %s", result.Selections[0].Ticker)
	}
	if result.Selections[1].Ticker != "AEHR" {
		t.Errorf("expected AEHR, got %s", result.Selections[1].Ticker)
	}
	if len(result.Tickers) != 2 {
		t.Fatalf("expected 2 tickers, got %d", len(result.Tickers))
	}
	if result.Tickers[0] != "FIGS" || result.Tickers[1] != "AEHR" {
		t.Errorf("expected tickers [FIGS, AEHR], got %v", result.Tickers)
	}
}

func TestParseMomentumResponse_EPStyleJSON(t *testing.T) {
	// EP prompts produce the same JSON schema — verify parser handles EP-style setups.
	input := "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 2,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "EP_TIER1",
      "accumulation": "ACCUMULATION",
      "rs_composite": 9.0,
      "entry_zone_low": 198.00,
      "entry_zone_high": 202.00,
      "stop": 192.00,
      "stop_anchor": "2026-04-25 Low",
      "risk_pct": 3.5,
      "target_1": 220.00,
      "target_1_anchor": "measured move from gap",
      "target_2": 240.00,
      "rr_ratio": 5.1,
      "size": "FULL",
      "size_reason": "TIER 1 earnings catalyst, strong close vs range",
      "hold_days": 1,
      "earnings_date": "2026-04-25",
      "earnings_overlap": false,
      "confirmation_price": 203.00,
      "confirmation_volume": 15000000,
      "failure_price": 191.00,
      "conviction": "HIGH",
      "extension_pct": 2.1,
      "adr_pct": 3.8,
      "base_rate_win_pct": 72.0,
      "base_rate_sample_size": 83,
      "base_rate_source": "EP_TIER1"
    }
  ],
  "eliminations": [
    {
      "stock_id": "STOCK_B",
      "step": 1,
      "rule": "Relative volume < 3x",
      "value": "RelVol=1.8x"
    }
  ]
}` + "\n```\n\nStep 1 prose analysis follows..."

	idMap := map[string]string{
		"STOCK_A": "AAPL",
		"STOCK_B": "MSFT",
	}

	result := ParseMomentumResponse(input, idMap, "")
	if !result.ParseSuccess {
		t.Fatalf("expected parse_success=true for EP-style JSON, errors: %v", result.ParseErrors)
	}
	if len(result.Selections) != 1 {
		t.Fatalf("expected 1 selection, got %d", len(result.Selections))
	}
	if result.Selections[0].Ticker != "AAPL" {
		t.Errorf("expected AAPL, got %s", result.Selections[0].Ticker)
	}
	if result.Selections[0].Setup != "EP_TIER1" {
		t.Errorf("expected EP_TIER1 setup, got %s", result.Selections[0].Setup)
	}
	if len(result.Eliminations) != 1 {
		t.Fatalf("expected 1 elimination, got %d", len(result.Eliminations))
	}
	if result.Eliminations[0].Ticker != "MSFT" {
		t.Errorf("expected elimination ticker MSFT, got %s", result.Eliminations[0].Ticker)
	}
}

func TestParseMomentumResponse_LeadersStyleJSON(t *testing.T) {
	// Leaders prompts use the same JSON schema with leader-specific setup types.
	input := "```json\n" + `{
  "analysis_date": "2026-04-26",
  "regime": "STRONG_BULL",
  "candidate_count": 3,
  "selections": [
    {
      "rank": 1,
      "stock_id": "STOCK_A",
      "setup": "TIGHT_BASE",
      "accumulation": "ACCUMULATION",
      "rs_composite": 8.5,
      "entry_zone_low": 850.00,
      "entry_zone_high": 860.00,
      "stop": 810.00,
      "stop_anchor": "2026-04-20 Low",
      "risk_pct": 5.3,
      "target_1": 920.00,
      "target_1_anchor": "52W high 923.45",
      "target_2": 1000.00,
      "rr_ratio": 3.2,
      "size": "FULL",
      "size_reason": "#1 in LEADING sector, revenue-driven growth",
      "hold_weeks": 8,
      "earnings_date": "2026-06-15",
      "earnings_overlap": false,
      "confirmation_price": 865.00,
      "confirmation_volume": 5000000,
      "failure_price": 805.00,
      "conviction": "HIGH",
      "extension_pct": 1.5,
      "adr_pct": 2.8,
      "base_rate_win_pct": null,
      "base_rate_sample_size": null,
      "base_rate_source": null
    }
  ],
  "eliminations": [
    {
      "stock_id": "STOCK_B",
      "step": 1,
      "rule": "EPS growth YoY < 20%",
      "value": "EPS growth=12.3%"
    },
    {
      "stock_id": "STOCK_C",
      "step": 2,
      "rule": "ALREADY BROKE OUT: RSI > 75, Perf3M > 30%",
      "value": "RSI=82, Perf3M=38%"
    }
  ]
}` + "\n```\n\nProse analysis..."

	idMap := map[string]string{
		"STOCK_A": "COST",
		"STOCK_B": "PG",
		"STOCK_C": "NFLX",
	}

	result := ParseMomentumResponse(input, idMap, "")
	if !result.ParseSuccess {
		t.Fatalf("expected parse_success=true for Leaders-style JSON, errors: %v", result.ParseErrors)
	}
	if result.Tickers[0] != "COST" {
		t.Errorf("expected COST, got %s", result.Tickers[0])
	}
	if result.Selections[0].HoldWeeks != 8 {
		t.Errorf("expected hold_weeks=8 (position trade), got %d", result.Selections[0].HoldWeeks)
	}
	if len(result.Eliminations) != 2 {
		t.Fatalf("expected 2 eliminations, got %d", len(result.Eliminations))
	}
	if result.Eliminations[0].Ticker != "PG" {
		t.Errorf("expected elimination ticker PG, got %s", result.Eliminations[0].Ticker)
	}
}

func TestExtractJSONBlock_FallbackStrategy(t *testing.T) {
	// Test Strategy 2: raw JSON without fenced code block.
	input := `## MANDATORY OUTPUT CONTRACT

{
  "analysis_date": "2026-04-26",
  "regime": "BULL",
  "candidate_count": 0,
  "selections": [],
  "eliminations": []
}

### STEP 1 — prose follows`

	raw := extractJSONBlock(input)
	if raw == "" {
		t.Fatal("expected extractJSONBlock to find raw JSON via Strategy 2")
	}
	if !strings.Contains(raw, `"analysis_date"`) {
		t.Errorf("extracted JSON missing expected content: %s", raw)
	}
}

func TestExtractJSONBlock_NoJSON(t *testing.T) {
	input := "Just plain text with no JSON at all. No braces anywhere."
	raw := extractJSONBlock(input)
	if raw != "" {
		t.Errorf("expected empty string for no-JSON input, got: %s", raw)
	}
}

// contains checks if s contains substr (helper for test assertions).
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
