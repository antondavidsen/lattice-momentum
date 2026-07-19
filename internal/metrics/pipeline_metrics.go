// Package metrics registers all Prometheus metrics for the momentum-ai platform.
// Pipeline and LLM metrics are defined here; HTTP, market data, and DB pool
// metrics remain in metrics.go.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// TouchPipelineMetrics initialises the first label combination for each
// metric vector with a zero value.  Call once at application startup so
// that /metrics always exports the series, even before the first real
// event.
func TouchPipelineMetrics() {
	// Pipeline run outcomes — started/completed use "all"; failed also uses
	// "all" because step-level failures are tracked by NightlyPipelineStepTotal.
	NightlyPipelineRunsTotal.WithLabelValues("started", "all").Inc()
	NightlyPipelineRunsTotal.WithLabelValues("completed", "all").Add(0)

	// LLM counters — bootstrap with "init" for all labels so the series
	// appears in /metrics before any real LLM call.
	LLMRequestsTotal.WithLabelValues("init", "init", "init", "init").Add(0)

	// LLM histogram (Observe(0) creates the label combination)
	LLMRequestDuration.WithLabelValues("init", "init", "init").Observe(0)

	// Token counters
	for _, typ := range []string{"input", "output", "cache_read", "cache_write"} {
		LLMTokensUsedTotal.WithLabelValues("init", "init", typ, "init").Add(0)
	}
	LLMTokensCachedTotal.WithLabelValues("init", "init", "init").Add(0)
	LLMTokensUncachedTotal.WithLabelValues("init", "init", "init").Add(0)
}

// ── Nightly pipeline ──────────────────────────────────────────────────────────
var (
	NightlyPipelineDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nightly_step_duration_seconds",
		Help:    "Duration of nightly pipeline steps by job and step name.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
	}, []string{"job", "step"})

	NightlyPipelineStepTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nighFixtly_pipeline_step_total",
		Help: "Nightly pipeline step executions by step name and status (success|failure).",
	}, []string{"step", "status"})

	NightlyPipelineRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nightly_pipeline_runs_total",
		Help: "Nightly pipeline run completions by status (completed|failed) and step.",
	}, []string{"status", "step"})
)

// ── LLM ───────────────────────────────────────────────────────────────────────
//
// Label schema (applied across all LLM metrics):
//
//	llm_requests_total           {provider, model, status,            list_type}
//	llm_call_duration_seconds    {provider, model,                    list_type}
//	llm_tokens_total             {provider, model, type,              list_type}
//	llm_tokens_cached_total      {provider, model,                    list_type}
//	llm_tokens_uncached_total    {provider, model,                    list_type}
var (
	LLMRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_requests_total",
		Help: "LLM API calls by provider, model, status, and list_type.",
	}, []string{"provider", "model", "status", "list_type"})

	LLMRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "llm_call_duration_seconds",
		Help:    "LLM API call latency by provider, model, and list_type.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	}, []string{"provider", "model", "list_type"})

	LLMTokensUsedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_total",
		Help: "LLM tokens consumed by provider, model, type and list_type (input|output|cache_read|cache_write).",
	}, []string{"provider", "model", "type", "list_type"})

	LLMTokensCachedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_cached_total",
		Help: "LLM cached input tokens by provider, model, and list_type (cache_read).",
	}, []string{"provider", "model", "list_type"})

	LLMTokensUncachedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_uncached_total",
		Help: "LLM uncached input tokens by provider, model, and list_type.",
	}, []string{"provider", "model", "list_type"})
)

// ── Premarket pipeline ────────────────────────────────────────────────────────
var (
	PremarketPipelineDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "premarket_step_duration_seconds",
		Help:    "Duration of premarket pipeline steps.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
	}, []string{"step"})

	PipelineGateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pipeline_gate_total",
		Help: "Pipeline gate executions by level (full|ep_only|halt).",
	}, []string{"level"})
)
