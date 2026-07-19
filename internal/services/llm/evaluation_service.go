package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"ai-stock-service/internal/llm"
	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"

	"github.com/pgvector/pgvector-go"
)

// ── Data source interfaces ────────────────────────────────────────────────────
// Kept local so the service does not depend on concrete repository types.

// SnapshotSource loads TradingView daily snapshots.
type SnapshotSource interface {
	ListByDate(ctx context.Context, date time.Time) ([]models.TradingViewSnapshotDaily, error)
}

// RegimeSource loads the market regime classification.
type RegimeSource interface {
	GetMarketRegimeDaily(ctx context.Context, date time.Time) (*models.MarketRegimeDaily, error)
}

// SectorScoreSource loads sector scores.
type SectorScoreSource interface {
	GetSectorScores(ctx context.Context, date time.Time) ([]models.SectorScoreDaily, error)
}

// CandleSource loads historical daily candles for a ticker.
type CandleSource interface {
	GetCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}

// MemorySource loads historical prompt memories for RAG context (R-06).
type MemorySource interface {
	FindSimilar(ctx context.Context, embedding pgvector.Vector, listType models.ListType, topK int, excludeTicker string, maxAgeDays int) ([]models.PromptMemory, error)
}

// NarrativeVelocitySource loads per-ticker narrative velocity scores.
type NarrativeVelocitySource interface {
	GetByDate(ctx context.Context, date time.Time) (map[string]models.NarrativeVelocityDaily, error)
}

// Compile-time assertions.
var _ SnapshotSource = (*repository.TVSnapshotRepo)(nil)
var _ RegimeSource = (*repository.MarketRegimeRepo)(nil)
var _ SectorScoreSource = (*repository.SectorScoresRepo)(nil)
var _ CandleSource = (*repository.CandlesDailyRepo)(nil)
var _ MemorySource = (*repository.PromptMemoryRepo)(nil)
var _ NarrativeVelocitySource = (*repository.NarrativeVelocityRepo)(nil)

// ── EvaluationConfig ──────────────────────────────────────────────────────────

// EvaluationConfig holds tuneable parameters for LLM list evaluation calls.
type EvaluationConfig struct {
	// Model overrides the default model for evaluations (e.g. "gpt-4.1-mini").
	// Empty → let the provider pick its default.
	Model string

	// MaxTokens caps the LLM response length. Default 4000.
	MaxTokens int

	// Temperature controls randomness. Default 0.2.
	Temperature float64
}

// applyDefaults fills zero-valued config fields with sensible production defaults.
func (c *EvaluationConfig) applyDefaults() {
	if c.MaxTokens == 0 {
		c.MaxTokens = 14000
	}
	if c.Temperature == 0 {
		c.Temperature = 0.2
	}
}

// ── EvaluationResult ──────────────────────────────────────────────────────────

// EvaluationResult is the output of a single list evaluation.
type EvaluationResult struct {
	Date          time.Time
	ListType      models.ListType
	Provider      string
	Model         string
	PromptVersion string
	SystemPrompt  string
	UserPrompt    string
	RawResponse   string
	InputTickers  []string
	OutputTickers []string
	ParsedJSON    []byte
	VariantName   string
	InputTokens   int
	OutputTokens  int
	DurationMs    int
}

// ── EvaluationService ─────────────────────────────────────────────────────────

// EvaluationService orchestrates LLM evaluation of ranked stock lists.
type EvaluationService struct {
	provider          llm.Provider
	snapshots         SnapshotSource
	regime            RegimeSource
	sectorScores      SectorScoreSource
	candles           CandleSource            // optional — nil means no candle history in prompt
	memory            MemorySource            // optional — nil means no RAG context (R-06)
	narrativeVelocity NarrativeVelocitySource // optional — nil means no narrative velocity data
	embedder          *EmbeddingService       // optional — nil means no real embedding (RAG disabled or no API key)
	cfg               EvaluationConfig
	log               *slog.Logger
}

// NewEvaluationService constructs an EvaluationService.
//
// Optional interfaces (candles, memory, narrativeVelocity, optionsFlow) may be nil.
// embedder is also optional — nil means no real embedding (RAG disabled or no API key).
// IMPORTANT: callers must pass interface-typed nil (not concrete-typed nil)
// to avoid the Go interface footgun where (*T)(nil) wrapped in interface != nil.
// As defense-in-depth, we strip any non-nil interface that wraps a nil pointer.
func NewEvaluationService(
	provider llm.Provider,
	snapshots SnapshotSource,
	regime RegimeSource,
	sectorScores SectorScoreSource,
	candles CandleSource,
	memory MemorySource,
	narrativeVelocity NarrativeVelocitySource,
	embedder *EmbeddingService,
	cfg EvaluationConfig,
	log *slog.Logger,
) *EvaluationService {
	cfg.applyDefaults()

	// Defense-in-depth: strip nil concrete pointers wrapped in interfaces.
	// A nil *PromptMemoryRepo passed as MemorySource has type but nil value,
	// so the interface is non-nil but calling methods on it panics.
	candles = stripNilInterface(candles)
	memory = stripNilInterface(memory)
	narrativeVelocity = stripNilInterface(narrativeVelocity)

	if narrativeVelocity == nil {
		log.Warn("narrative velocity source not wired — narrative_velocity will default to 0 in prompts")
	}
	if memory != nil && embedder == nil {
		log.Warn("RAG memory wired but no embedder provided — RAG will use zero-vector fallback")
	}
	return &EvaluationService{
		provider:          provider,
		snapshots:         snapshots,
		regime:            regime,
		sectorScores:      sectorScores,
		candles:           candles,
		memory:            memory,
		narrativeVelocity: narrativeVelocity,
		embedder:          embedder,
		cfg:               cfg,
		log:               log,
	}
}

// screenerSourceForList maps a list type to the corresponding TradingView
// screener source so we pick the correct snapshot rows for enrichment.
var screenerSourceForList = map[models.ListType]models.ScreenerSource{
	models.ListTypeEP:       models.ScreenerEP,
	models.ListTypeMomentum: models.ScreenerMomentum,
	models.ListTypeLeaders:  models.ScreenerMarketLeaders,
}

// EvaluateList sends the ranked tickers for a single list type to the LLM for
// qualitative diligence. It enriches each ticker with snapshot, regime, and
// sector data before rendering the prompt.
//
// tickers is the ordered list of ticker symbols from the daily_rank_lists table
// (up to 10, best first).
func (s *EvaluationService) EvaluateList(
	ctx context.Context,
	date time.Time,
	listType models.ListType,
	tickers []string,
) (*EvaluationResult, error) {
	tag := date.Format("2006-01-02")
	start := time.Now()

	s.log.Info("EvaluationService: starting list evaluation",
		"date", tag,
		"list_type", string(listType),
		"input_tickers", tickers,
	)

	// ── 1. Load prompt template ───────────────────────────────────────────────
	tmpl, err := loadPromptTemplate(listType)
	if err != nil {
		return nil, fmt.Errorf("load prompt template for %s: %w", listType, err)
	}

	// ── 2. Enrich: load snapshot data for the ranked tickers ──────────────────
	allSnaps, err := s.snapshots.ListByDate(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("load snapshots for %s [%s]: %w", listType, tag, err)
	}

	source := screenerSourceForList[listType]
	snapMap := buildSnapshotMap(allSnaps, source)

	// Collect snapshots in rank order, skip any missing.
	var enrichedSnaps []models.TradingViewSnapshotDaily
	for _, ticker := range tickers {
		if snap, ok := snapMap[ticker]; ok {
			enrichedSnaps = append(enrichedSnaps, snap)
		} else {
			s.log.Warn("EvaluationService: no snapshot found for ranked ticker",
				"date", tag, "list_type", string(listType), "ticker", ticker,
			)
		}
	}

	if len(enrichedSnaps) == 0 {
		return nil, fmt.Errorf("no snapshot data found for any ranked ticker in %s [%s]", listType, tag)
	}

	// ── 3. Load market regime ─────────────────────────────────────────────────
	regime, err := s.regime.GetMarketRegimeDaily(ctx, date)
	if err != nil {
		s.log.Warn("EvaluationService: could not load regime, continuing without it",
			"date", tag, "error", err,
		)
		regime = nil
	}

	// ── 4. Load sector scores ─────────────────────────────────────────────────
	sectorScores, err := s.sectorScores.GetSectorScores(ctx, date)
	if err != nil {
		s.log.Warn("EvaluationService: could not load sector scores, continuing without them",
			"date", tag, "error", err,
		)
		sectorScores = nil
	}

	// ── 4b. Load recent candle history (20 trading days) ──────────────────────
	var candlesByTicker map[string][]models.CandleDaily
	if s.candles != nil {
		candlesByTicker = make(map[string][]models.CandleDaily, len(enrichedSnaps))
		from := date.AddDate(0, 0, -35) // ~35 calendar days ≈ 20 trading days
		for i := range enrichedSnaps {
			candles, cErr := s.candles.GetCandles(ctx, enrichedSnaps[i].Ticker, from, date)
			if cErr != nil {
				s.log.Warn("EvaluationService: could not load candles for ticker",
					"date", tag, "ticker", enrichedSnaps[i].Ticker, "error", cErr,
				)
				continue
			}
			if len(candles) > 0 {
				candlesByTicker[enrichedSnaps[i].Ticker] = candles
			}
		}
		s.log.Info("EvaluationService: candle history loaded",
			"date", tag, "list_type", string(listType),
			"tickers_with_candles", len(candlesByTicker),
		)
	}

	// ── 4c. Load RAG context from prompt memory (optional, R-06) ──────────────
	var ragMemories []models.PromptMemory
	if s.memory != nil {
		// Build a context summary from the ticker data for embedding search.
		// Use the real embedder when available; fall back to zero-vector placeholder.
		contextSummary := buildRAGContextSummary(ctx, enrichedSnaps, regime, listType, s.embedder, s.log)
		// Exclude the first ticker from RAG results to avoid self-matches.
		excludeTicker := ""
		if len(tickers) > 0 {
			excludeTicker = tickers[0]
		}
		memories, memErr := s.memory.FindSimilar(ctx, contextSummary, listType, 3, excludeTicker, 365)
		if memErr != nil {
			s.log.Warn("EvaluationService: RAG fetch failed, continuing without it",
				"date", tag, "list_type", string(listType), "error", memErr,
			)
		} else {
			ragMemories = memories
			s.log.Info("EvaluationService: RAG context loaded",
				"date", tag, "list_type", string(listType),
				"memories", len(memories),
			)
		}
	}

	// ── 4d. Build RAG context string from memories ────────────────────────────
	ragContext := buildRAGContextString(ragMemories)

	// ── 4e. Load narrative velocity scores (optional) ─────────────────────────
	var narrativeVelocity map[string]float64
	if s.narrativeVelocity != nil {
		velMap, velErr := s.narrativeVelocity.GetByDate(ctx, date)
		if velErr != nil {
			s.log.Warn("EvaluationService: narrative velocity load failed, continuing without it",
				"date", tag, "error", velErr,
			)
		} else {
			narrativeVelocity = make(map[string]float64, len(velMap))
			for ticker, nv := range velMap { //nolint:gocritic // map iteration always copies value; acceptable for moderate-size map
				narrativeVelocity[ticker] = nv.NarrativeVelocity
			}
			s.log.Info("EvaluationService: narrative velocity loaded",
				"date", tag, "list_type", string(listType),
				"tickers_with_velocity", len(narrativeVelocity),
			)
		}
	}

	// ── 5. Render prompt ──────────────────────────────────────────────────────

	userPrompt, tickerMap := renderUserPrompt(tmpl.User, date, listType, enrichedSnaps, regime, sectorScores, candlesByTicker, narrativeVelocity, ragContext)

	s.log.Info("EvaluationService: prompt rendered (tickers anonymised)",
		"date", tag,
		"list_type", string(listType),
		"system_prompt_len", len(tmpl.System),
		"user_prompt_len", len(userPrompt),
		"enriched_tickers", len(enrichedSnaps),
	)

	// ── 6. Call LLM (with 1 retry) ───────────────────────────────────────────
	// Scale output token budget with the number of tickers: ~1400 tokens per
	// ticker for the detailed analysis + JSON block, with a minimum of 4000
	// and a cap at the configured MaxTokens.
	scaledTokens := len(enrichedSnaps) * 1400
	if scaledTokens < 4000 {
		scaledTokens = 4000
	}
	if scaledTokens > s.cfg.MaxTokens {
		scaledTokens = s.cfg.MaxTokens
	}

	req := llm.Request{
		Model:             s.cfg.Model,
		SystemPrompt:      tmpl.System,
		UserPrompt:        userPrompt,
		Temperature:       s.cfg.Temperature,
		MaxTokens:         scaledTokens,
		CacheSystemPrompt: true,
		Stream:            true,
	}

	s.log.Info("EvaluationService: calling LLM",
		"date", tag,
		"list_type", string(listType),
		"max_output_tokens", scaledTokens,
		"enriched_tickers", len(enrichedSnaps),
		"cache_enabled", true,
		"stream_enabled", true,
	)

	var resp llm.Response
	llmCallStart := time.Now()
	var attempt int
	for attempt = 0; attempt < 2; attempt++ {
		resp, err = s.provider.Generate(ctx, &req)
		if err == nil {
			break
		}
		if attempt == 0 {
			s.log.Warn("EvaluationService: LLM call failed, retrying in 5s",
				"date", tag, "list_type", string(listType), "error", err, "attempt", 1,
			)
			time.Sleep(5 * time.Second)
		} else {
			s.log.Warn("EvaluationService: LLM call failed on retry",
				"date", tag, "list_type", string(listType), "error", err, "attempt", 2,
			)
		}
	}
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(s.provider.Name(), "unknown", "error", string(listType)).Inc()
		return nil, fmt.Errorf("LLM call failed for %s [%s] after 2 attempts: %w", listType, tag, err)
	}

	// Record metrics for successful LLM call
	llmCallDuration := time.Since(llmCallStart).Seconds()
	model := resp.Model
	if model == "" {
		model = "unknown"
	}
	metrics.LLMRequestsTotal.WithLabelValues(resp.Provider, model, "success", string(listType)).Inc()
	metrics.LLMRequestDuration.WithLabelValues(resp.Provider, model, string(listType)).Observe(llmCallDuration)
	metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "input", string(listType)).Add(float64(resp.InputTokens))
	metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "output", string(listType)).Add(float64(resp.OutputTokens))
	if resp.CacheReadTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "cache_read", string(listType)).Add(float64(resp.CacheReadTokens))
		metrics.LLMTokensCachedTotal.WithLabelValues(resp.Provider, model, string(listType)).Add(float64(resp.CacheReadTokens))
	}
	if resp.CacheCreationTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(resp.Provider, model, "cache_write", string(listType)).Add(float64(resp.CacheCreationTokens))
	}
	// Track uncached input tokens: total input minus cache_read.
	uncachedInput := resp.InputTokens - resp.CacheReadTokens
	if uncachedInput > 0 {
		metrics.LLMTokensUncachedTotal.WithLabelValues(resp.Provider, model, string(listType)).Add(float64(uncachedInput))
	}

	durationMs := int(time.Since(start).Milliseconds())

	s.log.Info("EvaluationService: LLM call completed",
		"date", tag,
		"list_type", string(listType),
		"provider", resp.Provider,
		"model", resp.Model,
		"input_tokens", resp.InputTokens,
		"output_tokens", resp.OutputTokens,
		"cache_read_tokens", resp.CacheReadTokens,
		"cache_creation_tokens", resp.CacheCreationTokens,
		"duration_ms", durationMs,
		"response_len", len(resp.Text),
		"attempt", attempt+1,
	)

	// ── 7. De-anonymise LLM response & parse structured output ──────────────
	deAnonymisedResponse := DeAnonymizeResponse(resp.Text, tickerMap)

	// Try JSON parser first (all list types now have MANDATORY OUTPUT CONTRACT).
	// Fall back to regex-v1 if no JSON block found (backward compatibility).
	var parsed *models.EvaluationParsedOutput
	jsonResult := ParseMomentumResponse(resp.Text, tickerMap.ToReal, "")
	if jsonResult.ParseSuccess || jsonResult.RawJSON != "" {
		parsed = momentumResultToEvaluation(jsonResult)
	} else {
		// Fallback: legacy regex parser for responses without JSON block.
		parsed = ParseEvaluationResponse(deAnonymisedResponse)
		if parsed.ParseSuccess {
			s.log.Warn("EvaluationService: fell back to regex-v1 parser (no JSON block found)",
				"date", tag, "list_type", string(listType))
		}
	}
	parsedJSON, _ := json.Marshal(parsed)

	outputTickers := make([]string, 0, len(parsed.Tickers))
	for _, t := range parsed.Tickers { //nolint:gocritic // each iteration copies 184 bytes; lifetime of this loop is a few iterations
		outputTickers = append(outputTickers, t.Ticker)
	}

	s.log.Info("EvaluationService: response parsed",
		"date", tag,
		"list_type", string(listType),
		"parsed_tickers", len(parsed.Tickers),
		"parse_success", parsed.ParseSuccess,
		"parse_errors", len(parsed.ParseErrors),
	)

	// ── 8. Compute content-hash version ──────────────────────────────────────
	hashedVersion := ComputePromptVersionHash(PromptVersion, tmpl.System, tmpl.User)

	return &EvaluationResult{
		Date:          date,
		ListType:      listType,
		Provider:      resp.Provider,
		Model:         resp.Model,
		PromptVersion: hashedVersion,
		SystemPrompt:  tmpl.System,
		UserPrompt:    userPrompt,
		RawResponse:   deAnonymisedResponse,
		InputTickers:  tickers,
		OutputTickers: outputTickers,
		ParsedJSON:    parsedJSON,
		VariantName:   "primary",
		InputTokens:   resp.InputTokens,
		OutputTokens:  resp.OutputTokens,
		DurationMs:    durationMs,
	}, nil
}

// EvaluateListWithVariant runs a shadow evaluation using a custom prompt variant.
// The result is stored with the variant's prompt_version and variant_name.
func (s *EvaluationService) EvaluateListWithVariant(
	ctx context.Context,
	date time.Time,
	listType models.ListType,
	tickers []string,
	variant models.PromptVariant, //nolint:gocritic // 144 bytes; pointer would complicate lifecycle
) (*EvaluationResult, error) {
	tag := date.Format("2006-01-02")
	start := time.Now()

	s.log.Info("EvaluationService: starting shadow evaluation",
		"date", tag,
		"list_type", string(listType),
		"variant", variant.VariantName,
		"input_tickers", tickers,
	)

	// Load snapshot data.
	allSnaps, err := s.snapshots.ListByDate(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("load snapshots for shadow %s [%s]: %w", listType, tag, err)
	}
	source := screenerSourceForList[listType]
	snapMap := buildSnapshotMap(allSnaps, source)

	var enrichedSnaps []models.TradingViewSnapshotDaily
	for _, ticker := range tickers {
		if snap, ok := snapMap[ticker]; ok {
			enrichedSnaps = append(enrichedSnaps, snap)
		}
	}
	if len(enrichedSnaps) == 0 {
		return nil, fmt.Errorf("no snapshot data for shadow eval %s [%s]", listType, tag)
	}

	regime, _ := s.regime.GetMarketRegimeDaily(ctx, date)
	sectorScores, _ := s.sectorScores.GetSectorScores(ctx, date)

	var candlesByTicker map[string][]models.CandleDaily
	if s.candles != nil {
		candlesByTicker = make(map[string][]models.CandleDaily, len(enrichedSnaps))
		from := date.AddDate(0, 0, -35)
		for i := range enrichedSnaps {
			candles, cErr := s.candles.GetCandles(ctx, enrichedSnaps[i].Ticker, from, date)
			if cErr == nil && len(candles) > 0 {
				candlesByTicker[enrichedSnaps[i].Ticker] = candles
			}
		}
	}

	// Load narrative velocity for shadow evaluation (optional).
	var narrativeVelocity map[string]float64
	if s.narrativeVelocity != nil {
		velMap, velErr := s.narrativeVelocity.GetByDate(ctx, date)
		if velErr == nil {
			narrativeVelocity = make(map[string]float64, len(velMap))
			for ticker, nv := range velMap { //nolint:gocritic // map iteration always copies value
				narrativeVelocity[ticker] = nv.NarrativeVelocity
			}
		}
	}

	userPrompt, tickerMap := renderUserPrompt(variant.UserTemplate, date, listType, enrichedSnaps, regime, sectorScores, candlesByTicker, narrativeVelocity, "")

	scaledTokens := len(enrichedSnaps) * 1400
	if scaledTokens < 4000 {
		scaledTokens = 4000
	}
	if scaledTokens > s.cfg.MaxTokens {
		scaledTokens = s.cfg.MaxTokens
	}

	req := llm.Request{
		Model:             s.cfg.Model,
		SystemPrompt:      variant.SystemPrompt,
		UserPrompt:        userPrompt,
		Temperature:       s.cfg.Temperature,
		MaxTokens:         scaledTokens,
		CacheSystemPrompt: true,
		Stream:            true,
	}

	var resp llm.Response
	for attempt := 0; attempt < 2; attempt++ {
		resp, err = s.provider.Generate(ctx, &req)
		if err == nil {
			break
		}
		if attempt == 0 {
			time.Sleep(5 * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("shadow LLM call failed for %s [%s]: %w", listType, tag, err)
	}

	durationMs := int(time.Since(start).Milliseconds())

	deAnonymisedResp := DeAnonymizeResponse(resp.Text, tickerMap)

	var parsed *models.EvaluationParsedOutput
	jsonResult := ParseMomentumResponse(resp.Text, tickerMap.ToReal, "")
	if jsonResult.ParseSuccess || jsonResult.RawJSON != "" {
		parsed = momentumResultToEvaluation(jsonResult)
	} else {
		parsed = ParseEvaluationResponse(deAnonymisedResp)
	}
	parsedJSON, _ := json.Marshal(parsed)

	outputTickers := make([]string, 0, len(parsed.Tickers))
	for _, t := range parsed.Tickers { //nolint:gocritic // each iteration copies 184 bytes; small slice
		outputTickers = append(outputTickers, t.Ticker)
	}

	s.log.Info("EvaluationService: shadow evaluation completed",
		"date", tag,
		"list_type", string(listType),
		"variant", variant.VariantName,
		"parsed_tickers", len(parsed.Tickers),
		"duration_ms", durationMs,
	)

	return &EvaluationResult{
		Date:          date,
		ListType:      listType,
		Provider:      resp.Provider,
		Model:         resp.Model,
		PromptVersion: variant.PromptVersion,
		SystemPrompt:  variant.SystemPrompt,
		UserPrompt:    userPrompt,
		RawResponse:   deAnonymisedResp,
		InputTickers:  tickers,
		OutputTickers: outputTickers,
		ParsedJSON:    parsedJSON,
		VariantName:   variant.VariantName,
		InputTokens:   resp.InputTokens,
		OutputTokens:  resp.OutputTokens,
		DurationMs:    durationMs,
	}, nil
}

// buildSnapshotMap indexes snapshots by ticker for a specific screener source.
func buildSnapshotMap(snaps []models.TradingViewSnapshotDaily, source models.ScreenerSource) map[string]models.TradingViewSnapshotDaily {
	m := make(map[string]models.TradingViewSnapshotDaily)
	for _, s := range snaps { //nolint:gocritic // each iteration copies 392 bytes; iterating over 20-40 items
		if s.ScreenerSource == source {
			m[s.Ticker] = s
		}
	}
	return m
}

// buildRAGContextSummary constructs a pgvector.Vector embedding from the current
// evaluation context for similarity search against historical prompt memories.
//
// When embedder is non-nil, it calls the real embedding model (text-embedding-3-small).
// When embedder is nil, it returns a zero-vector placeholder (RAG disabled or no API key).
func buildRAGContextSummary(
	ctx context.Context,
	snaps []models.TradingViewSnapshotDaily,
	regime *models.MarketRegimeDaily,
	listType models.ListType,
	embedder *EmbeddingService,
	log *slog.Logger,
) pgvector.Vector {
	// Build a text summary of the current context for embedding.
	var sb strings.Builder
	fmt.Fprintf(&sb, "List type: %s. ", listType)
	if regime != nil && regime.Confidence != nil {
		fmt.Fprintf(&sb, "Regime: %s (confidence: %.0f%%). ", regime.Regime, *regime.Confidence)
	} else if regime != nil {
		fmt.Fprintf(&sb, "Regime: %s. ", regime.Regime)
	}
	if len(snaps) > 0 {
		sb.WriteString("Tickers: ")
		for i, snap := range snaps { //nolint:gocritic // each iteration copies 392 bytes; small slice used for text building
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(snap.Ticker)
			if i >= 4 {
				sb.WriteString("...")
				break
			}
		}
		sb.WriteString(". ")
	}
	contextText := sb.String()

	if embedder != nil {
		vec, err := embedder.Embed(ctx, contextText)
		if err != nil {
			log.Warn("buildRAGContextSummary: embedding failed, using zero-vector fallback",
				"error", err,
			)
			return pgvector.NewVector(make([]float32, 1536))
		}
		log.Debug("embedding_generated",
			"ticker", len(snaps),
			"dimensions", 1536,
			"list_type", listType,
		)
		return vec
	}

	// No embedder: return zero-vector placeholder.
	return pgvector.NewVector(make([]float32, 1536))
}

// buildRAGContextString formats prompt memories into a compact context block
// for injection into the user prompt template via the {{rag_context}} placeholder.
// Returns an empty string if no memories are available.
func buildRAGContextString(memories []models.PromptMemory) string {
	if len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## PAST SIMILAR EVALUATIONS (RAG context)\n")
	sb.WriteString("The following are past evaluations with similar market context and verified outcomes.\n")
	sb.WriteString("Use them as reference for base rates and pattern recognition.\n\n")
	for i, m := range memories { //nolint:gocritic // each iteration copies 256 bytes; max 3 items
		fmt.Fprintf(&sb, "### Memory %d — %s (%s)\n", i+1, m.Date.Format("2006-01-02"), m.Ticker)
		fmt.Fprintf(&sb, "- List type: %s\n", m.ListType)
		fmt.Fprintf(&sb, "- Context: %s\n", truncateString(m.ContextSummary, 100))
		if m.LLMSetup != nil {
			fmt.Fprintf(&sb, "- Setup: %s\n", *m.LLMSetup)
		}
		if m.LLMConviction != nil {
			fmt.Fprintf(&sb, "- Conviction: %s\n", *m.LLMConviction)
		}
		if m.OutcomeReturn5D != nil {
			fmt.Fprintf(&sb, "- Return 5D: %+.1f%%\n", *m.OutcomeReturn5D*100)
		}
		if m.OutcomeStopHit != nil {
			fmt.Fprintf(&sb, "- Stop hit: %t\n", *m.OutcomeStopHit)
		}
		if m.OutcomeTargetHit != nil {
			fmt.Fprintf(&sb, "- Target hit: %t\n", *m.OutcomeTargetHit)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// truncateString truncates a string to maxLen characters, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stripNilInterface returns nil if the interface wraps a nil concrete pointer.
// This prevents the Go interface footgun where (*T)(nil) passed as interface
// is non-nil (type != nil, value == nil), causing nil-pointer dereference
// when methods are called on the receiver.
//
// Uses reflection to check if the underlying value is a nil pointer, nil map,
// nil slice, nil chan, or nil func.
func stripNilInterface[T any](v T) T {
	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Pointer,
		reflect.Interface,
		reflect.Map,
		reflect.Slice,
		reflect.Chan,
		reflect.Func:
		if rv.IsNil() {
			var zero T
			return zero
		}
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Array,
		reflect.String,
		reflect.Struct,
		reflect.UnsafePointer:
		// Non-nilable or non-nil-checked kinds — pass through unchanged.
	}

	return v
}

// ToModel converts an EvaluationResult into a database model ready for persistence.
func (r *EvaluationResult) ToModel() *models.LLMListEvaluation {
	return &models.LLMListEvaluation{
		Date:          r.Date,
		ListType:      r.ListType,
		Provider:      r.Provider,
		Model:         r.Model,
		PromptVersion: r.PromptVersion,
		SystemPrompt:  r.SystemPrompt,
		UserPrompt:    r.UserPrompt,
		RawResponse:   r.RawResponse,
		ParsedJSON:    json.RawMessage(r.ParsedJSON),
		InputTickers:  r.InputTickers,
		OutputTickers: r.OutputTickers,
		VariantName:   r.VariantName,
		InputTokens:   new(r.InputTokens),
		OutputTokens:  new(r.OutputTokens),
		DurationMs:    new(r.DurationMs),
	}
}
