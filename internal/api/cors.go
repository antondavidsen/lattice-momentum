package api

import "net/http"

// CORSMiddleware returns middleware that applies a strict CORS policy.
// allowedOrigins is the exhaustive list of origins permitted to issue
// cross-origin requests. If a request's Origin header is in the list, the
// response mirrors that origin. If allowedOrigins is empty (the safe
// default), no CORS headers are added at all and cross-origin browsers
// will be blocked by the same-origin policy.
//
// The previous "*" default is intentionally not preserved: a public subset
// that has no shipped frontend should not advertise credential-bearing
// cross-origin access. Configure allowedOrigins in cmd/api/main.go once a
// real frontend exists.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := originSet[origin]; ok && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
