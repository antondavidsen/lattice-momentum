package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// KeyMiddleware returns middleware that requires a valid
// "Authorization: Bearer <key>" header on all /api/ routes.
//
// When apiKey is empty, auth is disabled (dev mode) and a startup warning is
// logged. Public routes (/health, /, static assets) are never gated.
func KeyMiddleware(apiKey string) func(http.Handler) http.Handler {
	if apiKey == "" {
		slog.Warn("API_KEY is not set — all endpoints are unauthenticated")
		return func(next http.Handler) http.Handler { return next }
	}

	expected := []byte(apiKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only gate /api/ routes; leave health checks and static assets public.
			// The /api/v1/config endpoint is intentionally public so the
			// embedded frontend can bootstrap its API key at runtime.
			if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/v1/config" {
				next.ServeHTTP(w, r)
				return
			}

			// CORS preflight requests carry no auth header.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Accept the API key from either:
			//   1. X-API-Key header (preferred — avoids conflict with nginx basic auth)
			//   2. Authorization: Bearer <key> (standard, used by machine clients)
			token := r.Header.Get("X-API-Key")
			if token == "" {
				token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if token == r.Header.Get("Authorization") {
					token = "" // no "Bearer " prefix — not a valid bearer token
				}
			}

			if token == "" {
				writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}

			if subtle.ConstantTimeCompare([]byte(token), expected) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
