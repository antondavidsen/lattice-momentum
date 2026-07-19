package llm

import "fmt"

// Config holds the settings needed to instantiate an LLM provider.
type Config struct {
	// Provider selects the backend: "openai" | "anthropic" | "gemini".
	Provider string

	// APIKey is the secret key for the chosen provider.
	APIKey string

	// BaseURL is an optional override for the provider's API endpoint
	// (useful for proxies, local models, or testing).
	BaseURL string
}

// NewProvider instantiates the correct Provider implementation based on
// cfg.Provider. Returns an error when the provider name is unrecognised or
// the required API key is missing.
func NewProvider(cfg Config) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("llm: API key is required for provider %q", cfg.Provider)
	}

	switch cfg.Provider {
	case "openai":
		return newOpenAI(cfg.APIKey, cfg.BaseURL), nil
	case "anthropic":
		return newAnthropic(cfg.APIKey, cfg.BaseURL), nil
	case "gemini":
		return newGemini(cfg.APIKey, cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q (supported: openai, anthropic, gemini)", cfg.Provider)
	}
}
