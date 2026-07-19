// File: internal/llm/openai_test.go
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

// TestOpenAIGenerate_Success verifies a happy-path request returns the expected
// text and token counts from the OpenAI Responses API.
func TestOpenAIGenerate_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		// Validate request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if reqBody["model"] != "gpt-4.1-mini" {
			t.Errorf("expected model gpt-4.1-mini, got %v", reqBody["model"])
		}
		input, ok := reqBody["input"].([]interface{})
		if !ok || len(input) != 1 {
			t.Fatalf("expected input array with 1 item, got %v", reqBody["input"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "resp_123",
			"model": "gpt-4.1-mini",
			"output": [{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Hello from OpenAI"}]}],
			"usage": {"input_tokens": 8, "output_tokens": 4, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 5}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
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

	if resp.Text != "Hello from OpenAI" {
		t.Errorf("expected text 'Hello from OpenAI', got %q", resp.Text)
	}
	if resp.InputTokens != 8 {
		t.Errorf("expected input_tokens 8, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 4 {
		t.Errorf("expected output_tokens 4, got %d", resp.OutputTokens)
	}
	if resp.CacheReadTokens != 3 {
		t.Errorf("expected CacheReadTokens 3, got %d", resp.CacheReadTokens)
	}
	if resp.CacheCreationTokens != 5 {
		t.Errorf("expected CacheCreationTokens 5, got %d", resp.CacheCreationTokens)
	}
	if resp.Model != "gpt-4.1-mini" {
		t.Errorf("expected model 'gpt-4.1-mini', got %q", resp.Model)
	}
	if resp.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", resp.Provider)
	}
}

// TestOpenAIGenerate_WithSystemPrompt verifies the system prompt is sent as
// a developer-role input before the user message.
func TestOpenAIGenerate_WithSystemPrompt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		input, ok := reqBody["input"].([]interface{})
		if !ok || len(input) != 2 {
			t.Fatalf("expected input array with 2 items, got %d items", len(input))
		}
		first := input[0].(map[string]interface{})
		if first["role"] != "developer" {
			t.Errorf("expected first input role 'developer', got %v", first["role"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "resp_2",
			"model": "gpt-4.1-mini",
			"output": [{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "ok"}]}],
			"usage": {"input_tokens": 12, "output_tokens": 2}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt:   "Hi",
		SystemPrompt: "You are a helpful assistant.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOpenAIGenerate_HTTPError verifies non-2xx responses produce a formatted error.
func TestOpenAIGenerate_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
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
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to contain '401', got: %v", err)
	}
}

// TestOpenAIGenerate_MalformedJSON verifies invalid response JSON produces an error.
func TestOpenAIGenerate_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
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

// TestOpenAIGenerate_NoTextContent verifies that a response with no output_text
// content produces a "no text" error.
func TestOpenAIGenerate_NoTextContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "resp_empty",
			"model": "gpt-4.1-mini",
			"output": [{"type": "message", "role": "assistant", "content": [{"type": "refusal", "refusal": "I cannot answer"}]}],
			"usage": {"input_tokens": 5, "output_tokens": 1}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  ts.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = p.Generate(context.Background(), &llm.Request{
		UserPrompt: "Do bad thing",
	})
	if err == nil {
		t.Fatal("expected error for empty text response, got nil")
	}
	if !strings.Contains(err.Error(), "no text content") {
		t.Errorf("expected error to mention 'no text content', got: %v", err)
	}
}

// TestOpenAIGenerate_EmptyOutput verifies a response with an empty output array
// produces a "no text content" error.
func TestOpenAIGenerate_EmptyOutput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "resp_empty2",
			"model": "gpt-4.1-mini",
			"output": [],
			"usage": {"input_tokens": 3, "output_tokens": 0}
		}`))
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
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
		t.Fatal("expected error for empty output, got nil")
	}
}

// TestOpenAIGenerate_ContextCancelled verifies that a cancelled context is propagated.
func TestOpenAIGenerate_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.Config{
		Provider: "openai",
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
