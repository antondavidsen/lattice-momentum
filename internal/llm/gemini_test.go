// File: internal/llm/gemini_test.go
package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-stock-service/internal/llm"
)

// TestGeminiGenerate_Success verifies a happy-path request returns the expected
// text and token counts from the Gemini REST API.
func TestGeminiGenerate_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate API key is in the x-goog-api-key header, not the query string
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key header 'test-key', got %q", r.Header.Get("x-goog-api-key"))
		}
		if strings.Contains(r.URL.String(), "key=") {
			t.Errorf("API key should not be in query string, got %s", r.URL.String())
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		contents, ok := reqBody["contents"].([]interface{})
		if !ok || len(contents) != 1 {
			t.Fatalf("expected contents array with 1 item, got %v", reqBody["contents"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"candidates": [{"content": {"parts": [{"text": "Hello from Gemini"}]}}],
			"usageMetadata": {"promptTokenCount": 7, "candidatesTokenCount": 3}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Say hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Text != "Hello from Gemini" {
		t.Errorf("expected text 'Hello from Gemini', got %q", resp.Text)
	}
	if resp.InputTokens != 7 {
		t.Errorf("expected input_tokens 7, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 3 {
		t.Errorf("expected output_tokens 3, got %d", resp.OutputTokens)
	}
	if resp.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", resp.Provider)
	}
}

// TestGeminiGenerate_WithSystemPrompt verifies the systemInstruction field is
// populated when a system prompt is provided.
func TestGeminiGenerate_WithSystemPrompt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		sys, ok := reqBody["systemInstruction"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected systemInstruction in request body")
		}
		parts, ok := sys["parts"].([]interface{})
		if !ok || len(parts) != 1 {
			t.Fatalf("expected systemInstruction.parts with 1 item")
		}
		part := parts[0].(map[string]interface{})
		if part["text"] != "You are helpful." {
			t.Errorf("expected system text 'You are helpful.', got %v", part["text"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"candidates": [{"content": {"parts": [{"text": "ok"}]}}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 2}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt:   "Hi",
		SystemPrompt: "You are helpful.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGeminiGenerate_HTTPError verifies non-2xx responses produce a formatted error.
func TestGeminiGenerate_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": {"code": 403, "message": "API key not valid"}}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "bad-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Hello",
	})
	if err == nil {
		t.Fatal("expected error for HTTP 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to contain '403', got: %v", err)
	}
}

// TestGeminiGenerate_EmptyResponse verifies that a response with no candidates
// produces a "empty response" error.
func TestGeminiGenerate_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"candidates": [],
			"usageMetadata": {"promptTokenCount": 3, "candidatesTokenCount": 0}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Test",
	})
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("expected error to mention 'empty response', got: %v", err)
	}
}

// TestGeminiGenerate_MalformedJSON verifies invalid response JSON produces an error.
func TestGeminiGenerate_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{broken`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Test",
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestGeminiGenerate_NoUsageMetadata verifies the API gracefully handles
// missing usage metadata by defaulting counts to zero.
func TestGeminiGenerate_NoUsageMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"candidates": [{"content": {"parts": [{"text": "Hello"}]}}]
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Text != "Hello" {
		t.Errorf("expected text 'Hello', got %q", resp.Text)
	}
	if resp.InputTokens != 0 {
		t.Errorf("expected input_tokens 0 when usage missing, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 0 {
		t.Errorf("expected output_tokens 0 when usage missing, got %d", resp.OutputTokens)
	}
}

// TestGeminiGenerate_ContextCancelled verifies a cancelled context is propagated.
func TestGeminiGenerate_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "gemini",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = p.Generate(ctx, &llm.Request{
		UserPrompt: "Hello",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
