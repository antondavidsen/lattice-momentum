package llm

import (
	"ai-stock-service/internal/metrics"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicDefaultModel   = "claude-sonnet-4-6"
	anthropicVersion        = "2023-06-01"
)

// anthropicClient implements Provider using the Anthropic Messages API.
type anthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// newAnthropic creates a ready-to-use Anthropic provider.
func newAnthropic(apiKey, baseURL string) *anthropicClient {
	if baseURL == "" {
		baseURL = anthropicDefaultBaseURL
	}
	return &anthropicClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(DefaultTimeout) * time.Second,
		},
	}
}

func (c *anthropicClient) Name() string { return "anthropic" }

func (c *anthropicClient) Generate(ctx context.Context, req *Request) (Response, error) {
	req.ApplyDefaults()
	start := time.Now()
	listType := req.ListType
	if listType == "" {
		listType = "unknown"
	}

	model := req.Model
	if model == "" {
		model = anthropicDefaultModel
	}

	messages := []anthropicMessage{
		{Role: "user", Content: req.UserPrompt},
	}

	body := anthropicMessagesRequest{
		Model:       model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}

	// Build system prompt: use content blocks with cache_control when caching
	// is enabled, otherwise use a plain string for backward compatibility.
	if req.SystemPrompt != "" {
		if req.CacheSystemPrompt {
			body.SystemBlocks = []anthropicSystemBlock{
				{
					Type: "text",
					Text: req.SystemPrompt,
					CacheControl: &anthropicCacheControl{
						Type: "ephemeral",
					},
				},
			}
		} else {
			body.System = req.SystemPrompt
		}
	}

	payload, err := json.Marshal(&body)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("anthropic: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	// Enable prompt caching beta header when using cache_control.
	if req.CacheSystemPrompt {
		httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var llmResp Response
	if req.Stream {
		llmResp, err = c.handleStreamResponse(resp)
	} else {
		llmResp, err = c.handleSyncResponse(resp)
	}
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, err
	}

	// Record success metrics
	duration := time.Since(start).Seconds()
	respModel := llmResp.Model
	if respModel == "" {
		respModel = model
		slog.Warn("LLM response missing model — falling back to request model", "component", "llm_anthropic", "model", model)
	}
	metrics.LLMRequestsTotal.WithLabelValues(c.Name(), respModel, "success", listType).Inc()
	metrics.LLMRequestDuration.WithLabelValues(c.Name(), respModel, listType).Observe(duration)
	metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "input", listType).Add(float64(llmResp.InputTokens))
	metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "output", listType).Add(float64(llmResp.OutputTokens))
	if llmResp.CacheReadTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "cache_read", listType).Add(float64(llmResp.CacheReadTokens))
		metrics.LLMTokensCachedTotal.WithLabelValues(c.Name(), respModel, listType).Add(float64(llmResp.CacheReadTokens))
	}
	if llmResp.CacheCreationTokens > 0 {
		metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "cache_write", listType).Add(float64(llmResp.CacheCreationTokens))
	}
	uncachedInput := llmResp.InputTokens - llmResp.CacheReadTokens
	if uncachedInput > 0 {
		metrics.LLMTokensUncachedTotal.WithLabelValues(c.Name(), respModel, listType).Add(float64(uncachedInput))
	}

	return llmResp, nil
}

// handleSyncResponse processes a standard (non-streaming) Anthropic response.
func (c *anthropicClient) handleSyncResponse(resp *http.Response) (Response, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("anthropic: non-2xx response",
			"component", "llm_anthropic",
			"status_code", resp.StatusCode,
			"llm_provider", "anthropic",
		)
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var msgResp anthropicMessagesResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return c.buildResponse(msgResp), nil
}

// handleStreamResponse processes an SSE streaming response from Anthropic.
// It accumulates text content deltas and extracts the final usage from the
// message_delta event.
func (c *anthropicClient) handleStreamResponse(resp *http.Response) (Response, error) {
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("anthropic: non-2xx streaming response",
			"component", "llm_anthropic",
			"status_code", resp.StatusCode,
			"llm_provider", "anthropic",
		)
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var (
		text  strings.Builder
		model string
		usage anthropicUsage
	)

	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for potentially large SSE events.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // skip malformed events
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil {
				model = event.Message.Model
				// Capture initial usage (includes cache_read/cache_creation from prompt).
				usage = event.Message.Usage
			}
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Type == "text_delta" {
				text.WriteString(event.Delta.Text)
			}
		case "message_delta":
			// Final usage update with output tokens.
			if event.Usage != nil {
				usage.OutputTokens = event.Usage.OutputTokens
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return Response{}, fmt.Errorf("anthropic: stream read error: %w", err)
	}

	return Response{
		Text:                text.String(),
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		Provider:            c.Name(),
		Model:               model,
	}, nil
}

// buildResponse extracts a provider-agnostic Response from the Anthropic wire format.
func (c *anthropicClient) buildResponse(msgResp anthropicMessagesResponse) Response {
	var text string
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return Response{
		Text:                text,
		InputTokens:         msgResp.Usage.InputTokens,
		OutputTokens:        msgResp.Usage.OutputTokens,
		CacheReadTokens:     msgResp.Usage.CacheReadInputTokens,
		CacheCreationTokens: msgResp.Usage.CacheCreationInputTokens,
		Provider:            c.Name(),
		Model:               msgResp.Model,
	}
}

// ── Anthropic wire types ────────────────────────────────────────────────────

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicMessagesRequest supports both plain string system prompts and
// structured content blocks with cache_control.
type anthropicMessagesRequest struct {
	Model        string                 `json:"model"`
	Messages     []anthropicMessage     `json:"messages"`
	System       string                 `json:"-"` // marshalled manually
	SystemBlocks []anthropicSystemBlock `json:"-"` // marshalled manually
	Temperature  float64                `json:"temperature"`
	MaxTokens    int                    `json:"max_tokens"`
	Stream       bool                   `json:"stream,omitempty"`
}

// MarshalJSON implements custom marshalling to handle the system field
// being either a string or an array of content blocks.
func (r *anthropicMessagesRequest) MarshalJSON() ([]byte, error) {
	type baseRequest struct {
		Model       string             `json:"model"`
		Messages    []anthropicMessage `json:"messages"`
		Temperature float64            `json:"temperature"`
		MaxTokens   int                `json:"max_tokens"`
		Stream      bool               `json:"stream,omitempty"`
	}

	base := baseRequest{
		Model:       r.Model,
		Messages:    r.Messages,
		Temperature: r.Temperature,
		MaxTokens:   r.MaxTokens,
		Stream:      r.Stream,
	}

	// Marshal base fields first.
	data, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}

	// Insert the system field as the appropriate type.
	if len(r.SystemBlocks) > 0 {
		sysData, err := json.Marshal(r.SystemBlocks)
		if err != nil {
			return nil, err
		}
		// Inject "system": [...] into the JSON object.
		data = data[:len(data)-1] // remove trailing '}'
		data = append(data, []byte(`,"system":`)...)
		data = append(data, sysData...)
		data = append(data, '}')
	} else if r.System != "" {
		sysData, err := json.Marshal(r.System)
		if err != nil {
			return nil, err
		}
		data = data[:len(data)-1]
		data = append(data, []byte(`,"system":`)...)
		data = append(data, sysData...)
		data = append(data, '}')
	}

	return data, nil
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

type anthropicMessagesResponse struct {
	Model   string                  `json:"model"`
	Content []anthropicContentBlock `json:"content"`
	Usage   anthropicUsage          `json:"usage"`
}

// ── Anthropic SSE streaming types ───────────────────────────────────────────

type anthropicStreamEvent struct {
	Type    string                     `json:"type"`
	Message *anthropicMessagesResponse `json:"message,omitempty"`
	Delta   *anthropicStreamDelta      `json:"delta,omitempty"`
	Usage   *anthropicUsage            `json:"usage,omitempty"`
}

type anthropicStreamDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text,omitempty"`
}
