package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pgvector/pgvector-go"
)

// EmbeddingService wraps the OpenAI embeddings API.
type EmbeddingService struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	model      string
}

// NewEmbeddingService creates a new EmbeddingService for OpenAI text-embedding-3-small.
func NewEmbeddingService(apiKey, baseURL string) *EmbeddingService {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &EmbeddingService{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		model: "text-embedding-3-small",
	}
}

type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed computes a 1536-dim embedding for the given text.
func (s *EmbeddingService) Embed(ctx context.Context, text string) (pgvector.Vector, error) {
	body := embeddingRequest{
		Model: s.model,
		Input: text,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("create embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("embedding API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("read embedding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return pgvector.Vector{}, fmt.Errorf("embedding API error %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return pgvector.Vector{}, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	if embResp.Error != nil {
		return pgvector.Vector{}, fmt.Errorf("embedding API error: %s", embResp.Error.Message)
	}

	if len(embResp.Data) == 0 || len(embResp.Data[0].Embedding) == 0 {
		return pgvector.Vector{}, fmt.Errorf("empty embedding response")
	}

	return pgvector.NewVector(embResp.Data[0].Embedding), nil
}
