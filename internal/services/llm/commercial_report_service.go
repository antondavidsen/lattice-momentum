package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ai-stock-service/internal/llm"
	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/models"
)

// CommercialReportPromptVersion is recorded alongside every commercial report
// for reproducibility.
const CommercialReportPromptVersion = "v3"

// commercialReportPromptFile is the embedded prompt template.
const commercialReportPromptFile = "prompts/commercial_report_prompt.md"

// ── CommercialReportConfig ────────────────────────────────────────────────────

// CommercialReportConfig holds tuneable parameters for the report transformer.
type CommercialReportConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

func (c *CommercialReportConfig) applyDefaults() {
	if c.MaxTokens == 0 {
		c.MaxTokens = 16000 // commercial reports are long structured JSON
	}
	if c.Temperature == 0 {
		c.Temperature = 0.15 // low creativity — we want faithful rewriting
	}
}

// ── CommercialReportResult ────────────────────────────────────────────────────

// CommercialReportResult is the parsed output from the LLM transformer.
type CommercialReportResult struct {
	Date            time.Time
	Regime          string
	Headline        string
	MarketSummary   string
	SectorSummary   string
	TradeCards      []models.CommercialTradeCard
	RiskNote        string
	ClosingSummary  string
	FullReportMD    string
	SourceListTypes []string
	Provider        string
	Model           string
	InputTokens     int
	OutputTokens    int
	DurationMs      int
	TradeOfDay      *models.TradeOfDay
}

// ToModel converts the result into a database model ready for persistence.
func (r *CommercialReportResult) ToModel() *models.CommercialReport {
	cardsJSON, _ := json.Marshal(r.TradeCards)
	inputTokens := r.InputTokens
	outputTokens := r.OutputTokens
	durationMs := r.DurationMs

	return &models.CommercialReport{
		ReportDate:         r.Date,
		Regime:             r.Regime,
		Headline:           r.Headline,
		MarketSummary:      r.MarketSummary,
		SectorSummary:      r.SectorSummary,
		TradeCardsJSON:     json.RawMessage(cardsJSON),
		RiskNote:           r.RiskNote,
		ClosingSummary:     r.ClosingSummary,
		FullReportMarkdown: r.FullReportMD,
		PerformanceBlurb:   "", // populated externally from trade outcomes
		SourceListTypes:    r.SourceListTypes,
		Provider:           r.Provider,
		Model:              r.Model,
		PromptVersion:      CommercialReportPromptVersion,
		InputTokens:        &inputTokens,
		OutputTokens:       &outputTokens,
		DurationMs:         &durationMs,
	}
}

// ── CommercialReportService ───────────────────────────────────────────────────

// CommercialReportService transforms institutional research memos from
// llm_list_evaluations.raw_response into subscriber-friendly commercial reports.
//
// It acts as a financial copywriter: it rewrites language and structure but
// NEVER changes numbers, tickers, or trading levels.
type CommercialReportService struct {
	provider llm.Provider
	cfg      CommercialReportConfig
	log      *slog.Logger
}

// NewCommercialReportService constructs a CommercialReportService.
func NewCommercialReportService(
	provider llm.Provider,
	cfg CommercialReportConfig,
	log *slog.Logger,
) *CommercialReportService {
	cfg.applyDefaults()
	return &CommercialReportService{
		provider: provider,
		cfg:      cfg,
		log:      log,
	}
}

// Transform reads the raw institutional memos and sends them to the LLM for
// rewriting into a commercial subscriber report.
func (s *CommercialReportService) Transform(
	ctx context.Context,
	date time.Time,
	evals []models.LLMListEvaluation,
	regime string,
) (*CommercialReportResult, error) {
	tag := date.Format("2006-01-02")
	start := time.Now()

	if len(evals) == 0 {
		return nil, fmt.Errorf("CommercialReportService [%s]: no evaluations to transform", tag)
	}

	s.log.Info("CommercialReportService: starting transformation",
		"date", tag,
		"eval_count", len(evals),
	)

	// ── 1. Load prompt template ───────────────────────────────────────────────
	tmpl, err := loadCommercialPrompt()
	if err != nil {
		return nil, fmt.Errorf("load commercial prompt: %w", err)
	}

	// ── 2. Assemble the source memos ──────────────────────────────────────────
	var listTypes []string
	var memos strings.Builder
	for i := range evals {
		if i > 0 {
			memos.WriteString("\n\n---\n\n")
		}
		label := commercialListLabel(evals[i].ListType)
		listTypes = append(listTypes, string(evals[i].ListType))
		fmt.Fprintf(&memos, "### %s List — %s\n\n", label, tag)
		memos.WriteString(evals[i].RawResponse)
	}

	// ── 3. Build the user prompt ──────────────────────────────────────────────
	userPrompt := strings.Replace(tmpl.User, "[PASTE_MEMOS_HERE]", memos.String(), 1)

	s.log.Info("CommercialReportService: prompt assembled",
		"date", tag,
		"system_len", len(tmpl.System),
		"user_len", len(userPrompt),
		"list_types", listTypes,
	)

	// ── 4. Call LLM ───────────────────────────────────────────────────────────
	req := llm.Request{
		Model:             s.cfg.Model,
		SystemPrompt:      tmpl.System,
		UserPrompt:        userPrompt,
		Temperature:       s.cfg.Temperature,
		MaxTokens:         s.cfg.MaxTokens,
		CacheSystemPrompt: true,
		Stream:            true,
	}

	resp, err := s.provider.Generate(ctx, &req)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(s.provider.Name(), "unknown", "error", "commercial_report").Inc()
		return nil, fmt.Errorf("LLM call failed for commercial report [%s]: %w", tag, err)
	}

	// Record LLM metrics
	llmCallDuration := time.Since(start).Seconds()
	model := resp.Model
	if model == "" {
		model = "unknown"
	}
	metrics.LLMRequestsTotal.WithLabelValues(resp.Provider, model, "success", "commercial_report").Inc()
	metrics.LLMRequestDuration.WithLabelValues(resp.Provider, model, "commercial_report").Observe(llmCallDuration)
	metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "input", "commercial_report").Add(float64(resp.InputTokens))
	metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "output", "commercial_report").Add(float64(resp.OutputTokens))
	if resp.CacheReadTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "cache_read", "commercial_report").Add(float64(resp.CacheReadTokens))
		metrics.LLMTokensCachedTotal.WithLabelValues(resp.Provider, model, "commercial_report").Add(float64(resp.CacheReadTokens))
	}
	if resp.CacheCreationTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "cache_write", "commercial_report").Add(float64(resp.CacheCreationTokens))
	}
	// Track uncached input tokens: total input minus cache_read.
	uncachedInput := resp.InputTokens - resp.CacheReadTokens
	if uncachedInput > 0 {
		metrics.LLMTokensUncachedTotal.WithLabelValues(resp.Provider, model, "commercial_report").Add(float64(uncachedInput))
	}

	durationMs := int(time.Since(start).Milliseconds())

	s.log.Info("CommercialReportService: LLM call completed",
		"date", tag,
		"provider", resp.Provider,
		"model", resp.Model,
		"input_tokens", resp.InputTokens,
		"output_tokens", resp.OutputTokens,
		"cache_read_tokens", resp.CacheReadTokens,
		"cache_creation_tokens", resp.CacheCreationTokens,
		"duration_ms", durationMs,
		"response_len", len(resp.Text),
	)

	// ── 5. Parse structured JSON from response ────────────────────────────────
	result, err := parseCommercialResponse(resp.Text)
	if err != nil {
		return nil, fmt.Errorf("parse commercial report response [%s]: %w", tag, err)
	}

	result.Date = date
	result.Regime = regime
	result.SourceListTypes = listTypes
	result.Provider = resp.Provider
	result.Model = resp.Model
	result.InputTokens = resp.InputTokens
	result.OutputTokens = resp.OutputTokens
	result.DurationMs = durationMs

	// ── 6. Select Trade of the Day ────────────────────────────────────────────
	result.TradeOfDay = SelectTradeOfDay(result.TradeCards)

	s.log.Info("CommercialReportService: transformation complete",
		"date", tag,
		"headline", result.Headline,
		"trade_cards", len(result.TradeCards),
		"trade_of_day", result.TradeOfDay != nil,
	)

	return result, nil
}

// GenerateHaltReport produces a minimal commercial report when the circuit
// breaker gate level is HALT.  No LLM call is made — the report is constructed
// from the regime data alone and signals that the pipeline is in cash-preservation
// mode.
func (s *CommercialReportService) GenerateHaltReport(
	_ context.Context,
	date time.Time,
	regimeLabel string,
	vixLevel float64,
) (*models.CommercialReport, error) {
	tag := date.Format("2006-01-02")

	var headline, summary string

	switch regimeLabel {
	case "bear":
		headline = "Market in bear regime — no new trade ideas"
		summary = fmt.Sprintf(
			"Pipeline halted. Regime: %s, VIX: %.1f. The system is in cash-preservation mode.",
			regimeLabel, vixLevel,
		)
	case "correction":
		if vixLevel >= 28.0 {
			headline = "Market in correction with elevated VIX — no new trade ideas"
			summary = fmt.Sprintf(
				"Pipeline halted. Regime: %s, VIX: %.1f (≥28 threshold). The system is in cash-preservation mode.",
				regimeLabel, vixLevel,
			)
		}
	default:
		headline = "Market in high-risk regime — no new trade ideas"
		summary = fmt.Sprintf(
			"Pipeline halted. Regime: %s, VIX: %.1f. The system is in cash-preservation mode.",
			regimeLabel, vixLevel,
		)
	}

	// Fallback if headline/summary weren't set (e.g. correction with VIX < 28
	// should never reach here, but be defensive).
	if headline == "" {
		headline = "Market conditions uncertain — no new trade ideas"
		summary = fmt.Sprintf(
			"Pipeline halted. Regime: %s, VIX: %.1f. The system is in cash-preservation mode.",
			regimeLabel, vixLevel,
		)
	}

	emptyCards, _ := json.Marshal([]models.CommercialTradeCard{})

	report := &models.CommercialReport{
		ReportDate:         date,
		Regime:             regimeLabel,
		Headline:           headline,
		MarketSummary:      summary,
		SectorSummary:      "",
		TradeCardsJSON:     json.RawMessage(emptyCards),
		RiskNote:           "🚫 MARKET CLOSED TO NEW IDEAS: No ranking lists are generated today. Prioritise risk management and wait for conditions to improve.",
		ClosingSummary:     "",
		FullReportMarkdown: "",
		PerformanceBlurb:   "",
		SourceListTypes:    []string{},
		Provider:           "circuit_breaker",
		Model:              "rule_based",
		PromptVersion:      "halt",
		InputTokens:        nil,
		OutputTokens:       nil,
		DurationMs:         nil,
		GateLevel:          "halt",
		RegimeLabel:        regimeLabel,
		VIXLevel:           vixLevel,
	}

	s.log.Info("CommercialReportService: halt report generated",
		"date", tag,
		"regime", regimeLabel,
		"vix", vixLevel,
		"headline", headline,
	)

	return report, nil
}

// ── Prompt loading ────────────────────────────────────────────────────────────

func loadCommercialPrompt() (promptTemplate, error) {
	data, err := promptFS.ReadFile(commercialReportPromptFile)
	if err != nil {
		return promptTemplate{}, fmt.Errorf("read commercial prompt: %w", err)
	}
	return splitPrompt(string(data))
}

// ── Response parsing ──────────────────────────────────────────────────────────

// commercialResponseJSON mirrors the JSON structure the LLM is instructed to
// produce. We parse into this and then map to our result type.
type commercialResponseJSON struct {
	Headline           string                       `json:"headline"`
	MarketSummary      string                       `json:"market_summary"`
	SectorSummary      string                       `json:"sector_summary"`
	TradeCards         []models.CommercialTradeCard `json:"trade_cards"`
	RiskNote           string                       `json:"risk_note"`
	ClosingSummary     string                       `json:"closing_summary"`
	FullReportMarkdown string                       `json:"full_report_markdown"`
}

func parseCommercialResponse(raw string) (*CommercialReportResult, error) {
	// The LLM sometimes wraps JSON in markdown fences — strip them.
	cleaned := stripJSONFences(raw)

	var parsed commercialResponseJSON
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w\nraw (first 500 chars): %s",
			err, truncate(cleaned, 500))
	}

	if parsed.Headline == "" {
		return nil, fmt.Errorf("parsed response missing headline")
	}

	return &CommercialReportResult{
		Headline:       parsed.Headline,
		MarketSummary:  parsed.MarketSummary,
		SectorSummary:  parsed.SectorSummary,
		TradeCards:     parsed.TradeCards,
		RiskNote:       parsed.RiskNote,
		ClosingSummary: parsed.ClosingSummary,
		FullReportMD:   parsed.FullReportMarkdown,
	}, nil
}

// stripJSONFences removes ```json ... ``` wrapping that LLMs sometimes add.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)

	// Remove leading ```json or ```
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}

	// Remove trailing ```
	s = strings.TrimSuffix(s, "```")

	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func commercialListLabel(lt models.ListType) string {
	switch lt {
	case models.ListTypeEP:
		return "Catalyst-Driven Trades (EP)"
	case models.ListTypeMomentum:
		return "Momentum Breakout Leaders"
	case models.ListTypeLeaders:
		return "Institutional-Quality Leaders"
	default:
		return string(lt)
	}
}

// ── Trade of the Day ──────────────────────────────────────────────────────────

// convictionScore maps a conviction label to a numeric value for ranking.
func convictionScore(conviction string) float64 {
	switch strings.ToLower(strings.TrimSpace(conviction)) {
	case "high":
		return 1.0
	case "medium":
		return 0.6
	case "starter":
		return 0.3
	default:
		return 0.0
	}
}

// parseRiskRewardRatio parses a risk_reward_ratio string like "3.5:1" or "2:1"
// to a float64. Returns 0 if unparseable. Uses the existing parseRiskReward
// from response_parser.go (which returns *float64) and dereferences it.
func parseRiskRewardRatio(rr string) float64 {
	v := parseRiskReward(rr)
	if v == nil {
		return 0
	}
	return *v
}

// SelectTradeOfDay returns the single highest-conviction trade card from all
// three lists combined. Selection uses the composite formula:
//
//	composite = (conviction_score * 0.6) + (risk_reward * 0.4)
//
// Returns nil if cards is empty.
func SelectTradeOfDay(cards []models.CommercialTradeCard) *models.TradeOfDay {
	if len(cards) == 0 {
		return nil
	}

	bestIdx := 0
	bestScore := -1.0

	for i := range cards {
		cs := convictionScore(cards[i].Conviction)
		rr := parseRiskRewardRatio(cards[i].RiskRewardRatio)
		score := cs*0.6 + rr*0.4
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	reason := fmt.Sprintf(
		"Highest conviction score (%.2f) — %s conviction, %.1f:1 risk/reward",
		bestScore,
		cards[bestIdx].Conviction,
		parseRiskRewardRatio(cards[bestIdx].RiskRewardRatio),
	)

	return &models.TradeOfDay{
		TradeCard:       &cards[bestIdx],
		SelectionReason: reason,
	}
}
