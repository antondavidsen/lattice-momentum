package llm

import (
	"strings"
	"testing"
)

func TestLoadCommercialPrompt(t *testing.T) {
	tmpl, err := loadCommercialPrompt()
	if err != nil {
		t.Fatalf("loadCommercialPrompt() error: %v", err)
	}

	if tmpl.System == "" {
		t.Error("system prompt is empty")
	}
	if tmpl.User == "" {
		t.Error("user prompt is empty")
	}

	// System prompt should contain the translator persona.
	if !strings.Contains(tmpl.System, "translator") {
		t.Error("system prompt should contain 'translator'")
	}

	// User prompt should contain the placeholder.
	if !strings.Contains(tmpl.User, "[PASTE_MEMOS_HERE]") {
		t.Error("user prompt should contain [PASTE_MEMOS_HERE] placeholder")
	}

	// System prompt should contain the hard rules.
	if !strings.Contains(tmpl.System, "HARD RULES") {
		t.Error("system prompt should contain 'HARD RULES'")
	}
}

func TestParseCommercialResponse_ValidJSON(t *testing.T) {
	input := `{
		"headline": "Market Tailwinds Support New Breakout Candidates",
		"market_summary": "The market continues to show strength.",
		"sector_summary": "Technology leads. Energy lags.",
		"trade_cards": [
			{
				"ticker": "NVDA",
				"company_name": "NVIDIA Corporation",
				"conviction": "High",
				"why_it_made_the_list": "Strong earnings growth supports the trend.",
				"entry_zone": "$142.50–$145.00",
				"stop_loss": "$136.80",
				"target_1": "$155.00",
				"target_2": "$168.00",
				"position_size_guidance": "Standard",
				"earnings_note": "Next earnings: May 28",
				"base_type": "Flat base",
				"base_depth": "~8%",
				"volume_behavior": "Volume drying up (bullish)",
				"pivot_price": "$145.00",
				"extension_from_pivot": "Currently 2.1% below breakout price",
				"relative_volume": 1.85,
				"near_52w_high_pct": "93.2%",
				"perf_3m_6m": "12.4% / 28.7%",
				"institutional_interest": "Strong — elevated volume near highs suggests accumulation",
				"risk_reward_ratio": "3.2:1",
				"position_pct": "25%"
			}
		],
		"risk_note": "Watch earnings dates. Size accordingly.",
		"closing_summary": "Several strong setups today.",
		"full_report_markdown": "# Report\n\nContent here."
	}`

	result, err := parseCommercialResponse(input)
	if err != nil {
		t.Fatalf("parseCommercialResponse() error: %v", err)
	}

	if result.Headline != "Market Tailwinds Support New Breakout Candidates" {
		t.Errorf("headline = %q, want 'Market Tailwinds Support New Breakout Candidates'", result.Headline)
	}
	if len(result.TradeCards) != 1 {
		t.Fatalf("trade_cards count = %d, want 1", len(result.TradeCards))
	}
	card := result.TradeCards[0]
	if card.Ticker != "NVDA" {
		t.Errorf("trade_cards[0].ticker = %q, want 'NVDA'", card.Ticker)
	}
	if card.EntryZone != "$142.50–$145.00" {
		t.Errorf("trade_cards[0].entry_zone = %q, want '$142.50–$145.00'", card.EntryZone)
	}
	if card.StopLoss != "$136.80" {
		t.Errorf("trade_cards[0].stop_loss = %q, want '$136.80'", card.StopLoss)
	}
	// Price structure fields
	if card.BaseType != "Flat base" {
		t.Errorf("trade_cards[0].base_type = %q, want 'Flat base'", card.BaseType)
	}
	if card.BaseDepth != "~8%" {
		t.Errorf("trade_cards[0].base_depth = %q, want '~8%%'", card.BaseDepth)
	}
	if card.VolumeBehavior != "Volume drying up (bullish)" {
		t.Errorf("trade_cards[0].volume_behavior = %q, want 'Volume drying up (bullish)'", card.VolumeBehavior)
	}
	if card.PivotPrice != "$145.00" {
		t.Errorf("trade_cards[0].pivot_price = %q, want '$145.00'", card.PivotPrice)
	}
	if card.ExtensionFromPivot != "Currently 2.1% below breakout price" {
		t.Errorf("trade_cards[0].extension_from_pivot = %q, want 'Currently 2.1%% below breakout price'", card.ExtensionFromPivot)
	}
	// Institutional footprint fields
	if card.RelativeVolume != 1.85 {
		t.Errorf("trade_cards[0].relative_volume = %v, want 1.85", card.RelativeVolume)
	}
	if card.Near52WHighPct != "93.2%" {
		t.Errorf("trade_cards[0].near_52w_high_pct = %q, want '93.2%%'", card.Near52WHighPct)
	}
	if card.Perf3M6M != "12.4% / 28.7%" {
		t.Errorf("trade_cards[0].perf_3m_6m = %q, want '12.4%% / 28.7%%'", card.Perf3M6M)
	}
	if card.InstitutionalInterest != "Strong — elevated volume near highs suggests accumulation" {
		t.Errorf("trade_cards[0].institutional_interest = %q, want 'Strong — elevated volume near highs suggests accumulation'", card.InstitutionalInterest)
	}
	// Risk/reward & sizing fields
	if card.RiskRewardRatio != "3.2:1" {
		t.Errorf("trade_cards[0].risk_reward_ratio = %q, want '3.2:1'", card.RiskRewardRatio)
	}
	if card.PositionPct != "25%" {
		t.Errorf("trade_cards[0].position_pct = %q, want '25%%'", card.PositionPct)
	}
}

func TestParseCommercialResponse_WithMarkdownFences(t *testing.T) {
	input := "```json\n" + `{"headline":"Test","market_summary":"","sector_summary":"","trade_cards":[],"risk_note":"","closing_summary":"","full_report_markdown":""}` + "\n```"

	result, err := parseCommercialResponse(input)
	if err != nil {
		t.Fatalf("parseCommercialResponse() with fences error: %v", err)
	}
	if result.Headline != "Test" {
		t.Errorf("headline = %q, want 'Test'", result.Headline)
	}
}

func TestParseCommercialResponse_MissingHeadline(t *testing.T) {
	input := `{"headline":"","market_summary":"","sector_summary":"","trade_cards":[],"risk_note":"","closing_summary":"","full_report_markdown":""}`

	_, err := parseCommercialResponse(input)
	if err == nil {
		t.Error("expected error for empty headline, got nil")
	}
}

func TestStripJSONFences(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"  ```json\n{\"a\":1}\n```  ", `{"a":1}`},
	}
	for _, tt := range tests {
		got := stripJSONFences(tt.input)
		if got != tt.want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
