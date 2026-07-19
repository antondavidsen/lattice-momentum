package llm

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"ai-stock-service/internal/models"
)

const parserVersion = "regex-v1"

// ParseEvaluationResponse extracts structured ticker recommendations from an LLM evaluation response.
func ParseEvaluationResponse(rawResponse string) *models.EvaluationParsedOutput {
	out := &models.EvaluationParsedOutput{
		ParsedAt:      time.Now().UTC().Format(time.RFC3339),
		ParserVersion: parserVersion,
	}

	if strings.TrimSpace(rawResponse) == "" {
		out.ParseErrors = append(out.ParseErrors, "empty response")
		return out
	}

	// Primary: parse the summary table.
	tableTickers := parseSummaryTable(rawResponse)

	// Fallback: parse the detailed blocks.
	blockTickers := parseDetailedBlocks(rawResponse)

	// Merge: prefer block data (more detailed) when both available, fall back to table.
	merged := mergeParsed(blockTickers, tableTickers)

	if len(merged) == 0 {
		out.ParseErrors = append(out.ParseErrors, "no tickers parsed from response")
		return out
	}

	out.Tickers = merged
	out.ParseSuccess = true
	return out
}

// ── Summary table parser ──────────────────────────────────────────────────────

var (
	// Matches a markdown table row: | val | val | val | ...
	tableRowRe = regexp.MustCompile(`^\|(.+)\|$`)
	// Matches separator row: |---|---|
	tableSepRe = regexp.MustCompile(`^\|[\s\-:]+\|`)
)

// parseSummaryTable extracts tickers from the final markdown summary table.
func parseSummaryTable(rawResponse string) []models.EvaluationParsedTicker {
	lines := strings.Split(rawResponse, "\n")

	// Find the LAST table in the response (the summary table).
	var tableStart, tableEnd int
	inTable := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if tableRowRe.MatchString(trimmed) || tableSepRe.MatchString(trimmed) {
			if !inTable {
				tableStart = i
				inTable = true
			}
			tableEnd = i
		} else if inTable && trimmed != "" {
			// End of a table block. If we find another table later, we'll overwrite.
			inTable = false
		}
	}

	if tableEnd <= tableStart+1 {
		return nil
	}

	// Parse rows (skip header and separator).
	var tickers []models.EvaluationParsedTicker
	headerParsed := false
	var colMap map[string]int

	for i := tableStart; i <= tableEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if tableSepRe.MatchString(trimmed) {
			continue
		}
		if !tableRowRe.MatchString(trimmed) {
			continue
		}

		cells := splitTableRow(trimmed)

		if !headerParsed {
			colMap = buildColumnMap(cells)
			headerParsed = true
			continue
		}

		if colMap == nil {
			continue
		}

		t := parseTableRowTicker(cells, colMap)
		if t.Ticker != "" {
			tickers = append(tickers, t)
		}
	}

	return tickers
}

func splitTableRow(row string) []string {
	// Remove leading/trailing pipes.
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

func buildColumnMap(headers []string) map[string]int {
	m := make(map[string]int, len(headers))
	for i, h := range headers {
		key := strings.ToLower(strings.TrimSpace(h))
		// Normalize common variations.
		key = strings.ReplaceAll(key, " ", "_")
		key = strings.ReplaceAll(key, "%", "pct")
		m[key] = i
	}
	return m
}

func parseTableRowTicker(cells []string, colMap map[string]int) models.EvaluationParsedTicker {
	get := func(keys ...string) string {
		for _, k := range keys {
			if idx, ok := colMap[k]; ok && idx < len(cells) {
				return strings.TrimSpace(cells[idx])
			}
		}
		return ""
	}

	ticker := get("ticker", "id", "stock_id", "symbol")
	if ticker == "" {
		return models.EvaluationParsedTicker{}
	}
	// Clean ticker: remove any ** markdown bold, # headers, whitespace.
	ticker = strings.ReplaceAll(ticker, "**", "")
	ticker = strings.ReplaceAll(ticker, "#", "")
	ticker = strings.TrimSpace(ticker)
	// Skip rows where the "ticker" is clearly not a ticker (e.g. numeric rank in ID column).
	if ticker == "" || (ticker[0] >= '0' && ticker[0] <= '9') {
		return models.EvaluationParsedTicker{}
	}

	rankStr := get("rank", "#", "no.")
	rank, _ := strconv.Atoi(rankStr)

	setup := get("setup", "pattern", "setup_type")
	entryZoneStr := get("entry_zone", "entry", "entry_range")
	stopStr := get("stop", "stop_price")
	riskStr := get("riskpct", "risk_pct", "risk")
	t1Str := get("t1", "target_1", "target1", "t1_(3m)")
	t2Str := get("t2", "target_2", "target2", "t2_(6m)", "t2_(12m)")
	rrStr := get("r/r", "rr", "risk_reward", "r:r", "r/r_(t1)")
	sizeStr := get("size", "position_size", "position", "sizepct", "size%", "sizepct")
	conviction := get("conviction")
	holdStr := get("hold", "hold_period")

	accumStr := get("accum/distrib", "a/d", "accum", "accumulation")

	entryLow, entryHigh := parseEntryZone(entryZoneStr)
	stop := parsePrice(stopStr)
	target1 := parsePrice(t1Str)
	target2 := parsePrice(t2Str)
	riskPct := parsePctValue(riskStr)
	rr := parseRiskReward(rrStr)

	return models.EvaluationParsedTicker{
		Ticker:       ticker,
		Rank:         rank,
		Setup:        setup,
		Conviction:   strings.ToUpper(conviction),
		EntryLow:     entryLow,
		EntryHigh:    entryHigh,
		StopPrice:    stop,
		Target1:      target1,
		Target2:      target2,
		RiskPct:      riskPct,
		RiskReward:   rr,
		PositionSize: cleanPositionSize(sizeStr),
		HoldPeriod:   cleanValue(holdStr),
		AccumDistrib: strings.ToUpper(cleanValue(accumStr)),
	}
}

// ── Detailed block parser ─────────────────────────────────────────────────────

var (
	// Matches "**1. AAPL — Apple Inc.**" or "**1. STOCK_A**" or "**1. STOCK_A — Description**"
	tickerHeaderRe = regexp.MustCompile(`\*\*(\d+)\.\s+((?:STOCK_[A-Z]+)|(?:[A-Z]{1,6}))(?:\s*[—–-]\s*.+?)?\*\*`)
	// Matches "### AAPL" or "### AAPL — Description" (Claude format, no numbering)
	tickerH3Re   = regexp.MustCompile(`^###\s+((?:STOCK_[A-Z]+)|(?:[A-Z]{1,6}))(?:\s*[—–-].*)?$`)
	entryZoneRe  = regexp.MustCompile(`(?i)\*?\*?Entry\s*(?:zone|range)?\*?\*?:?\s*(.+)`)
	stopRe       = regexp.MustCompile(`(?i)\*?\*?Stop\*?\*?:?\s*(.+)`)
	t1t2Re       = regexp.MustCompile(`(?i)\*?\*?T1\s*/\s*T2\*?\*?:?\s*(.+)`)
	t1Re         = regexp.MustCompile(`(?i)\*?\*?(?:T1|Target\s*1)\*?\*?:?\s*(.+)`)
	t2Re         = regexp.MustCompile(`(?i)\*?\*?(?:T2|Target\s*2)\*?\*?:?\s*(.+)`)
	rrRe         = regexp.MustCompile(`(?i)\*?\*?R/R\*?\*?:?\s*(.+)`)
	sizeRe       = regexp.MustCompile(`(?i)\*?\*?(?:Size|Position)\*?\*?:?\s*(.+)`)
	setupRe      = regexp.MustCompile(`(?i)\*?\*?Setup\*?\*?:?\s*(.+)`)
	convictionRe = regexp.MustCompile(`(?i)\*?\*?Conviction\*?\*?:?\s*(.+)`)
	holdPeriodRe = regexp.MustCompile(`(?i)\*?\*?Hold\s*(?:period|horizon)\*?\*?:?\s*(.+)`)
	accumRe      = regexp.MustCompile(`(?i)\*?\*?(?:A/D|Accum|Accumulation)\*?\*?:?\s*(.+)`)
	riskPctRe    = regexp.MustCompile(`(?i)(?:Risk[%\s]*(?:\([^)]*\)\s*:?\s*)?\*?\*?([\d.]+)\s*%|Risk\s+([\d.]+)%)`)
	// Disqualifier extraction (R-06) — matches "disqualifier_reason:" or "Disqualifier reason:" or "disqualifier reason:"
	disqualifierRe = regexp.MustCompile(`(?i)\*?\*?disqualifier[\s_]*reason\*?\*?:?\s*(.+)`)

	// Matches "R/R" or "R:R" values in prose lines like "**0.54:1** — poor R/R"
	rrInlineRe = regexp.MustCompile(`\*?\*?([\d.]+)\s*:\s*1\*?\*?`)
)

// parseDetailedBlocks extracts tickers from the **[RANK]. [TICKER]** blocks
// and also from ### TICKER headers (Claude format).
func parseDetailedBlocks(rawResponse string) []models.EvaluationParsedTicker {
	lines := strings.Split(rawResponse, "\n")
	tickers := make([]models.EvaluationParsedTicker, 0, 20)

	// Find all ticker block boundaries.
	type blockBoundary struct {
		lineIdx int
		rank    int
		ticker  string
	}
	var boundaries []blockBoundary
	h3Rank := 0 // counter for ### headers that lack explicit numbering
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Strategy 1: **1. TICKER — Name**
		matches := tickerHeaderRe.FindStringSubmatch(trimmed)
		if matches != nil {
			rank, _ := strconv.Atoi(matches[1])
			boundaries = append(boundaries, blockBoundary{lineIdx: i, rank: rank, ticker: matches[2]})
			continue
		}
		// Strategy 2: ### TICKER or ### TICKER — description
		matches = tickerH3Re.FindStringSubmatch(trimmed)
		if matches != nil {
			h3Rank++
			boundaries = append(boundaries, blockBoundary{lineIdx: i, rank: h3Rank, ticker: matches[1]})
		}
	}

	for idx, b := range boundaries {
		endLine := len(lines)
		if idx+1 < len(boundaries) {
			endLine = boundaries[idx+1].lineIdx
		}

		blockText := strings.Join(lines[b.lineIdx:endLine], "\n")
		t := parseOneBlock(blockText, b.rank, b.ticker)
		tickers = append(tickers, t)
	}

	return tickers
}

func parseOneBlock(block string, rank int, ticker string) models.EvaluationParsedTicker {
	t := models.EvaluationParsedTicker{
		Ticker: ticker,
		Rank:   rank,
	}

	if m := setupRe.FindStringSubmatch(block); m != nil {
		t.Setup = cleanValue(m[1])
	}
	if m := convictionRe.FindStringSubmatch(block); m != nil {
		t.Conviction = strings.ToUpper(cleanValue(m[1]))
	}
	if m := entryZoneRe.FindStringSubmatch(block); m != nil {
		t.EntryLow, t.EntryHigh = parseEntryZone(m[1])
	}
	if m := stopRe.FindStringSubmatch(block); m != nil {
		t.StopPrice = parsePrice(m[1])
		// Also try to extract risk percent from the stop line.
		if rm := riskPctRe.FindStringSubmatch(m[1]); rm != nil {
			t.RiskPct = extractRiskPct(rm)
		}
	}
	// Try combined T1/T2 first, then fall back to separate lines.
	if m := t1t2Re.FindStringSubmatch(block); m != nil {
		parts := strings.Split(m[1], "/")
		if len(parts) >= 1 {
			t.Target1 = parsePrice(parts[0])
		}
		if len(parts) >= 2 {
			t.Target2 = parsePrice(parts[1])
		}
	} else {
		if m := t1Re.FindStringSubmatch(block); m != nil {
			t.Target1 = parsePrice(m[1])
		}
		if m := t2Re.FindStringSubmatch(block); m != nil {
			t.Target2 = parsePrice(m[1])
		}
	}
	if m := rrRe.FindStringSubmatch(block); m != nil {
		t.RiskReward = parseRiskReward(m[1])
	}
	// Also try inline R/R like "**0.54:1**"
	if t.RiskReward == nil {
		if m := rrInlineRe.FindStringSubmatch(block); m != nil {
			t.RiskReward = parseRiskReward(m[1])
		}
	}
	// Also try extracting risk_pct from a standalone "Risk%" line in the block.
	if t.RiskPct == nil {
		if rm := riskPctRe.FindStringSubmatch(block); rm != nil {
			t.RiskPct = extractRiskPct(rm)
		}
	}
	if m := sizeRe.FindStringSubmatch(block); m != nil {
		t.PositionSize = cleanPositionSize(m[1])
	}
	if m := holdPeriodRe.FindStringSubmatch(block); m != nil {
		t.HoldPeriod = cleanValue(m[1])
	}
	if m := accumRe.FindStringSubmatch(block); m != nil {
		t.AccumDistrib = strings.ToUpper(cleanValue(m[1]))
	}

	// Disqualifier extraction (R-06): check for DISQUALIFIED conviction or explicit disqualifier_reason.
	if strings.Contains(strings.ToUpper(t.Conviction), "DISQUALIFIED") {
		t.Disqualified = true
	}
	if m := disqualifierRe.FindStringSubmatch(block); m != nil {
		t.Disqualified = true
		t.DisqualifierReason = cleanValue(m[1])
	}

	return t

}

// ── Merge ─────────────────────────────────────────────────────────────────────

func mergeParsed(blocks, table []models.EvaluationParsedTicker) []models.EvaluationParsedTicker {
	if len(blocks) == 0 && len(table) == 0 {
		return nil
	}
	if len(blocks) == 0 {
		return table
	}
	if len(table) == 0 {
		return blocks
	}

	// Build a map of table tickers for validation.
	tableMap := make(map[string]models.EvaluationParsedTicker, len(table))
	for i := range table {
		tableMap[table[i].Ticker] = table[i]
	}

	// Prefer block data; fill missing fields from table.
	seen := make(map[string]bool)
	var merged []models.EvaluationParsedTicker
	for i := range blocks {
		b := &blocks[i]
		seen[b.Ticker] = true
		if tt, ok := tableMap[b.Ticker]; ok {
			tt := tt // copy for pointer
			*b = *fillMissing(b, &tt)
		}
		merged = append(merged, *b)
	}

	// Add table-only tickers.
	for i := range table {
		if !seen[table[i].Ticker] {
			merged = append(merged, table[i])
		}
	}

	return merged
}

func fillMissing(primary, fallback *models.EvaluationParsedTicker) *models.EvaluationParsedTicker {
	if primary.Setup == "" {
		primary.Setup = fallback.Setup
	}
	if primary.EntryLow == nil {
		primary.EntryLow = fallback.EntryLow
	}
	if primary.EntryHigh == nil {
		primary.EntryHigh = fallback.EntryHigh
	}
	if primary.StopPrice == nil {
		primary.StopPrice = fallback.StopPrice
	}
	if primary.Target1 == nil {
		primary.Target1 = fallback.Target1
	}
	if primary.Target2 == nil {
		primary.Target2 = fallback.Target2
	}
	if primary.RiskPct == nil {
		primary.RiskPct = fallback.RiskPct
	}
	if primary.RiskReward == nil {
		primary.RiskReward = fallback.RiskReward
	}
	if primary.PositionSize == "" {
		primary.PositionSize = fallback.PositionSize
	}
	if primary.Conviction == "" {
		primary.Conviction = fallback.Conviction
	}
	return primary
}

// ── Price & value parsing helpers ─────────────────────────────────────────────

// extractRiskPct handles the two-group riskPctRe regex: group 1 catches
// "Risk% (...): **7.6%**" format, group 2 catches "Risk 7.6%" format.
func extractRiskPct(groups []string) *float64 {
	for _, g := range groups[1:] {
		if g != "" {
			return parsePctValue(g)
		}
	}
	return nil
}

var priceRe = regexp.MustCompile(`\$?([\d,]+\.?\d*)`)

// parsePrice extracts a dollar price from text like "$142.50" or "142.50" or "$1,234.56".
func parsePrice(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(strings.ToLower(s), "insufficient") || s == "-" || s == "N/A" {
		return nil
	}
	m := priceRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	cleaned := strings.ReplaceAll(m[1], ",", "")
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return nil
	}
	return &v
}

// parseEntryZone extracts low and high from "$142.50–$145.00" or "$142.50-145.00" or "$142.50 to $145.00".
func parseEntryZone(s string) (low, high *float64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	// Split by common delimiters: – (em-dash), - (hyphen), "to"
	var parts []string
	for _, sep := range []string{"–", "—", "-", " to "} {
		if strings.Contains(s, sep) {
			parts = strings.SplitN(s, sep, 2)
			break
		}
	}

	if len(parts) == 2 {
		return parsePrice(parts[0]), parsePrice(parts[1])
	}
	// Single price: treat as both low and high.
	p := parsePrice(s)
	return p, p
}

var rrParseRe = regexp.MustCompile(`([\d.]+)`)

// parseRiskReward extracts R:R from "3.2:1" or "3:1" or "3.2 to 1".
func parseRiskReward(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := rrParseRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	return &v
}

// parsePctValue extracts a percentage value from "4.2%" or "4.2".
func parsePctValue(s string) *float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func cleanValue(s string) string {
	s = strings.TrimSpace(s)
	// Remove trailing markdown or pipe artifacts.
	s = strings.TrimRight(s, "*|")
	s = strings.TrimSpace(s)
	return s
}

// cleanPositionSize normalizes "STANDARD (20%)" → "STANDARD".
func cleanPositionSize(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	// Strip parenthetical like "(20%)".
	if idx := strings.Index(s, "("); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}
