package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-stock-service/internal/models"
)

// ── Parser version ────────────────────────────────────────────────────────────

const momentumParserVersion = "json-v1"

// ── JSON schema types ─────────────────────────────────────────────────────────

// MomentumAnalysisJSON is the top-level structure extracted from the LLM JSON block.
type MomentumAnalysisJSON struct {
	AnalysisDate   string                    `json:"analysis_date"`
	Regime         string                    `json:"regime"`
	CandidateCount int                       `json:"candidate_count"`
	Selections     []MomentumSelectionJSON   `json:"selections"`
	Eliminations   []MomentumEliminationJSON `json:"eliminations"`
}

// MomentumSelectionJSON represents one selected ticker from the LLM JSON block.
type MomentumSelectionJSON struct {
	Rank               int      `json:"rank"`
	StockID            string   `json:"stock_id"`
	Setup              string   `json:"setup"`
	Accumulation       string   `json:"accumulation"`
	RSComposite        float64  `json:"rs_composite"`
	EntryZoneLow       float64  `json:"entry_zone_low"`
	EntryZoneHigh      float64  `json:"entry_zone_high"`
	Stop               float64  `json:"stop"`
	StopAnchor         string   `json:"stop_anchor"`
	RiskPct            float64  `json:"risk_pct"`
	Target1            float64  `json:"target_1"`
	Target1Anchor      string   `json:"target_1_anchor"`
	Target2            float64  `json:"target_2"`
	RRRatio            float64  `json:"rr_ratio"`
	Size               string   `json:"size"`
	SizeReason         string   `json:"size_reason"`
	HoldWeeks          int      `json:"hold_weeks"`
	HoldDays           int      `json:"hold_days,omitempty"`
	EarningsDate       *string  `json:"earnings_date"`
	EarningsOverlap    *bool    `json:"earnings_overlap"`
	ConfirmationPrice  float64  `json:"confirmation_price"`
	ConfirmationVolume int64    `json:"confirmation_volume"`
	FailurePrice       float64  `json:"failure_price"`
	Conviction         string   `json:"conviction"`
	ExtensionPct       float64  `json:"extension_pct"`
	ADRPct             float64  `json:"adr_pct"`
	BaseRateWinPct     *float64 `json:"base_rate_win_pct"`
	BaseRateSampleSize *int     `json:"base_rate_sample_size"`
	BaseRateSource     *string  `json:"base_rate_source"`
}

// MomentumEliminationJSON represents one eliminated ticker from the LLM JSON block.
type MomentumEliminationJSON struct {
	StockID string `json:"stock_id"`
	Step    int    `json:"step"`
	Rule    string `json:"rule"`
	Value   string `json:"value"`
}

// ── Parse result types ────────────────────────────────────────────────────────

// MomentumParseResult holds the full parsed output.
type MomentumParseResult struct {
	ParseSuccess  bool
	ParseErrors   []string
	ParserVersion string
	ParsedAt      time.Time
	Tickers       []string
	Selections    []ResolvedSelection
	Eliminations  []ResolvedElimination
	RawJSON       string
}

// ResolvedSelection embeds the JSON selection with its resolved real ticker.
type ResolvedSelection struct {
	MomentumSelectionJSON
	Ticker            string `json:"ticker"`
	ValidationWarning string `json:"validation_warning,omitempty"`
}

// ResolvedElimination embeds the JSON elimination with its resolved real ticker.
type ResolvedElimination struct {
	MomentumEliminationJSON
	Ticker string `json:"ticker"`
}

// ── Parser ────────────────────────────────────────────────────────────────────

// ParseMomentumResponse extracts the mandatory JSON block from an LLM response,
// resolves anonymous stock IDs to real tickers, and validates each selection.
func ParseMomentumResponse(response string, idToTicker map[string]string, parserVersion string) MomentumParseResult {
	if parserVersion == "" {
		parserVersion = momentumParserVersion
	}

	result := MomentumParseResult{
		ParserVersion: parserVersion,
		ParsedAt:      time.Now().UTC(),
	}

	// Step 1: Extract JSON block.
	rawJSON := extractJSONBlock(response)
	if rawJSON == "" {
		result.ParseErrors = append(result.ParseErrors, "json_block_not_found")
		return result
	}
	result.RawJSON = rawJSON

	// Step 2: Unmarshal.
	var analysis MomentumAnalysisJSON
	if err := json.Unmarshal([]byte(rawJSON), &analysis); err != nil {
		result.ParseErrors = append(result.ParseErrors, fmt.Sprintf("json_unmarshal_failed: %s", err.Error()))
		return result
	}

	// Step 3: Resolve and validate selections.
	var tickers []string
	for _, sel := range analysis.Selections { //nolint:gocritic // each iteration copies 296 bytes; small slice (≤10)
		resolved := ResolvedSelection{
			MomentumSelectionJSON: sel,
		}

		// Resolve stock ID to real ticker.
		ticker, ok := idToTicker[sel.StockID]
		if !ok {
			result.ParseErrors = append(result.ParseErrors,
				fmt.Sprintf("unknown stock_id %q not in idToTicker map", sel.StockID))
			continue
		}
		resolved.Ticker = ticker

		// Validate: stop and entry must be positive.
		if sel.Stop <= 0 || sel.EntryZoneLow <= 0 {
			result.ParseErrors = append(result.ParseErrors,
				fmt.Sprintf("invalid stop (%.2f) or entry_zone_low (%.2f) for %s", sel.Stop, sel.EntryZoneLow, sel.StockID))
			continue
		}

		// Validate: stop_anchor must not be empty.
		if strings.TrimSpace(sel.StopAnchor) == "" {
			result.ParseErrors = append(result.ParseErrors,
				fmt.Sprintf("empty stop_anchor for %s", sel.StockID))
			continue
		}

		// Soft validations (warnings, not rejections).
		if sel.RiskPct > 10.0 {
			resolved.ValidationWarning = "risk_pct exceeds 10"
		}
		// RRRatio < 2.5 is a warning only — no rejection, no annotation needed per spec beyond logging.

		result.Selections = append(result.Selections, resolved)
		tickers = append(tickers, ticker)
	}

	// Step 4: Resolve eliminations.
	for _, elim := range analysis.Eliminations {
		resolved := ResolvedElimination{
			MomentumEliminationJSON: elim,
		}
		if ticker, ok := idToTicker[elim.StockID]; ok {
			resolved.Ticker = ticker
		}
		result.Eliminations = append(result.Eliminations, resolved)
	}

	result.Tickers = tickers
	result.ParseSuccess = len(result.Selections) > 0
	return result
}

// extractJSONBlock finds the JSON block in the LLM response.
// Strategy 1: look for ```json ... ```
// Strategy 2: look for raw { after "MANDATORY OUTPUT CONTRACT"
func extractJSONBlock(response string) string {
	// Strategy 1: fenced code block.
	const jsonFenceStart = "```json"
	const fenceEnd = "```"

	startIdx := strings.Index(response, jsonFenceStart)
	if startIdx >= 0 {
		contentStart := startIdx + len(jsonFenceStart)
		rest := response[contentStart:]
		endIdx := strings.Index(rest, fenceEnd)
		if endIdx >= 0 {
			return strings.TrimSpace(rest[:endIdx])
		}
		// Truncated: fence opened but never closed.
		return ""
	}

	// Strategy 2: raw JSON after section header.
	const sectionHeader = "MANDATORY OUTPUT CONTRACT"
	headerIdx := strings.Index(response, sectionHeader)
	searchFrom := 0
	if headerIdx >= 0 {
		searchFrom = headerIdx
	}

	braceIdx := strings.Index(response[searchFrom:], "{")
	if braceIdx < 0 {
		return ""
	}
	braceIdx += searchFrom

	// Find the matching closing brace by counting nesting depth.
	depth := 0
	for i := braceIdx; i < len(response); i++ {
		switch response[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(response[braceIdx : i+1])
			}
		}
	}

	// Unmatched braces — truncated response.
	return ""
}

// momentumResultToEvaluation converts a MomentumParseResult into the standard
// EvaluationParsedOutput format so the pipeline can persist it uniformly.
func momentumResultToEvaluation(r MomentumParseResult) *models.EvaluationParsedOutput { //nolint:gocritic // 160-byte param; owned by caller, pointer would complicate lifecycle
	out := &models.EvaluationParsedOutput{
		ParsedAt:      r.ParsedAt.Format(time.RFC3339),
		ParserVersion: r.ParserVersion,
		ParseSuccess:  r.ParseSuccess,
		ParseErrors:   r.ParseErrors,
	}

	for _, sel := range r.Selections { //nolint:gocritic // each iteration copies 328 bytes; small slice (≤10)
		entryLow := sel.EntryZoneLow
		entryHigh := sel.EntryZoneHigh
		stop := sel.Stop
		t1 := sel.Target1
		t2 := sel.Target2
		riskPct := sel.RiskPct
		rr := sel.RRRatio

		holdPeriod := fmt.Sprintf("%d weeks", sel.HoldWeeks)
		if sel.HoldDays > 0 {
			holdPeriod = fmt.Sprintf("%d days", sel.HoldDays)
		}

		t := models.EvaluationParsedTicker{
			Ticker:       sel.Ticker,
			Rank:         sel.Rank,
			Setup:        sel.Setup,
			Conviction:   sel.Conviction,
			EntryLow:     &entryLow,
			EntryHigh:    &entryHigh,
			StopPrice:    &stop,
			Target1:      &t1,
			Target2:      &t2,
			RiskPct:      &riskPct,
			RiskReward:   &rr,
			PositionSize: sel.Size,
			HoldPeriod:   holdPeriod,
			AccumDistrib: sel.Accumulation,
		}
		out.Tickers = append(out.Tickers, t)
	}

	return out
}
