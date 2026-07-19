package llm

import (
	"ai-stock-service/internal/metrics"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	openaiDefaultBaseURL = "https://api.openai.com/v1"
	openaiDefaultModel   = "gpt-4.1-mini"
)

// openaiClient implements Provider using the OpenAI Responses API (v1/responses).
type openaiClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// newOpenAI creates a ready-to-use OpenAI provider.
func newOpenAI(apiKey, baseURL string) *openaiClient {
	if baseURL == "" {
		baseURL = openaiDefaultBaseURL
	}
	return &openaiClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(DefaultTimeout) * time.Second,
		},
	}
}

func (c *openaiClient) Name() string { return "openai" }

func (c *openaiClient) Generate(ctx context.Context, req *Request) (Response, error) {
	req.ApplyDefaults()
	start := time.Now()
	listType := req.ListType
	if listType == "" {
		listType = "unknown"
	}

	model := req.Model
	if model == "" {
		model = openaiDefaultModel
	}

	// Build the input array for the Responses API.
	input := make([]responsesInput, 0, 2)
	if req.SystemPrompt != "" {
		input = append(input, responsesInput{
			Role:    "developer",
			Content: req.SystemPrompt,
		})
	}
	input = append(input, responsesInput{
		Role:    "user",
		Content: req.UserPrompt,
	})

	body := responsesRequest{
		Model:           model,
		Input:           input,
		Temperature:     req.Temperature,
		MaxOutputTokens: req.MaxTokens,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("openai: non-2xx response",
			"component", "llm_openai",
			"status_code", resp.StatusCode,
			"llm_provider", "openai",
		)
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp responsesResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}

	// Extract the text from the output items.
	text := extractResponseText(apiResp.Output)
	if text == "" {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("openai: no text content in response")
	}

	llmResp := Response{
		Text:                text,
		InputTokens:         apiResp.Usage.InputTokens,
		OutputTokens:        apiResp.Usage.OutputTokens,
		CacheReadTokens:     apiResp.Usage.CacheReadInputTokens,
		CacheCreationTokens: apiResp.Usage.CacheCreationInputTokens,
		Provider:            c.Name(),
		Model:               apiResp.Model,
	}

	// Record success metrics
	duration := time.Since(start).Seconds()
	respModel := llmResp.Model
	if respModel == "" {
		respModel = model
		slog.Warn("LLM response missing model — falling back to request model", "component", "llm_openai", "model", model)
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

// extractResponseText walks the output items and concatenates all text content.
func extractResponseText(output []responsesOutputItem) string {
	for _, item := range output {
		if item.Type == "message" {
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					return c.Text
				}
			}
		}
	}
	return ""
}

// ── OpenAI Responses API wire types ─────────────────────────────────────────

type responsesInput struct {
	Role    string `json:"role"` // "developer" | "user"
	Content string `json:"content"`
}

type responsesRequest struct {
	Model           string           `json:"model"`
	Input           []responsesInput `json:"input"`
	Temperature     float64          `json:"temperature,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
}

type responsesResponse struct {
	ID     string                `json:"id"`
	Model  string                `json:"model"`
	Output []responsesOutputItem `json:"output"`
	Usage  struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	} `json:"usage"`
}

type responsesOutputItem struct {
	Type    string                  `json:"type"` // "message"
	Role    string                  `json:"role"`
	Content []responsesContentBlock `json:"content"`
}

type responsesContentBlock struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}
