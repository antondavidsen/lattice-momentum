// Package llm provides the LLM evaluation service for the nightly pipeline.
// It loads prompt templates, enriches them with ticker data, calls the LLM
// provider, and returns structured evaluation results.
package llm

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"strings"
	"time"

	"ai-stock-service/internal/models"
)

//go:embed prompts/*.md
var promptFS embed.FS

// PromptVersion is recorded alongside every LLM evaluation for reproducibility.
const PromptVersion = "v7"

// ComputePromptVersionHash returns a version string like "v7-a3f2b1c8" based on
// the base version and the SHA256 hash of the combined system+user template content.
func ComputePromptVersionHash(baseVersion, systemPrompt, userTemplate string) string {
	h := sha256.New()
	h.Write([]byte(systemPrompt))
	h.Write([]byte(userTemplate))
	hash := fmt.Sprintf("%x", h.Sum(nil))[:8]
	return fmt.Sprintf("%s-%s", baseVersion, hash)
}

// promptTemplate holds the separated system / user portions of a prompt file.
type promptTemplate struct {
	System string
	User   string
}

// listTypeToFile maps list types to their prompt template filenames.
var listTypeToFile = map[models.ListType]string{
	models.ListTypeEP:       "prompts/ep_prompt.md",
	models.ListTypeMomentum: "prompts/momentum_prompt.md",
	models.ListTypeLeaders:  "prompts/leaders_prompt.md",
}

// loadPromptTemplate reads the embedded markdown file and splits it into
// system and user sections using the "USER" divider.
func loadPromptTemplate(listType models.ListType) (promptTemplate, error) {
	filename, ok := listTypeToFile[listType]
	if !ok {
		return promptTemplate{}, fmt.Errorf("no prompt template for list type %q", listType)
	}

	data, err := promptFS.ReadFile(filename)
	if err != nil {
		return promptTemplate{}, fmt.Errorf("read prompt template %s: %w", filename, err)
	}

	return splitPrompt(string(data))
}

// splitPrompt separates a prompt markdown file into system and user parts.
// It looks for the "USER" header line (preceded by the ═══ divider).
func splitPrompt(raw string) (promptTemplate, error) {
	const divider = "═══════════════════════════════════════════════════════════════════"

	// Find the second occurrence of the divider (after USER header).
	parts := strings.SplitN(raw, divider, 3)
	if len(parts) < 3 {
		return promptTemplate{}, fmt.Errorf("prompt template missing USER section divider")
	}

	// parts[0] = SYSTEM header + system text
	// parts[1] = "\nUSER\n"
	// parts[2] = user text + trailing divider
	systemText := extractAfterHeader(parts[0], "SYSTEM")
	userText := parts[2]

	// Strip trailing divider if present.
	if idx := strings.LastIndex(userText, divider); idx >= 0 {
		userText = userText[:idx]
	}

	return promptTemplate{
		System: strings.TrimSpace(systemText),
		User:   strings.TrimSpace(userText),
	}, nil
}

// extractAfterHeader removes the first line that contains header and returns
// the remaining text.
func extractAfterHeader(text, header string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, header) {
			// Skip header and the decorative line below it.
			start := i + 1
			if start < len(lines) && strings.TrimSpace(lines[start]) == "──────" {
				start++
			}
			return strings.Join(lines[start:], "\n")
		}
	}
	return text
}

// TickerMapping holds the bidirectional mapping between real tickers and
// anonymous identifiers used in prompts to prevent name bias.
type TickerMapping struct {
	ToAnon map[string]string // real ticker → anonymous ID (e.g. "NVDA" → "STOCK_A")
	ToReal map[string]string // anonymous ID → real ticker (e.g. "STOCK_A" → "NVDA")
}

// buildTickerMapping creates anonymous identifiers for each snapshot ticker.
// Uses STOCK_A, STOCK_B, … STOCK_Z, STOCK_AA, STOCK_AB, etc.
func buildTickerMapping(snapshots []models.TradingViewSnapshotDaily) TickerMapping {
	m := TickerMapping{
		ToAnon: make(map[string]string, len(snapshots)),
		ToReal: make(map[string]string, len(snapshots)),
	}
	for i := range snapshots {
		anon := indexToAnonID(i)
		m.ToAnon[snapshots[i].Ticker] = anon
		m.ToReal[anon] = snapshots[i].Ticker
	}
	return m
}

func indexToAnonID(i int) string {
	if i < 26 {
		return fmt.Sprintf("STOCK_%c", 'A'+i)
	}
	// For > 26 tickers: STOCK_AA, STOCK_AB, etc.
	return fmt.Sprintf("STOCK_%c%c", 'A'+(i/26-1), 'A'+(i%26))
}

// DeAnonymizeResponse replaces all anonymous IDs in the LLM response with real tickers.
func DeAnonymizeResponse(response string, mapping TickerMapping) string {
	// Replace longer IDs first (STOCK_AA before STOCK_A) to avoid partial matches.
	// Since we only have up to ~10 tickers, simple iteration is fine.
	result := response
	// Process in reverse order (longer IDs first).
	for i := len(mapping.ToReal) - 1; i >= 0; i-- {
		anon := indexToAnonID(i)
		if r, ok := mapping.ToReal[anon]; ok {
			result = strings.ReplaceAll(result, anon, r)
		}
	}
	return result
}

// renderUserPrompt injects ticker data, market regime, sector scores, candle
// history, options flow, narrative velocity, and RAG context into the user
// prompt template. Tickers are anonymised to prevent name bias.
//
// optionsFlow and narrativeVelocity are optional (may be nil) — they are
// injected as zero values when unavailable. ragContext is also optional.
func renderUserPrompt(
	tmpl string,
	date time.Time,
	listType models.ListType,
	snapshots []models.TradingViewSnapshotDaily,
	regime *models.MarketRegimeDaily,
	sectorScores []models.SectorScoreDaily,
	candlesByTicker map[string][]models.CandleDaily, // ticker → options_flow_score (optional)
	narrativeVelocity map[string]float64, // ticker → narrative_velocity (optional)
	ragContext string, // RAG context block (optional)
) (string, TickerMapping) {
	tickerMap := buildTickerMapping(snapshots)
	// Build header section with date and market context.
	var header strings.Builder
	fmt.Fprintf(&header, "**Date:** %s\n\n", date.Format("2006-01-02"))

	if regime != nil {
		confidence := 0.0
		if regime.Confidence != nil {
			confidence = *regime.Confidence
		}
		fmt.Fprintf(&header, "**Market Regime:** %s (confidence: %.0f%%)\n\n",
			strings.ToUpper(regime.Regime), confidence)
	} else {
		header.WriteString("**Market Regime:** [DATA MISSING]\n\n")
	}

	if len(sectorScores) > 0 {
		header.WriteString("**Sector Scores:**\n")
		for i := range sectorScores {
			s := &sectorScores[i]
			fmt.Fprintf(&header, "- %s: %s (score: %.2f)\n", s.ETF, s.Label, s.Score)
		}
		header.WriteString("\n")
	}

	// Build the ticker data table (with anonymous IDs).
	table := buildTickerTable(listType, snapshots, tickerMap)

	// Replace the [PASTE 10 ROWS: ...] block with the actual data.
	result := replaceDataPlaceholder(tmpl, header.String()+table)

	// Append candle history section if at least 30% of tickers have history.
	tickersWithCandles := len(candlesByTicker)
	if tickersWithCandles > 0 && float64(tickersWithCandles)/float64(len(snapshots)) >= 0.3 {
		result = replaceCandlePlaceholder(result, candlesByTicker, snapshots, tickerMap)
	} else {
		result = strings.Replace(result, "[CANDLE_HISTORY]", "[NO CANDLE HISTORY AVAILABLE — use SMA levels and today's OHLC for stops and targets]", 1)
	}

	// Inject narrative velocity scores (per-ticker).
	if len(narrativeVelocity) > 0 {
		var nvSB strings.Builder
		nvSB.WriteString("\n## NARRATIVE VELOCITY\n")
		nvSB.WriteString("| ID | Narrative Velocity |\n")
		nvSB.WriteString("|--------|-------------------|\n")
		for i := range snapshots {
			snap := &snapshots[i]
			anonID := tickerMap.ToAnon[snap.Ticker]
			nvScore := 0.0
			if v, ok := narrativeVelocity[snap.Ticker]; ok {
				nvScore = v
			}
			fmt.Fprintf(&nvSB, "| %s | %.2f |\n", anonID, nvScore)
		}
		nvSB.WriteString("\n")
		result = strings.Replace(result, "{{narrative_velocity_table}}", nvSB.String(), 1)
	} else {
		result = strings.Replace(result, "{{narrative_velocity_table}}", "", 1)
	}

	// Inject RAG context block.
	if ragContext != "" {
		result = strings.Replace(result, "{{rag_context}}", ragContext, 1)
	} else {
		result = strings.Replace(result, "{{rag_context}}", "<!-- No RAG context available -->", 1)
	}

	return result, tickerMap
}

// replaceDataPlaceholder finds the "[PASTE 10 ROWS: ...]" line and replaces
// the entire bracketed section (and surrounding blank lines) with actual data.
func replaceDataPlaceholder(tmpl, replacement string) string {
	lines := strings.Split(tmpl, "\n")
	var out []string
	skipUntilBlank := false

	for _, line := range lines {
		if strings.Contains(line, "[PASTE 10 ROWS:") || strings.Contains(line, "[PASTE 10 ROWS") {
			// Start replacing; consume lines until we hit a blank line or "---".
			out = append(out, replacement)
			skipUntilBlank = true
			continue
		}
		if skipUntilBlank {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == "---" {
				skipUntilBlank = false
				if trimmed == "---" {
					out = append(out, line)
				}
				continue
			}
			continue // still inside the placeholder block
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// buildTickerTable formats snapshot data as a markdown table for the LLM prompt.
// Tickers are replaced with anonymous IDs from the mapping to prevent name bias.
func buildTickerTable(listType models.ListType, snapshots []models.TradingViewSnapshotDaily, tickerMap TickerMapping) string {
	var sb strings.Builder

	switch listType {
	case models.ListTypeEP:
		sb.WriteString("| # | ID | Sector | Market Cap | Price | Open | High | Low | Gap % | Change % | Rel Vol | Avg Vol 10d | Float | RSI | Perf 3M | Perf 6M | Dist 52W High | 52W High | SMA20 | SMA50 | SMA150 | SMA200 | ATR | ADR% | Ext% | EPS TTM | EPS Growth YoY | Revenue TTM | Rev Growth YoY | Gross Margin | Op Margin | ROE | Earnings Date |\n")
		sb.WriteString("|---|--------|--------|------------|-------|------|------|-----|-------|----------|---------|-------------|-------|-----|---------|---------|---------------|----------|-------|-------|--------|--------|-----|------|------|---------|----------------|-------------|----------------|--------------|-----------|-----|---------------|\n")
	case models.ListTypeMomentum:
		sb.WriteString("| # | ID | Sector | Market Cap | Price | Open | High | Low | Rel Vol | RSI | Perf 3M | Perf 6M | Dist 52W High | 52W High | SMA20 | SMA50 | SMA150 | SMA200 | ATR | ADR% | Ext% | EPS TTM | EPS Growth YoY | Revenue TTM | Rev Growth YoY | Gross Margin | Op Margin | ROE | Earnings Date |\n")
		sb.WriteString("|---|--------|--------|------------|-------|------|------|-----|---------|-----|---------|---------|---------------|----------|-------|-------|--------|--------|-----|------|------|---------|----------------|-------------|----------------|--------------|-----------|-----|---------------|\n")
	case models.ListTypeLeaders:
		sb.WriteString("| # | ID | Sector | Market Cap | Price | Rel Vol | RSI | Perf 3M | Perf 6M | Dist 52W High | 52W High | SMA20 | SMA50 | SMA150 | SMA200 | ATR | ADR% | Ext% | EPS TTM | EPS Growth YoY | Revenue TTM | Rev Growth YoY | Gross Margin | Net Margin | Op Margin | ROE | Earnings Date |\n")
		sb.WriteString("|---|--------|--------|------------|-------|---------|-----|---------|---------|---------------|----------|-------|-------|--------|--------|-----|------|------|---------|----------------|-------------|----------------|--------------|------------|-----------|-----|---------------|\n")
	}

	for i := range snapshots {
		s := &snapshots[i]
		anonID := tickerMap.ToAnon[s.Ticker]
		switch listType {
		case models.ListTypeEP:
			fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				i+1,
				anonID,
				fmtStr(s.Sector),
				fmtMarketCap(s.MarketCap),
				fmtFloat(s.Close, 2),
				fmtFloat(s.Open, 2),
				fmtFloat(s.High, 2),
				fmtFloat(s.Low, 2),
				fmtPct(s.GapPct),
				fmtPct(s.ChangePct),
				fmtFloat(s.RelativeVolume, 2),
				fmtInt64(s.AvgVolume10d),
				fmtInt64(s.FloatShares),
				fmtFloat(s.RSI14, 1),
				fmtPct(s.Perf3M),
				fmtPct(s.Perf6M),
				fmtPct(s.Distance52wHigh),
				fmtFloat(s.Price52wHigh, 2),
				fmtFloat(s.SMA20, 2),
				fmtFloat(s.SMA50, 2),
				fmtFloat(s.SMA150, 2),
				fmtFloat(s.SMA200, 2),
				fmtFloat(s.ATR14, 2),
				fmtPct(computeADR(s.ATR14, s.Close)),
				fmtPct(computeExtension(s.Close, s.SMA50)),
				fmtFloat(s.EPSTTM, 2),
				fmtPct(s.EPSGrowthYOY),
				fmtRevenue(s.RevenueTTM),
				fmtPct(s.RevenueGrowthYOY),
				fmtPct(s.GrossMargin),
				fmtPct(s.OperatingMargin),
				fmtPct(s.ROE),
				fmtTime(s.EarningsDate),
			)
		case models.ListTypeMomentum:
			fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				i+1,
				anonID,
				fmtStr(s.Sector),
				fmtMarketCap(s.MarketCap),
				fmtFloat(s.Close, 2),
				fmtFloat(s.Open, 2),
				fmtFloat(s.High, 2),
				fmtFloat(s.Low, 2),
				fmtFloat(s.RelativeVolume, 2),
				fmtFloat(s.RSI14, 1),
				fmtPct(s.Perf3M),
				fmtPct(s.Perf6M),
				fmtPct(s.Distance52wHigh),
				fmtFloat(s.Price52wHigh, 2),
				fmtFloat(s.SMA20, 2),
				fmtFloat(s.SMA50, 2),
				fmtFloat(s.SMA150, 2),
				fmtFloat(s.SMA200, 2),
				fmtFloat(s.ATR14, 2),
				fmtPct(computeADR(s.ATR14, s.Close)),
				fmtPct(computeExtension(s.Close, s.SMA50)),
				fmtFloat(s.EPSTTM, 2),
				fmtPct(s.EPSGrowthYOY),
				fmtRevenue(s.RevenueTTM),
				fmtPct(s.RevenueGrowthYOY),
				fmtPct(s.GrossMargin),
				fmtPct(s.OperatingMargin),
				fmtPct(s.ROE),
				fmtTime(s.EarningsDate),
			)
		case models.ListTypeLeaders:
			fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				i+1,
				anonID,
				fmtStr(s.Sector),
				fmtMarketCap(s.MarketCap),
				fmtFloat(s.Close, 2),
				fmtFloat(s.RelativeVolume, 2),
				fmtFloat(s.RSI14, 1),
				fmtPct(s.Perf3M),
				fmtPct(s.Perf6M),
				fmtPct(s.Distance52wHigh),
				fmtFloat(s.Price52wHigh, 2),
				fmtFloat(s.SMA20, 2),
				fmtFloat(s.SMA50, 2),
				fmtFloat(s.SMA150, 2),
				fmtFloat(s.SMA200, 2),
				fmtFloat(s.ATR14, 2),
				fmtPct(computeADR(s.ATR14, s.Close)),
				fmtPct(computeExtension(s.Close, s.SMA50)),
				fmtFloat(s.EPSTTM, 2),
				fmtPct(s.EPSGrowthYOY),
				fmtRevenue(s.RevenueTTM),
				fmtPct(s.RevenueGrowthYOY),
				fmtPct(s.GrossMargin),
				fmtPct(s.NetMargin),
				fmtPct(s.OperatingMargin),
				fmtPct(s.ROE),
				fmtTime(s.EarningsDate),
			)
		}
	}

	return sb.String()
}

// computeADR returns ATR/Price × 100 as a percentage, or nil if inputs are missing.
func computeADR(atr, price *float64) *float64 {
	if atr == nil || price == nil || *price == 0 {
		return nil
	}
	v := (*atr / *price) * 100
	return &v
}

// computeExtension returns (Price - SMA50) / SMA50 × 100 as a percentage.
func computeExtension(price, sma50 *float64) *float64 {
	if price == nil || sma50 == nil || *sma50 == 0 {
		return nil
	}
	v := (*price - *sma50) / *sma50 * 100
	return &v
}

// replaceCandlePlaceholder replaces the [CANDLE_HISTORY] marker with a per-ticker
// table of recent daily OHLCV candles so the LLM can identify real support/resistance
// levels instead of hallucinating them.
func replaceCandlePlaceholder(tmpl string, candlesByTicker map[string][]models.CandleDaily, snapshots []models.TradingViewSnapshotDaily, tickerMap TickerMapping) string {
	var sb strings.Builder
	sb.WriteString("## RECENT CANDLE HISTORY (up to 20 trading days)\n")
	sb.WriteString("Use these candles to identify REAL support/resistance levels, prior close (for gap fill), and base structure.\n")
	sb.WriteString("Do NOT invent price levels — every stop, entry, and target must reference a level visible in this data.\n\n")

	for i := range snapshots {
		snap := &snapshots[i]
		anonID := tickerMap.ToAnon[snap.Ticker]
		candles, ok := candlesByTicker[snap.Ticker]
		if !ok || len(candles) == 0 {
			fmt.Fprintf(&sb, "### %s — [NO CANDLE HISTORY]\n\n", anonID)
			continue
		}
		fmt.Fprintf(&sb, "### %s — Last %d Trading Days\n", anonID, len(candles))
		sb.WriteString("| Date | Open | High | Low | Close | Volume |\n")
		sb.WriteString("|------|------|------|-----|-------|--------|\n")
		for j := range candles {
			c := &candles[j]
			fmt.Fprintf(&sb, "| %s | %.2f | %.2f | %.2f | %.2f | %s |\n",
				c.Date.Format("2006-01-02"),
				c.Open, c.High, c.Low, c.Close,
				fmtVol(c.Volume),
			)
		}
		sb.WriteString("\n")
	}

	return strings.Replace(tmpl, "[CANDLE_HISTORY]", sb.String(), 1)
}

// fmtVol formats volume with K/M suffix for compact display.
func fmtVol(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1e6)
	case v >= 1_000:
		return fmt.Sprintf("%.0fK", float64(v)/1e3)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// ── Formatting helpers ─────────────────────────────────────────────────────────

func fmtFloat(p *float64, decimals int) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	return fmt.Sprintf("%.*f", decimals, *p)
}

// fmtPct formats a value that is already in percent form (e.g. 21.8 → "21.8%").
// TradingView returns all percentage fields as whole numbers (21.8 = 21.8%).
func fmtPct(p *float64) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	return fmt.Sprintf("%.1f%%", *p)
}

func fmtStr(p *string) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	return *p
}

func fmtInt64(p *int64) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	v := *p
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(v)/1e9)
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1e6)
	case v >= 1_000:
		return fmt.Sprintf("%.0fK", float64(v)/1e3)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func fmtMarketCap(p *float64) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	v := *p
	switch {
	case v >= 1e12:
		return fmt.Sprintf("$%.1fT", v/1e12)
	case v >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("$%.0fM", v/1e6)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}

func fmtRevenue(p *float64) string {
	if p == nil {
		return "[DATA MISSING]"
	}
	v := *p
	switch {
	case v >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("$%.0fM", v/1e6)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "[DATA MISSING]"
	}
	return t.Format("2006-01-02")
}

// PipelineHealthInfo holds pipeline health context for the LLM prompt.
type PipelineHealthInfo struct {
	CandidatesPreFilter int
	CandidatesFinal     int
	Health              string // "normal", "thin", "single", "empty"
}
