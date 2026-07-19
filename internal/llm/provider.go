// Package llm defines a provider-agnostic interface for large language model
// completions. Each provider (OpenAI, Anthropic, Gemini) is implemented in its
// own file. Use NewProvider to instantiate the correct backend from config.
package llm

import "context"

// Provider is the abstraction for an LLM text-generation backend.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Generate sends a completion request and returns the model's text response.
	Generate(ctx context.Context, req *Request) (Response, error)

	// Name returns the canonical provider identifier (e.g. "openai", "anthropic", "gemini").
	Name() string
}
