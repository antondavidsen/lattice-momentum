// File: internal/config/config_test.go
package config

import (
	"ai-stock-service/internal/llm"
	"os"
	"testing"
)

// clearConfigEnv removes all environment variables that config.Load() reads.
func clearConfigEnv() {
	envs := []string{
		"DATABASE_URL",
		"MARKET_DATA_PROVIDER",
		"POLYGON_API_KEY",
		"TWELVEDATA_API_KEY",
		"POLYGON_REQUESTS_PER_MIN",
		"TWELVEDATA_REQUESTS_PER_MIN",
		"MARKET_DATA_CONCURRENCY",
		"MARKET_DATA_WORKER_COUNT",
		"LLM_PROVIDER",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GEMINI_API_KEY",
		"LLM_BASE_URL",
		"LLM_MODEL",
		"LLM_MAX_TOKENS",
		"LLM_TEMPERATURE",
		"LLM_EVAL_ENABLED",
		"COMMERCIAL_REPORT_ENABLED",
		"COMMERCIAL_LLM_PROVIDER",
		"COMMERCIAL_LLM_MODEL",
		"TV_COLLECTOR_URL",
		"LOG_LEVEL",
		"APP_ENV",
		"API_KEY",
		"ENRICHMENT_ENABLED",
		"PREMARKET_ENABLED",
		"PREMARKET_CRON_SCHEDULE",
		"PREMARKET_LLM_CATALYST_ENABLED",
		"FINNHUB_API_KEY",
		"PROMPT_MEMORY_ENABLED",
		"PREMONITION_BACKEND",
		"PREMONITION_MIN_AUC",
		"PREMONITION_TMP_DIR",
		"PREMONITION_ENABLED",
		"EMBEDDING_BACKEND",
		"EMBEDDING_MODEL",
		"EMBEDDING_ENDPOINT_URL",
		"RAG_ENABLED",
		"RAG_TOP_K",
		"RAG_MAX_AGE_DAYS",
	}
	for _, e := range envs {
		_ = os.Unsetenv(e)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearConfigEnv()
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing, got nil")
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost/test" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://localhost/test")
	}
	if cfg.MarketDataProvider != "polygon" {
		t.Errorf("MarketDataProvider = %q, want %q", cfg.MarketDataProvider, "polygon")
	}
	if cfg.PolygonRequestsPerMin != 5 {
		t.Errorf("PolygonRequestsPerMin = %d, want %d", cfg.PolygonRequestsPerMin, 5)
	}
	if cfg.TwelveDataRequestsPerMin != 8 {
		t.Errorf("TwelveDataRequestsPerMin = %d, want %d", cfg.TwelveDataRequestsPerMin, 8)
	}
	if cfg.MarketDataConcurrency != 5 {
		t.Errorf("MarketDataConcurrency = %d, want %d", cfg.MarketDataConcurrency, 5)
	}
	if cfg.MarketDataWorkerCount != 1 {
		t.Errorf("MarketDataWorkerCount = %d, want %d", cfg.MarketDataWorkerCount, 1)
	}
	if cfg.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider = %q, want %q", cfg.LLMProvider, "anthropic")
	}
	if cfg.LLMMaxTokens != 8000 {
		t.Errorf("LLMMaxTokens = %d, want %d", cfg.LLMMaxTokens, 8000)
	}
	if cfg.LLMTemperature != 0.2 {
		t.Errorf("LLMTemperature = %v, want %v", cfg.LLMTemperature, 0.2)
	}
	if cfg.LLMEvalEnabled != false {
		t.Errorf("LLMEvalEnabled = %v, want %v", cfg.LLMEvalEnabled, false)
	}
	if cfg.CommercialReportEnabled != false {
		t.Errorf("CommercialReportEnabled = %v, want %v", cfg.CommercialReportEnabled, false)
	}
	if cfg.CommercialLLMProvider != "openai" {
		t.Errorf("CommercialLLMProvider = %q, want %q", cfg.CommercialLLMProvider, "openai")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, "development")
	}
	if cfg.EnrichmentEnabled != false {
		t.Errorf("EnrichmentEnabled = %v, want %v", cfg.EnrichmentEnabled, false)
	}
	if cfg.PremarketEnabled != false {
		t.Errorf("PremarketEnabled = %v, want %v", cfg.PremarketEnabled, false)
	}
	if cfg.PremarketCronSchedule != "30 11 * * 1-5" {
		t.Errorf("PremarketCronSchedule = %q, want %q", cfg.PremarketCronSchedule, "30 11 * * 1-5")
	}
	if cfg.PremarketLLMCatalystEnabled != true {
		t.Errorf("PremarketLLMCatalystEnabled = %v, want %v", cfg.PremarketLLMCatalystEnabled, true)
	}
	if cfg.PromptMemoryEnabled != false {
		t.Errorf("PromptMemoryEnabled = %v, want %v", cfg.PromptMemoryEnabled, false)
	}
	// R-12 defaults
	if cfg.PremonitionBackend != "xgboost" {
		t.Errorf("PremonitionBackend = %q, want %q", cfg.PremonitionBackend, "xgboost")
	}
	if cfg.PremonitionMinAUC != 0.65 {
		t.Errorf("PremonitionMinAUC = %v, want %v", cfg.PremonitionMinAUC, 0.65)
	}
	if cfg.PremonitionTmpDir != "/tmp/premonition" {
		t.Errorf("PremonitionTmpDir = %q, want %q", cfg.PremonitionTmpDir, "/tmp/premonition")
	}
	if cfg.PremonitionEnabled != false {
		t.Errorf("PremonitionEnabled = %v, want %v", cfg.PremonitionEnabled, false)
	}
	if cfg.EmbeddingBackend != "noop" {
		t.Errorf("EmbeddingBackend = %q, want %q", cfg.EmbeddingBackend, "noop")
	}
	if cfg.EmbeddingModel != "all-MiniLM-L6-v2" {
		t.Errorf("EmbeddingModel = %q, want %q", cfg.EmbeddingModel, "all-MiniLM-L6-v2")
	}
	if cfg.RAGEnabled != false {
		t.Errorf("RAGEnabled = %v, want %v", cfg.RAGEnabled, false)
	}
	if cfg.RAGTopK != 3 {
		t.Errorf("RAGTopK = %d, want %d", cfg.RAGTopK, 3)
	}
	if cfg.RAGMaxAgeDays != 365 {
		t.Errorf("RAGMaxAgeDays = %d, want %d", cfg.RAGMaxAgeDays, 365)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://custom/db")
	t.Setenv("MARKET_DATA_PROVIDER", "twelvedata")
	t.Setenv("POLYGON_API_KEY", "poly-key")
	t.Setenv("TWELVEDATA_API_KEY", "td-key")
	t.Setenv("POLYGON_REQUESTS_PER_MIN", "10")
	t.Setenv("TWELVEDATA_REQUESTS_PER_MIN", "15")
	t.Setenv("MARKET_DATA_CONCURRENCY", "20")
	t.Setenv("MARKET_DATA_WORKER_COUNT", "3")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("LLM_BASE_URL", "http://llm-proxy:8080")
	t.Setenv("LLM_MODEL", "gpt-4")
	t.Setenv("LLM_MAX_TOKENS", "4000")
	t.Setenv("LLM_TEMPERATURE", "0.5")
	t.Setenv("LLM_EVAL_ENABLED", "true")
	t.Setenv("COMMERCIAL_REPORT_ENABLED", "true")
	t.Setenv("COMMERCIAL_LLM_PROVIDER", "anthropic")
	t.Setenv("COMMERCIAL_LLM_MODEL", "claude-3")
	t.Setenv("TV_COLLECTOR_URL", "http://tv:8001")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("APP_ENV", "production")
	t.Setenv("API_KEY", "secret-api-key")
	t.Setenv("ENRICHMENT_ENABLED", "true")
	t.Setenv("PREMARKET_ENABLED", "true")
	t.Setenv("PREMARKET_CRON_SCHEDULE", "0 12 * * 1-5")
	t.Setenv("PREMARKET_LLM_CATALYST_ENABLED", "false")
	t.Setenv("FINNHUB_API_KEY", "finnhub-key")
	t.Setenv("PROMPT_MEMORY_ENABLED", "true")
	t.Setenv("PREMONITION_BACKEND", "lightgbm")
	t.Setenv("PREMONITION_MIN_AUC", "0.75")
	t.Setenv("PREMONITION_TMP_DIR", "/custom/cache")
	t.Setenv("PREMONITION_ENABLED", "true")
	t.Setenv("EMBEDDING_BACKEND", "python")
	t.Setenv("EMBEDDING_MODEL", "custom-model")
	t.Setenv("EMBEDDING_ENDPOINT_URL", "http://embed:8080")
	t.Setenv("RAG_ENABLED", "true")
	t.Setenv("RAG_TOP_K", "10")
	t.Setenv("RAG_MAX_AGE_DAYS", "90")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://custom/db" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://custom/db")
	}
	if cfg.MarketDataProvider != "twelvedata" {
		t.Errorf("MarketDataProvider = %q, want %q", cfg.MarketDataProvider, "twelvedata")
	}
	if cfg.PolygonAPIKey != "poly-key" {
		t.Errorf("PolygonAPIKey = %q, want %q", cfg.PolygonAPIKey, "poly-key")
	}
	if cfg.TwelveDataAPIKey != "td-key" {
		t.Errorf("TwelveDataAPIKey = %q, want %q", cfg.TwelveDataAPIKey, "td-key")
	}
	if cfg.PolygonRequestsPerMin != 10 {
		t.Errorf("PolygonRequestsPerMin = %d, want %d", cfg.PolygonRequestsPerMin, 10)
	}
	if cfg.TwelveDataRequestsPerMin != 15 {
		t.Errorf("TwelveDataRequestsPerMin = %d, want %d", cfg.TwelveDataRequestsPerMin, 15)
	}
	if cfg.MarketDataConcurrency != 20 {
		t.Errorf("MarketDataConcurrency = %d, want %d", cfg.MarketDataConcurrency, 20)
	}
	if cfg.MarketDataWorkerCount != 3 {
		t.Errorf("MarketDataWorkerCount = %d, want %d", cfg.MarketDataWorkerCount, 3)
	}
	if cfg.LLMProvider != "openai" {
		t.Errorf("LLMProvider = %q, want %q", cfg.LLMProvider, "openai")
	}
	if cfg.OpenAIAPIKey != "openai-key" {
		t.Errorf("OpenAIAPIKey = %q, want %q", cfg.OpenAIAPIKey, "openai-key")
	}
	if cfg.AnthropicAPIKey != "anthropic-key" {
		t.Errorf("AnthropicAPIKey = %q, want %q", cfg.AnthropicAPIKey, "anthropic-key")
	}
	if cfg.GeminiAPIKey != "gemini-key" {
		t.Errorf("GeminiAPIKey = %q, want %q", cfg.GeminiAPIKey, "gemini-key")
	}
	if cfg.LLMBaseURL != "http://llm-proxy:8080" {
		t.Errorf("LLMBaseURL = %q, want %q", cfg.LLMBaseURL, "http://llm-proxy:8080")
	}
	if cfg.LLMModel != "gpt-4" {
		t.Errorf("LLMModel = %q, want %q", cfg.LLMModel, "gpt-4")
	}
	if cfg.LLMMaxTokens != 4000 {
		t.Errorf("LLMMaxTokens = %d, want %d", cfg.LLMMaxTokens, 4000)
	}
	if cfg.LLMTemperature != 0.5 {
		t.Errorf("LLMTemperature = %v, want %v", cfg.LLMTemperature, 0.5)
	}
	if cfg.LLMEvalEnabled != true {
		t.Errorf("LLMEvalEnabled = %v, want %v", cfg.LLMEvalEnabled, true)
	}
	if cfg.CommercialReportEnabled != true {
		t.Errorf("CommercialReportEnabled = %v, want %v", cfg.CommercialReportEnabled, true)
	}
	if cfg.CommercialLLMProvider != "anthropic" {
		t.Errorf("CommercialLLMProvider = %q, want %q", cfg.CommercialLLMProvider, "anthropic")
	}
	if cfg.CommercialLLMModel != "claude-3" {
		t.Errorf("CommercialLLMModel = %q, want %q", cfg.CommercialLLMModel, "claude-3")
	}
	if cfg.TVCollectorURL != "http://tv:8001" {
		t.Errorf("TVCollectorURL = %q, want %q", cfg.TVCollectorURL, "http://tv:8001")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.AppEnv != "production" {
		t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, "production")
	}
	if cfg.APIKey != "secret-api-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "secret-api-key")
	}
	if cfg.EnrichmentEnabled != true {
		t.Errorf("EnrichmentEnabled = %v, want %v", cfg.EnrichmentEnabled, true)
	}
	if cfg.PremarketEnabled != true {
		t.Errorf("PremarketEnabled = %v, want %v", cfg.PremarketEnabled, true)
	}
	if cfg.PremarketCronSchedule != "0 12 * * 1-5" {
		t.Errorf("PremarketCronSchedule = %q, want %q", cfg.PremarketCronSchedule, "0 12 * * 1-5")
	}
	if cfg.PremarketLLMCatalystEnabled != false {
		t.Errorf("PremarketLLMCatalystEnabled = %v, want %v", cfg.PremarketLLMCatalystEnabled, false)
	}
	if cfg.FinnhubAPIKey != "finnhub-key" {
		t.Errorf("FinnhubAPIKey = %q, want %q", cfg.FinnhubAPIKey, "finnhub-key")
	}
	if cfg.PromptMemoryEnabled != true {
		t.Errorf("PromptMemoryEnabled = %v, want %v", cfg.PromptMemoryEnabled, true)
	}
	// R-12 custom values
	if cfg.PremonitionBackend != "lightgbm" {
		t.Errorf("PremonitionBackend = %q, want %q", cfg.PremonitionBackend, "lightgbm")
	}
	if cfg.PremonitionMinAUC != 0.75 {
		t.Errorf("PremonitionMinAUC = %v, want %v", cfg.PremonitionMinAUC, 0.75)
	}
	if cfg.PremonitionTmpDir != "/custom/cache" {
		t.Errorf("PremonitionTmpDir = %q, want %q", cfg.PremonitionTmpDir, "/custom/cache")
	}
	if cfg.PremonitionEnabled != true {
		t.Errorf("PremonitionEnabled = %v, want %v", cfg.PremonitionEnabled, true)
	}
	if cfg.EmbeddingBackend != "python" {
		t.Errorf("EmbeddingBackend = %q, want %q", cfg.EmbeddingBackend, "python")
	}
	if cfg.EmbeddingModel != "custom-model" {
		t.Errorf("EmbeddingModel = %q, want %q", cfg.EmbeddingModel, "custom-model")
	}
	if cfg.EmbeddingEndpointURL != "http://embed:8080" {
		t.Errorf("EmbeddingEndpointURL = %q, want %q", cfg.EmbeddingEndpointURL, "http://embed:8080")
	}
	if cfg.RAGEnabled != true {
		t.Errorf("RAGEnabled = %v, want %v", cfg.RAGEnabled, true)
	}
	if cfg.RAGTopK != 10 {
		t.Errorf("RAGTopK = %d, want %d", cfg.RAGTopK, 10)
	}
	if cfg.RAGMaxAgeDays != 90 {
		t.Errorf("RAGMaxAgeDays = %d, want %d", cfg.RAGMaxAgeDays, 90)
	}
}

func TestLoad_InvalidIntFallsBackToDefault(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("POLYGON_REQUESTS_PER_MIN", "not-a-number")
	t.Setenv("TWELVEDATA_REQUESTS_PER_MIN", "also-not-a-number")
	t.Setenv("LLM_MAX_TOKENS", "bad")
	t.Setenv("RAG_TOP_K", "not-a-number")
	t.Setenv("RAG_MAX_AGE_DAYS", "invalid")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PolygonRequestsPerMin != 5 {
		t.Errorf("PolygonRequestsPerMin = %d, want fallback %d", cfg.PolygonRequestsPerMin, 5)
	}
	if cfg.TwelveDataRequestsPerMin != 8 {
		t.Errorf("TwelveDataRequestsPerMin = %d, want fallback %d", cfg.TwelveDataRequestsPerMin, 8)
	}
	if cfg.LLMMaxTokens != 8000 {
		t.Errorf("LLMMaxTokens = %d, want fallback %d", cfg.LLMMaxTokens, 8000)
	}
	if cfg.RAGTopK != 3 {
		t.Errorf("RAGTopK = %d, want fallback %d", cfg.RAGTopK, 3)
	}
	if cfg.RAGMaxAgeDays != 365 {
		t.Errorf("RAGMaxAgeDays = %d, want fallback %d", cfg.RAGMaxAgeDays, 365)
	}
}

func TestLoad_InvalidFloatFallsBackToDefault(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("LLM_TEMPERATURE", "not-a-float")
	t.Setenv("PREMONITION_MIN_AUC", "also-bad")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLMTemperature != 0.2 {
		t.Errorf("LLMTemperature = %v, want fallback %v", cfg.LLMTemperature, 0.2)
	}
	if cfg.PremonitionMinAUC != 0.65 {
		t.Errorf("PremonitionMinAUC = %v, want fallback %v", cfg.PremonitionMinAUC, 0.65)
	}
}

// TestLoad_EmptyIntString verifies that empty env vars fall back to defaults.
func TestLoad_EmptyIntString(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("POLYGON_REQUESTS_PER_MIN", "")
	t.Setenv("LLM_MAX_TOKENS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PolygonRequestsPerMin != 5 {
		t.Errorf("PolygonRequestsPerMin = %d, want fallback %d", cfg.PolygonRequestsPerMin, 5)
	}
	if cfg.LLMMaxTokens != 8000 {
		t.Errorf("LLMMaxTokens = %d, want fallback %d", cfg.LLMMaxTokens, 8000)
	}
}

// TestLoad_NegativeInts verifies negative integer values are accepted.
func TestLoad_NegativeInts(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("POLYGON_REQUESTS_PER_MIN", "-1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PolygonRequestsPerMin != -1 {
		t.Errorf("PolygonRequestsPerMin = %d, want -1", cfg.PolygonRequestsPerMin)
	}
}

// TestLoad_EmptyStringFields verifies that string fields default to empty.
func TestLoad_EmptyStringFields(t *testing.T) {
	clearConfigEnv()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PolygonAPIKey != "" {
		t.Errorf("PolygonAPIKey = %q, want empty", cfg.PolygonAPIKey)
	}
	if cfg.TwelveDataAPIKey != "" {
		t.Errorf("TwelveDataAPIKey = %q, want empty", cfg.TwelveDataAPIKey)
	}
	if cfg.OpenAIAPIKey != "" {
		t.Errorf("OpenAIAPIKey = %q, want empty", cfg.OpenAIAPIKey)
	}
	if cfg.AnthropicAPIKey != "" {
		t.Errorf("AnthropicAPIKey = %q, want empty", cfg.AnthropicAPIKey)
	}
	if cfg.GeminiAPIKey != "" {
		t.Errorf("GeminiAPIKey = %q, want empty", cfg.GeminiAPIKey)
	}
	if cfg.LLMBaseURL != "" {
		t.Errorf("LLMBaseURL = %q, want empty", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "" {
		t.Errorf("LLMModel = %q, want empty", cfg.LLMModel)
	}
	if cfg.TVCollectorURL != "" {
		t.Errorf("TVCollectorURL = %q, want empty", cfg.TVCollectorURL)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
	if cfg.FinnhubAPIKey != "" {
		t.Errorf("FinnhubAPIKey = %q, want empty", cfg.FinnhubAPIKey)
	}
}

func TestLoad_BooleanVariations(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{"true lowercase", "true", true},
		{"TRUE uppercase", "TRUE", true},
		{"True mixedcase", "True", true},
		{"1", "1", true},
		{"t", "t", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"empty", "", false},
		{"false", "false", false},
		{"whitespace", "  true  ", true},
		{"unrecognized", "maybe", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv()
			t.Setenv("DATABASE_URL", "postgres://localhost/test")
			t.Setenv("LLM_EVAL_ENABLED", tt.envValue)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.LLMEvalEnabled != tt.want {
				t.Errorf("LLMEvalEnabled = %v, want %v", cfg.LLMEvalEnabled, tt.want)
			}
		})
	}
}

func assertLLMConfig(t *testing.T, got llm.Config, wantProvider, wantKey, wantBaseURL string) {
	t.Helper()
	if got.Provider != wantProvider {
		t.Errorf("Provider = %q, want %q", got.Provider, wantProvider)
	}
	if got.APIKey != wantKey {
		t.Errorf("APIKey = %q, want %q", got.APIKey, wantKey)
	}
	if got.BaseURL != wantBaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, wantBaseURL)
	}
}

func TestConfig_LLMConfig(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantKey  string
	}{
		{"anthropic", "anthropic", "anthropic-key"},
		{"gemini", "gemini", "gemini-key"},
		{"openai", "openai", "openai-key"},
		{"unknown defaults to openai", "unknown", "openai-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ //nolint:gosec // test keys, not real credentials
				LLMProvider:     tt.provider,
				OpenAIAPIKey:    "openai-key",
				AnthropicAPIKey: "anthropic-key",
				GeminiAPIKey:    "gemini-key",
				LLMBaseURL:      "http://proxy",
			}
			assertLLMConfig(t, cfg.LLMConfig(), tt.provider, tt.wantKey, "http://proxy")
		})
	}
}

func TestConfig_CommercialLLMConfig(t *testing.T) {
	tests := []struct {
		provider string
		wantKey  string
	}{
		{"anthropic", "anthropic-key"},
		{"gemini", "gemini-key"},
		{"openai", "openai-key"},
		{"unknown", "openai-key"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			cfg := &Config{ //nolint:gosec // test keys, not real credentials
				CommercialLLMProvider: tt.provider,
				OpenAIAPIKey:          "openai-key",
				AnthropicAPIKey:       "anthropic-key",
				GeminiAPIKey:          "gemini-key",
				LLMBaseURL:            "http://proxy",
			}
			assertLLMConfig(t, cfg.CommercialLLMConfig(), tt.provider, tt.wantKey, "http://proxy")
		})
	}
}

// TestGetHelperFunctions directly tests the unexported helper functions used
// internally by Load().
func TestGetHelperFunctions(t *testing.T) {
	t.Run("getEnv returns value when set", func(t *testing.T) {
		t.Setenv("TEST_GETENV_SET", "custom")
		if got := getEnv("TEST_GETENV_SET", "fallback"); got != "custom" {
			t.Errorf("getEnv = %q, want %q", got, "custom")
		}
	})
	t.Run("getEnv returns fallback when unset", func(t *testing.T) {
		_ = os.Unsetenv("TEST_GETENV_UNSET")
		if got := getEnv("TEST_GETENV_UNSET", "fallback"); got != "fallback" {
			t.Errorf("getEnv = %q, want %q", got, "fallback")
		}
	})
	t.Run("getEnv returns fallback when empty", func(t *testing.T) {
		t.Setenv("TEST_GETENV_EMPTY", "")
		if got := getEnv("TEST_GETENV_EMPTY", "default"); got != "default" {
			t.Errorf("getEnv = %q, want %q", got, "default")
		}
	})
	t.Run("getEnvInt returns value when set", func(t *testing.T) {
		t.Setenv("TEST_INT_SET", "42")
		if got := getEnvInt("TEST_INT_SET", 1); got != 42 {
			t.Errorf("getEnvInt = %d, want %d", got, 42)
		}
	})
	t.Run("getEnvInt returns fallback when unset", func(t *testing.T) {
		_ = os.Unsetenv("TEST_INT_UNSET")
		if got := getEnvInt("TEST_INT_UNSET", 99); got != 99 {
			t.Errorf("getEnvInt = %d, want %d", got, 99)
		}
	})
	t.Run("getEnvInt returns fallback on bad value", func(t *testing.T) {
		t.Setenv("TEST_INT_BAD", "not-an-int")
		if got := getEnvInt("TEST_INT_BAD", 77); got != 77 {
			t.Errorf("getEnvInt = %d, want %d", got, 77)
		}
	})
	t.Run("getEnvInt returns fallback on empty value", func(t *testing.T) {
		t.Setenv("TEST_INT_EMPTY", "")
		if got := getEnvInt("TEST_INT_EMPTY", 55); got != 55 {
			t.Errorf("getEnvInt = %d, want %d", got, 55)
		}
	})
	t.Run("getEnvFloat returns value when set", func(t *testing.T) {
		t.Setenv("TEST_FLOAT_SET", "3.14")
		if got := getEnvFloat("TEST_FLOAT_SET", 1.0); got != 3.14 {
			t.Errorf("getEnvFloat = %v, want %v", got, 3.14)
		}
	})
	t.Run("getEnvFloat returns fallback when unset", func(t *testing.T) {
		_ = os.Unsetenv("TEST_FLOAT_UNSET")
		if got := getEnvFloat("TEST_FLOAT_UNSET", 2.71); got != 2.71 {
			t.Errorf("getEnvFloat = %v, want %v", got, 2.71)
		}
	})
	t.Run("getEnvFloat returns fallback on bad value", func(t *testing.T) {
		t.Setenv("TEST_FLOAT_BAD", "not-a-float")
		if got := getEnvFloat("TEST_FLOAT_BAD", 0.5); got != 0.5 {
			t.Errorf("getEnvFloat = %v, want %v", got, 0.5)
		}
	})
	t.Run("getEnvFloat returns fallback on empty value", func(t *testing.T) {
		t.Setenv("TEST_FLOAT_EMPTY", "")
		if got := getEnvFloat("TEST_FLOAT_EMPTY", 0.1); got != 0.1 {
			t.Errorf("getEnvFloat = %v, want %v", got, 0.1)
		}
	})
	t.Run("requireEnv returns value when set", func(t *testing.T) {
		t.Setenv("TEST_REQUIRE_SET", "present")
		got, err := requireEnv("TEST_REQUIRE_SET")
		if err != nil {
			t.Fatalf("requireEnv unexpected error: %v", err)
		}
		if got != "present" {
			t.Errorf("requireEnv = %q, want %q", got, "present")
		}
	})
	t.Run("requireEnv returns error when unset", func(t *testing.T) {
		_ = os.Unsetenv("TEST_REQUIRE_UNSET")
		_, err := requireEnv("TEST_REQUIRE_UNSET")
		if err == nil {
			t.Fatal("expected error from requireEnv for unset var, got nil")
		}
	})
	t.Run("requireEnv returns error when empty", func(t *testing.T) {
		t.Setenv("TEST_REQUIRE_EMPTY", "")
		_, err := requireEnv("TEST_REQUIRE_EMPTY")
		if err == nil {
			t.Fatal("expected error from requireEnv for empty var, got nil")
		}
	})
}

// TestGetEnvIntNegativeValues verifies that negative integers are accepted.
func TestGetEnvIntNegativeValues(t *testing.T) {
	t.Setenv("TEST_NEG", "-100")
	if got := getEnvInt("TEST_NEG", 1); got != -100 {
		t.Errorf("getEnvInt(-100) = %d, want -100", got)
	}
}

// TestGetFloatZero verifies zero float values are accepted.
func TestGetFloatZero(t *testing.T) {
	t.Setenv("TEST_ZERO_FLOAT", "0")
	if got := getEnvFloat("TEST_ZERO_FLOAT", 1.0); got != 0.0 {
		t.Errorf("getEnvFloat(0) = %v, want 0", got)
	}
}

// TestRequireEnv returns error for empty but not for whitespace (os.Getenv doesn't trim).
func TestRequireEnvWhitespace(t *testing.T) {
	t.Setenv("TEST_WS", "   ")
	got, err := requireEnv("TEST_WS")
	if err != nil {
		t.Fatalf("requireEnv whitespace unexpected error: %v", err)
	}
	if got != "   " {
		t.Errorf("requireEnv whitespace = %q, want whitespace preserved", got)
	}
}
