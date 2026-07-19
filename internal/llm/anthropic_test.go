// File: internal/llm/anthropic_test.go
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

// TestAnthropicGenerate_Success verifies a happy-path non-streaming request
// returns the expected text and token counts.
func TestAnthropicGenerate_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request headers
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header 'test-key', got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected anthropic-version header: %s", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		// Validate request body structure
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if reqBody["model"] != "claude-sonnet-4-6" {
			t.Errorf("expected model claude-sonnet-4-6, got %v", reqBody["model"])
		}
		if _, ok := reqBody["stream"]; ok {
			t.Errorf("expected no stream field in non-streaming request")
		}

		// Return a valid response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_123",
			"model": "claude-sonnet-4-6",
			"content": [{"type": "text", "text": "Hello from Claude"}],
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Say hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Text != "Hello from Claude" {
		t.Errorf("expected text 'Hello from Claude', got %q", resp.Text)
	}
	if resp.InputTokens != 10 {
		t.Errorf("expected input_tokens 10, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 5 {
		t.Errorf("expected output_tokens 5, got %d", resp.OutputTokens)
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model 'claude-sonnet-4-6', got %q", resp.Model)
	}
	if resp.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", resp.Provider)
	}
}

// TestAnthropicGenerate_WithSystemPrompt verifies that a system prompt is sent
// as a plain string (when caching is disabled).
func TestAnthropicGenerate_WithSystemPrompt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		// System prompt should be a plain string
		sys, ok := reqBody["system"].(string)
		if !ok || sys != "You are helpful." {
			t.Errorf("expected system as string 'You are helpful.', got %v", reqBody["system"])
		}
		// No anthropic-beta header when caching disabled
		if r.Header.Get("anthropic-beta") != "" {
			t.Errorf("unexpected anthropic-beta header when caching disabled")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"model": "claude-sonnet-4-6",
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 15, "output_tokens": 2}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
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

// TestAnthropicGenerate_WithCacheSystemPrompt verifies that cached system
// prompts use content blocks with cache_control and the anthropic-beta header.
func TestAnthropicGenerate_WithCacheSystemPrompt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-beta") != "prompt-caching-2024-07-31" {
			t.Errorf("expected anthropic-beta caching header")
		}
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		// System should be an array of content blocks with cache_control
		sysBlocks, ok := reqBody["system"].([]interface{})
		if !ok || len(sysBlocks) != 1 {
			t.Fatalf("expected system as array of blocks, got %T", reqBody["system"])
		}
		block := sysBlocks[0].(map[string]interface{})
		if block["type"] != "text" {
			t.Errorf("expected block type 'text', got %v", block["type"])
		}
		if block["text"] != "Cached instruction" {
			t.Errorf("expected block text 'Cached instruction', got %v", block["text"])
		}
		cc, ok := block["cache_control"].(map[string]interface{})
		if !ok || cc["type"] != "ephemeral" {
			t.Errorf("expected cache_control type 'ephemeral', got %v", block["cache_control"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_c",
			"model": "claude-sonnet-4-6",
			"content": [{"type": "text", "text": "cached reply"}],
			"usage": {"input_tokens": 20, "output_tokens": 2, "cache_read_input_tokens": 10, "cache_creation_input_tokens": 20}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		UserPrompt:        "Go",
		SystemPrompt:      "Cached instruction",
		CacheSystemPrompt: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.CacheReadTokens != 10 {
		t.Errorf("expected CacheReadTokens 10, got %d", resp.CacheReadTokens)
	}
	if resp.CacheCreationTokens != 20 {
		t.Errorf("expected CacheCreationTokens 20, got %d", resp.CacheCreationTokens)
	}
}

// TestAnthropicGenerate_Streaming verifies SSE streaming responses produce the
// expected accumulated text and usage from message_delta.
func TestAnthropicGenerate_Streaming(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		stream, ok := reqBody["stream"].(bool)
		if !ok || !stream {
			t.Errorf("expected stream=true in request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// message_start with model and initial usage
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_s\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()
		// content_block_delta with text
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}\n\n"))
		flusher.Flush()
		// message_delta with final output tokens
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n"))
		flusher.Flush()
		// Done signal
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Stream test",
		Stream:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", resp.Text)
	}
	if resp.InputTokens != 5 {
		t.Errorf("expected input_tokens 5, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 3 {
		t.Errorf("expected output_tokens 3, got %d", resp.OutputTokens)
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model 'claude-sonnet-4-6', got %q", resp.Model)
	}
}

// TestAnthropicGenerate_HTTPError verifies that a non-2xx HTTP status propagates
// as a formatted error message containing the status code.
func TestAnthropicGenerate_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": {"type": "rate_limit_error", "message": "Too fast"}}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Hello",
	})
	if err == nil {
		t.Fatal("expected error for HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected error to contain '429', got: %v", err)
	}
}

// TestAnthropicGenerate_StreamHTTPError verifies that a non-2xx response during
// streaming is handled as an error.
func TestAnthropicGenerate_StreamHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": {"message": "Bad Request"}}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Hello",
		Stream:     true,
	})
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain '400', got: %v", err)
	}
}

// TestAnthropicGenerate_MalformedJSON verifies that a response with invalid JSON
// produces a decode error.
func TestAnthropicGenerate_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
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

// TestAnthropicGenerate_ContextCancelled verifies that a cancelled context
// propagates as an error.
func TestAnthropicGenerate_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is cancelled
		<-r.Context().Done()
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = p.Generate(ctx, &llm.Request{
		UserPrompt: "Hello",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
