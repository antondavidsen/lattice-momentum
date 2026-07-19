package llm

// Request is a provider-agnostic completion request.
type Request struct {
	// Model is the model identifier (e.g. "gpt-4o", "claude-sonnet-4-6", "gemini-2.5-flash").
	// When empty the provider implementation should use its own sensible default.
	Model string

	// SystemPrompt is an optional system-level instruction prepended to the conversation.
	SystemPrompt string

	// UserPrompt is the main user message to send to the model.
	UserPrompt string

	// Temperature controls randomness (0.0–1.0). Zero value uses DefaultTemperature.
	Temperature float64

	// MaxTokens caps the response length. Zero value uses DefaultMaxTokens.
	MaxTokens int

	// CacheSystemPrompt enables prompt caching for the system prompt (Anthropic only).
	// When true, the system prompt is sent with cache_control: {"type": "ephemeral"}
	// so that identical system prompts across requests are cached and reused.
	CacheSystemPrompt bool

	// Stream enables streaming mode for the response. When true, the provider
	// uses server-sent events (SSE) to stream incremental results, preventing
	// HTTP timeouts for long-running completions.
	Stream bool

	// ListType is an optional label for metrics instrumentation (e.g. "ep",
	// "momentum", "leaders", "premarket", "commercial_report").
	// When empty, provider-level metrics use "unknown".
	ListType string
}

// Response is a provider-agnostic completion result.
type Response struct {
	// Text is the model's plain-text output.
	Text string

	// InputTokens is the number of prompt tokens consumed (for cost tracking).
	InputTokens int

	// OutputTokens is the number of completion tokens produced.
	OutputTokens int

	// CacheReadTokens is the number of input tokens served from the prompt cache.
	// Only populated when the provider supports prompt caching (e.g. Anthropic).
	CacheReadTokens int

	// CacheCreationTokens is the number of input tokens written to the prompt cache.
	// Only populated when the provider supports prompt caching (e.g. Anthropic).
	CacheCreationTokens int

	// Provider is the canonical provider name that served this request.
	Provider string

	// Model is the concrete model that produced the response.
	Model string
}

// Safety defaults applied when the caller leaves fields at their zero value.
const (
	DefaultTemperature float64 = 0.2
	DefaultMaxTokens   int     = 4000
	DefaultTimeout             = 180 // seconds – large eval prompts (8k+ output tokens) need >60s
)

// ApplyDefaults fills zero-valued fields with safe production defaults.
func (r *Request) ApplyDefaults() {
	if r.Temperature == 0 {
		r.Temperature = DefaultTemperature
	}
	if r.MaxTokens == 0 {
		r.MaxTokens = DefaultMaxTokens
	}
}
