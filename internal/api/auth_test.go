package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyMiddleware(t *testing.T) {
	// A simple next handler that writes 200 OK with body "OK"
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	tests := []struct {
		name             string
		apiKey           string
		requestPath      string
		requestMethod    string
		headers          map[string]string
		wantStatus       int
		wantBodyContains string
		expectNextCalled bool
	}{
		// ── Development mode (no API key configured) ─────────────────────
		{
			name:             "dev mode - API request passes without auth",
			apiKey:           "",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:             "dev mode - health endpoint public",
			apiKey:           "",
			requestPath:      "/health",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},

		// ── Production mode (API key configured) ─────────────────────────
		{
			name:             "public route bypasses auth",
			apiKey:           "my-secret-key",
			requestPath:      "/health",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:             "root path bypasses auth",
			apiKey:           "my-secret-key",
			requestPath:      "/",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:             "config endpoint public",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/config",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},

		// ── CORS preflight requests ──────────────────────────────────────
		{
			name:             "OPTIONS request bypasses auth",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodOptions,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},

		// ── Missing or malformed credentials ─────────────────────────────
		{
			name:             "missing auth header",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing or malformed Authorization header",
			expectNextCalled: false,
		},
		{
			name:             "bearer token without prefix",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"Authorization": "my-secret-key"},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing or malformed Authorization header",
			expectNextCalled: false,
		},
		{
			name:             "empty X-API-Key header",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"X-API-Key": ""},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing or malformed Authorization header",
			expectNextCalled: false,
		},
		{
			name:             "empty bearer token",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"Authorization": "Bearer "},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing or malformed Authorization header",
			expectNextCalled: false,
		},

		// ── Invalid credentials ──────────────────────────────────────────
		{
			name:             "invalid API key via X-API-Key",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"X-API-Key": "wrong-key"},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid API key",
			expectNextCalled: false,
		},
		{
			name:             "invalid API key via Authorization",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"Authorization": "Bearer wrong-key"},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid API key",
			expectNextCalled: false,
		},
		{
			name:             "case-sensitive key mismatch",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"X-API-Key": "MY-SECRET-KEY"},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid API key",
			expectNextCalled: false,
		},

		// ── Valid credentials ────────────────────────────────────────────
		{
			name:             "valid X-API-Key header",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"X-API-Key": "my-secret-key"},
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:             "valid Bearer token",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/reports",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"Authorization": "Bearer my-secret-key"},
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:          "X-API-Key takes precedence over Authorization",
			apiKey:        "my-secret-key",
			requestPath:   "/api/v1/reports",
			requestMethod: http.MethodGet,
			headers: map[string]string{
				"X-API-Key":     "my-secret-key",
				"Authorization": "Bearer wrong-key",
			},
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:          "X-API-Key takes precedence even when invalid",
			apiKey:        "my-secret-key",
			requestPath:   "/api/v1/reports",
			requestMethod: http.MethodGet,
			headers: map[string]string{
				"X-API-Key":     "wrong-key",
				"Authorization": "Bearer my-secret-key",
			},
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid API key",
			expectNextCalled: false,
		},
		{
			name:             "valid auth on nested API path",
			apiKey:           "my-secret-key",
			requestPath:      "/api/v1/premarket/some/deep/path",
			requestMethod:    http.MethodGet,
			headers:          map[string]string{"X-API-Key": "my-secret-key"},
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},

		// ── Edge cases ───────────────────────────────────────────────────
		{
			name:             "static assets bypass auth",
			apiKey:           "my-secret-key",
			requestPath:      "/static/main.js",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
		{
			name:             "path starting with /api but not actually API",
			apiKey:           "my-secret-key",
			requestPath:      "/api-style-name",
			requestMethod:    http.MethodGet,
			headers:          nil,
			wantStatus:       http.StatusOK,
			wantBodyContains: "OK",
			expectNextCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track if the next handler was called
			nextCalled := false
			trackingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				okHandler.ServeHTTP(w, r)
			})

			// Create middleware with the configured API key
			middleware := KeyMiddleware(tt.apiKey)

			// Wrap the tracking handler with the middleware
			handler := middleware(trackingHandler)

			// Build the request
			req := httptest.NewRequestWithContext(context.Background(), tt.requestMethod, tt.requestPath, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			// Record the response
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify status code
			if rr.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, rr.Code)
			}

			// Verify body contains expected string
			body := rr.Body.String()
			if !contains(body, tt.wantBodyContains) {
				t.Errorf("expected body to contain %q, got %q", tt.wantBodyContains, body)
			}

			// Verify next handler was called (or not) as expected
			if nextCalled != tt.expectNextCalled {
				t.Errorf("expected next handler called=%v, got %v", tt.expectNextCalled, nextCalled)
			}
		})
	}
}

// Helper function for string containment check
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	return len(s) >= len(substr) && (s == substr || s != "" && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
