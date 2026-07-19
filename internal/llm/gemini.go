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
	geminiDefaultBaseURL = "https://generativelanguage.googleapis.com"
	geminiDefaultModel   = "gemini-2.5-flash"
)

// geminiClient implements Provider using the Google Gemini REST API.
type geminiClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// newGemini creates a ready-to-use Gemini provider.
func newGemini(apiKey, baseURL string) *geminiClient {
	if baseURL == "" {
		baseURL = geminiDefaultBaseURL
	}
	return &geminiClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(DefaultTimeout) * time.Second,
		},
	}
}

func (c *geminiClient) Name() string { return "gemini" }

func (c *geminiClient) Generate(ctx context.Context, req *Request) (Response, error) {
	req.ApplyDefaults()
	start := time.Now()
	listType := req.ListType
	if listType == "" {
		listType = "unknown"
	}

	model := req.Model
	if model == "" {
		model = geminiDefaultModel
	}

	// Gemini uses a flat "parts" array within contents.
	// System instructions are sent via the top-level systemInstruction field.
	parts := []geminiPart{
		{Text: req.UserPrompt},
	}

	body := geminiGenerateRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: parts},
		},
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		},
	}

	if req.SystemPrompt != "" {
		body.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("gemini: non-2xx response",
			"component", "llm_gemini",
			"status_code", resp.StatusCode,
			"llm_provider", "gemini",
		)
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, redactErrorBody(respBody))
	}

	var genResp geminiGenerateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: decode response: %w", err)
	}

	// Extract text from the first candidate.
	var text string
	if len(genResp.Candidates) > 0 {
		for _, p := range genResp.Candidates[0].Content.Parts {
			text += p.Text
		}
	}
	if text == "" {
		metrics.LLMRequestsTotal.WithLabelValues(c.Name(), model, "error", listType).Inc()
		return Response{}, fmt.Errorf("gemini: empty response from model")
	}

	var inputTokens, outputTokens int
	if genResp.UsageMetadata != nil {
		inputTokens = genResp.UsageMetadata.PromptTokenCount
		outputTokens = genResp.UsageMetadata.CandidatesTokenCount
	}

	llmResp := Response{
		Text:         text,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Provider:     c.Name(),
		Model:        model,
	}

	// Record success metrics
	duration := time.Since(start).Seconds()
	respModel := llmResp.Model
	if respModel == "" {
		respModel = model
		slog.Warn("LLM response missing model — falling back to request model", "component", "llm_gemini", "model", model)
	}
	metrics.LLMRequestsTotal.WithLabelValues(c.Name(), respModel, "success", listType).Inc()
	metrics.LLMRequestDuration.WithLabelValues(c.Name(), respModel, listType).Observe(duration)
	metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "input", listType).Add(float64(llmResp.InputTokens))
	metrics.LLMTokensUsedTotal.WithLabelValues(c.Name(), respModel, "output", listType).Add(float64(llmResp.OutputTokens))

	return llmResp, nil
}

// ── Gemini wire types ───────────────────────────────────────────────────────

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type geminiGenerateRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerateResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}
