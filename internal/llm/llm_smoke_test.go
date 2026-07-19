package llm_test

import (
	"testing"

	"ai-stock-service/internal/llm"
)

func TestNewProvider_OpenAI(t *testing.T) {
	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("expected provider name %q, got %q", "openai", p.Name())
	}
}

func TestNewProvider_Anthropic(t *testing.T) {
	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Fatalf("expected provider name %q, got %q", "anthropic", p.Name())
	}
}

func TestNewProvider_Gemini(t *testing.T) {
	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Fatalf("expected provider name %q, got %q", "gemini", p.Name())
	}
}

func TestNewProvider_UnsupportedProvider(t *testing.T) {
	_, err := llm.NewProvider(llm.Config{
		Provider: "cohere",
		APIKey:   "test-key",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
}

func TestNewProvider_MissingAPIKey(t *testing.T) {
	_, err := llm.NewProvider(llm.Config{
		Provider: "openai",
		APIKey:   "",
	})
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
}

func TestRequestApplyDefaults(t *testing.T) {
	req := llm.Request{UserPrompt: "hello"}
	req.ApplyDefaults()

	if req.Temperature != llm.DefaultTemperature {
		t.Fatalf("expected temperature %v, got %v", llm.DefaultTemperature, req.Temperature)
	}
	if req.MaxTokens != llm.DefaultMaxTokens {
		t.Fatalf("expected max tokens %d, got %d", llm.DefaultMaxTokens, req.MaxTokens)
	}
}

func TestRequestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	req := llm.Request{
		UserPrompt:  "hello",
		Temperature: 0.9,
		MaxTokens:   2000,
	}
	req.ApplyDefaults()

	if req.Temperature != 0.9 {
		t.Fatalf("expected temperature 0.9, got %v", req.Temperature)
	}
	if req.MaxTokens != 2000 {
		t.Fatalf("expected max tokens 2000, got %d", req.MaxTokens)
	}
}
