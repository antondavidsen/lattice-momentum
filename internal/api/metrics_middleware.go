package api

import (
	"net/http"
	"strconv"
	"time"

	"ai-stock-service/internal/metrics"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// MetricsMiddleware records http_requests_total and http_request_duration_seconds
// for every request. Registers path pattern as the "path" label (not raw URL —
// avoids high-cardinality label explosion from ticker symbols, UUIDs, etc.).
//
// Usage: wrap the mux AFTER all routes are registered so http.ServeMux pattern
// matching has already occurred. The http.Request.Pattern field (Go 1.22+)
// provides the registered route pattern for clean label values.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		path := r.Pattern
		if path == "" {
			path = "unknown"
		}
		status := strconv.Itoa(rw.status)

		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}
