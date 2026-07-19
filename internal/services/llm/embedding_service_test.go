package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Constructor ───────────────────────────────────────────────────────────────

func TestNewEmbeddingService_DefaultBaseURL(t *testing.T) {
	svc := NewEmbeddingService("sk-test-123", "")
	assert.Equal(t, "https://api.openai.com", svc.baseURL)
	assert.Equal(t, "sk-test-123", svc.apiKey)
	assert.Equal(t, "text-embedding-3-small", svc.model)
	assert.NotNil(t, svc.httpClient)
}

func TestNewEmbeddingService_CustomBaseURL(t *testing.T) {
	svc := NewEmbeddingService("sk-test-456", "https://custom.openai.com")
	assert.Equal(t, "https://custom.openai.com", svc.baseURL)
	assert.Equal(t, "sk-test-456", svc.apiKey)
}

// ── Embed — Happy path ────────────────────────────────────────────────────────

func TestEmbed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer sk-test-key", r.Header.Get("Authorization"))

		var req embeddingRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "text-embedding-3-small", req.Model)
		assert.Equal(t, "test input text", req.Input)

		// Build a 1536-dim response (only fill first 3 elements to keep the test readable)
		emb := make([]float32, 1536)
		emb[0] = 0.0123
		emb[1] = -0.0456
		emb[2] = 0.0789

		resp := embeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{
				{Embedding: emb},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("sk-test-key", srv.URL)
	vec, err := svc.Embed(context.Background(), "test input text")
	require.NoError(t, err)
	assert.Equal(t, 1536, len(vec.Slice()))
	assert.InDelta(t, float32(0.0123), vec.Slice()[0], 0.0001)
	assert.InDelta(t, float32(-0.0456), vec.Slice()[1], 0.0001)
	assert.InDelta(t, float32(0.0789), vec.Slice()[2], 0.0001)
}

// ── Embed — Error paths ──────────────────────────────────────────────────────

func TestEmbed_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("bad-key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "Invalid API key")
}

func TestEmbed_APIErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := embeddingResponse{
			Error: &struct {
				Message string `json:"message"`
			}{Message: "rate limit exceeded"},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestEmbed_EmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := embeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty embedding response")
}

func TestEmbed_ZeroDimEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := embeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{
				{Embedding: []float32{}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty embedding response")
}

func TestEmbed_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid"))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestEmbed_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// The request will fail due to context cancellation before the server responds.
		// We just need a server that accepts the connection.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	svc := NewEmbeddingService("key", srv.URL)
	_, err := svc.Embed(ctx, "test")
	require.Error(t, err)
}

// ── Embed — Header verification via server-side assertions ────────────────────

func TestEmbed_AuthorizationAndContentTypeHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer my-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		emb := make([]float32, 1536)
		resp := embeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{
				{Embedding: emb},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("my-api-key", srv.URL)
	_, err := svc.Embed(context.Background(), "test")
	require.NoError(t, err)
}

// ── Embed — pgvector.Vector correctness ──────────────────────────────────────

func TestEmbed_ReturnsPgvectorVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		emb := make([]float32, 1536)
		emb[0] = 0.5
		emb[1535] = -0.5
		resp := embeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{
				{Embedding: emb},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	svc := NewEmbeddingService("key", srv.URL)
	vec, err := svc.Embed(context.Background(), "test")
	require.NoError(t, err)

	// Verify it's the right type and dimension
	assert.IsType(t, pgvector.Vector{}, vec)
	assert.Equal(t, 1536, len(vec.Slice()))
	assert.Equal(t, float32(0.5), vec.Slice()[0])
	assert.Equal(t, float32(-0.5), vec.Slice()[1535])
}
