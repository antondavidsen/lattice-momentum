package llm

import (
	"fmt"
	"regexp"
)

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

// redactErrorBody returns a short, secret-safe representation of an upstream
// API error body for inclusion in returned Go errors. It caps the body at
// 512 bytes and masks common API-key token patterns so the key never lands
// in a wrapped error that might be logged upstream.
func redactErrorBody(body []byte) string {
	const maxLen = 512
	b := body
	if len(b) > maxLen {
		b = b[:maxLen]
	}
	s := string(b)
	// Mask common provider key prefixes. These patterns are intentionally
	// permissive: a sk-... token, a sk-ant-... token, a Google AIza... key,
	// and an explicit Authorization: Bearer <token> line.
	keyPatterns := []string{
		`sk-ant-[A-Za-z0-9_-]+`,
		`sk-[A-Za-z0-9_-]{8,}`,
		`AIza[0-9A-Za-z_-]{8,}`,
		`Bearer\s+[A-Za-z0-9._\-]+`,
	}
	for _, p := range keyPatterns {
		re := regexp.MustCompile(p)
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
