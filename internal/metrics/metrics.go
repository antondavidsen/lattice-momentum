// Package metrics registers all Prometheus metrics for the momentum-ai platform.
// Import this package once from cmd/api/main.go; all other packages call the
// exported variables directly — no init() side-effects, no global registry
// divergence.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ── HTTP ──────────────────────────────────────────────────────────────────────
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by method, path, and status code.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by method and path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// ── Market data ───────────────────────────────────────────────────────────────
var (
	MarketDataFetchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "market_data_fetches_total",
		Help: "Market data provider fetch attempts by provider and status (success|failure).",
	}, []string{"provider", "status"})

	MarketDataFetchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "market_data_fetch_duration_seconds",
		Help:    "Market data fetch latency by provider.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider"})

	TickersIngestedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tickers_ingested_total",
		Help: "Candles inserted per ticker ingestion run by provider.",
	}, []string{"provider"})
)

// ── DB pool ───────────────────────────────────────────────────────────────────
var (
	DBPoolAcquiredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "db_pool_acquired_total",
		Help: "Total successful pgx pool connection acquisitions.",
	})

	DBPoolErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "db_pool_errors_total",
		Help: "Total failed pgx pool connection acquisitions.",
	})
)
