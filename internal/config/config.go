// Package config loads all runtime configuration from environment variables.
// Call Load() once at program startup; pass *Config through the dependency graph.
package config

import (
	"fmt"
	"os"
	"strconv"

	"ai-stock-service/internal/llm"

	"github.com/joho/godotenv"
)

// Config holds every configurable value for the service.
// All fields are populated from environment variables; see .env.example for docs.
type Config struct {
	// Database
	DatabaseURL string

	// Market data provider: "polygon" | "twelvedata"
	MarketDataProvider string
	PolygonAPIKey      string
	TwelveDataAPIKey   string

	// Rate limits (requests per minute). 0 → use provider default.
	PolygonRequestsPerMin    int // default 5  (free tier)
	TwelveDataRequestsPerMin int // default 8  (free tier)

	// Max parallel goroutines during backfill / daily-update jobs.
	MarketDataConcurrency int // default 5

	// MarketDataWorkerCount is the number of concurrent goroutines in the
	// nightly async candle ingestion worker pool.
	// Default 1 (free-tier: 5 req/min ÷ ~10 s per request ≈ 1 useful worker).
	// Increase when on a paid API tier with higher RPM limits.
	MarketDataWorkerCount int // default 1

	// LLM provider: "openai" | "anthropic" | "gemini"
	LLMProvider     string
	OpenAIAPIKey    string
	AnthropicAPIKey string
	GeminiAPIKey    string
	LLMBaseURL      string // optional override for LLM API endpoint (proxy/testing)

	// LLM list evaluation settings (Step 9).
	LLMModel                string  // model override, e.g. "gpt-4.1-mini" (empty → provider default)
	LLMMaxTokens            int     // max response tokens for evaluations (default 8000)
	LLMTemperature          float64 // temperature for evaluations (default 0.2)
	LLMEvalEnabled          bool    // feature toggle: set LLM_EVAL_ENABLED=true to activate Step 9
	CommercialReportEnabled bool    // feature toggle: set COMMERCIAL_REPORT_ENABLED=true to activate Step 12

	// Separate LLM config for commercial report transformation (Step 12).
	CommercialLLMProvider string // COMMERCIAL_LLM_PROVIDER — default "openai"
	CommercialLLMModel    string // COMMERCIAL_LLM_MODEL — default "" (provider default)

	// TVCollectorURL is the base URL of the tv-collector HTTP service.
	// When set, the nightly pipeline Step 1 calls POST {TVCollectorURL}/run to
	// trigger a TradingView screener collection and waits for it to complete.
	// If empty, Step 1 is skipped with a warning (backward-compat mode).
	// Example: http://tv-collector:8001
	TVCollectorURL string

	// Runtime
	LogLevel string // "debug" | "info" | "warn" | "error"
	AppEnv   string // "development" | "production"

	// API authentication. When non-empty, all /api/v1/* requests must include
	// an "Authorization: Bearer <key>" header matching this value.
	// When empty, auth is disabled (convenient for local dev).
	APIKey string

	// Enrichment (Massive.com profile + news enrichment pipeline).
	// Uses the same PolygonProvider since Massive.com IS rebranded Polygon.io.
	// Set ENRICHMENT_ENABLED=true to activate the enrichment pipeline step.
	EnrichmentEnabled bool

	// Pre-market catalyst surge scanner (separate pipeline from nightly).
	PremarketEnabled            bool   // PREMARKET_ENABLED
	PremarketCronSchedule       string // PREMARKET_CRON_SCHEDULE (UTC cron, default "30 11 * * 1-5" = 07:30 ET)
	PremarketLLMCatalystEnabled bool   // PREMARKET_LLM_CATALYST_ENABLED (use LLM for catalyst verification)

	// Finnhub news fallback (free tier: 60 req/min).
	FinnhubAPIKey string // FINNHUB_API_KEY

	// Prompt memory (pgvector-based retrieval of past evaluations).
	PromptMemoryEnabled bool // PROMPT_MEMORY_ENABLED

	// R-12: Premonition model (XGBoost/LightGBM) refit settings.
	PremonitionBackend string  // PREMONITION_BACKEND — "xgboost" | "lightgbm" (default "xgboost")
	PremonitionMinAUC  float64 // PREMONITION_MIN_AUC — default 0.65
	PremonitionTmpDir  string  // PREMONITION_TMP_DIR — default "/tmp/premonition"
	PremonitionEnabled bool    // PREMONITION_ENABLED — feature toggle

	// R-12: Embedding generator settings.
	EmbeddingBackend     string // EMBEDDING_BACKEND — "python" | "http" | "noop" (default "noop")
	EmbeddingModel       string // EMBEDDING_MODEL — default "all-MiniLM-L6-v2"
	EmbeddingEndpointURL string // EMBEDDING_ENDPOINT_URL — for HTTP backend

	// R-12: RAG settings.
	RAGEnabled    bool // RAG_ENABLED — feature toggle for RAG injection
	RAGTopK       int  // RAG_TOP_K — default 3
	RAGMaxAgeDays int  // RAG_MAX_AGE_DAYS — default 365
}

// Load reads .env (if present) then overlays actual environment variables.
// Returns an error only when a required variable is missing.
func Load() (*Config, error) {
	// .env is optional – silently ignored when absent (e.g. inside Docker).
	_ = godotenv.Load()

	databaseURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return nil, err
	}

	return &Config{
		DatabaseURL:        databaseURL,
		MarketDataProvider: getEnv("MARKET_DATA_PROVIDER", "polygon"),
		PolygonAPIKey:      os.Getenv("POLYGON_API_KEY"),
		TwelveDataAPIKey:   os.Getenv("TWELVEDATA_API_KEY"),

		PolygonRequestsPerMin:    getEnvInt("POLYGON_REQUESTS_PER_MIN", 5),
		TwelveDataRequestsPerMin: getEnvInt("TWELVEDATA_REQUESTS_PER_MIN", 8),
		MarketDataConcurrency:    getEnvInt("MARKET_DATA_CONCURRENCY", 5),
		MarketDataWorkerCount:    getEnvInt("MARKET_DATA_WORKER_COUNT", 1),

		LLMProvider:     getEnv("LLM_PROVIDER", "anthropic"),
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		GeminiAPIKey:    os.Getenv("GEMINI_API_KEY"),
		LLMBaseURL:      os.Getenv("LLM_BASE_URL"),

		LLMModel:                os.Getenv("LLM_MODEL"),
		LLMMaxTokens:            getEnvInt("LLM_MAX_TOKENS", 8000),
		LLMTemperature:          getEnvFloat("LLM_TEMPERATURE", 0.2),
		LLMEvalEnabled:          getEnv("LLM_EVAL_ENABLED", "false") == "true",
		CommercialReportEnabled: getEnv("COMMERCIAL_REPORT_ENABLED", "false") == "true",

		CommercialLLMProvider: getEnv("COMMERCIAL_LLM_PROVIDER", "openai"),
		CommercialLLMModel:    os.Getenv("COMMERCIAL_LLM_MODEL"),

		TVCollectorURL: os.Getenv("TV_COLLECTOR_URL"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		AppEnv:         getEnv("APP_ENV", "development"),
		APIKey:         os.Getenv("API_KEY"),

		EnrichmentEnabled: getEnv("ENRICHMENT_ENABLED", "false") == "true",

		PremarketEnabled:            getEnv("PREMARKET_ENABLED", "false") == "true",
		PremarketCronSchedule:       getEnv("PREMARKET_CRON_SCHEDULE", "30 11 * * 1-5"),
		PremarketLLMCatalystEnabled: getEnv("PREMARKET_LLM_CATALYST_ENABLED", "true") == "true",
		FinnhubAPIKey:               os.Getenv("FINNHUB_API_KEY"),

		PromptMemoryEnabled: getEnv("PROMPT_MEMORY_ENABLED", "false") == "true",

		PremonitionBackend: getEnv("PREMONITION_BACKEND", "xgboost"),
		PremonitionMinAUC:  getEnvFloat("PREMONITION_MIN_AUC", 0.65),
		PremonitionTmpDir:  getEnv("PREMONITION_TMP_DIR", "/tmp/premonition"),
		PremonitionEnabled: getEnv("PREMONITION_ENABLED", "false") == "true",

		EmbeddingBackend:     getEnv("EMBEDDING_BACKEND", "noop"),
		EmbeddingModel:       getEnv("EMBEDDING_MODEL", "all-MiniLM-L6-v2"),
		EmbeddingEndpointURL: os.Getenv("EMBEDDING_ENDPOINT_URL"),

		RAGEnabled:    getEnv("RAG_ENABLED", "false") == "true",
		RAGTopK:       getEnvInt("RAG_TOP_K", 3),
		RAGMaxAgeDays: getEnvInt("RAG_MAX_AGE_DAYS", 365),
	}, nil
}

// LLMConfig returns an llm.Config populated from the application config,
// selecting the correct API key for the active LLM provider.
func (c *Config) LLMConfig() llm.Config {
	var apiKey string
	switch c.LLMProvider {
	case "anthropic":
		apiKey = c.AnthropicAPIKey
	case "gemini":
		apiKey = c.GeminiAPIKey
	default: // "openai" and any future default
		apiKey = c.OpenAIAPIKey
	}
	return llm.Config{
		Provider: c.LLMProvider,
		APIKey:   apiKey,
		BaseURL:  c.LLMBaseURL,
	}
}

// CommercialLLMConfig returns an llm.Config for the commercial report provider,
// which may differ from the main evaluation provider.
func (c *Config) CommercialLLMConfig() llm.Config {
	var apiKey string
	switch c.CommercialLLMProvider {
	case "anthropic":
		apiKey = c.AnthropicAPIKey
	case "gemini":
		apiKey = c.GeminiAPIKey
	default:
		apiKey = c.OpenAIAPIKey
	}
	return llm.Config{
		Provider: c.CommercialLLMProvider,
		APIKey:   apiKey,
		BaseURL:  c.LLMBaseURL,
	}
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable %q is not set", key)
	}
	return v, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
