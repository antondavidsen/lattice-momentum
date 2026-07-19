"""
app/metrics.py
──────────────
Prometheus metrics for tv-collector.

Emits:
  - screeners_total — counter for screener runs by name + status (success/failure)
  - csv_saves_total — counter for successful CSV snapshots saved
  - backend_posts_total — counter for backend API POSTs by status
  - collection_duration_seconds — gauge for latest job wall-clock time
  - validation_failures — counter for validation failures by screener
  - fallback_usage — counter for fallback usage by screener
  - validation_errors_total — counter for validation errors by screener + field
  - schema_version — gauge for active schema version by screener
"""

from prometheus_client import Counter, Gauge

# ── Counters ──────────────────────────────────────────────────────────────────

screeners_total = Counter(
    "tv_collector_screeners_total",
    "Total screener runs by name and status",
    labelnames=["name", "status"],  # status: success | failure
)

csv_saves_total = Counter(
    "tv_collector_csv_saves_total",
    "Total CSV snapshots saved",
    labelnames=["screener"],
)

backend_posts_total = Counter(
    "tv_collector_backend_posts_total",
    "Total backend API POST attempts by status",
    labelnames=["status"],  # status: success | failure
)

validation_failures = Counter(
    "tv_collector_validation_failures",
    "Total validation failures by screener",
    labelnames=["screener"],
)

fallback_usage = Counter(
    "tv_collector_fallback_usage",
    "Total fallback usage by screener",
    labelnames=["screener"],
)

validation_errors_total = Counter(
    "tv_collector_validation_errors_total",
    "Total validation errors by screener and field",
    labelnames=["screener", "field"],
)

# ── Gauges ────────────────────────────────────────────────────────────────────

collection_duration_seconds = Gauge(
    "tv_collector_collection_duration_seconds",
    "Wall-clock duration of the latest collection run",
)

schema_version = Gauge(
    "tv_collector_schema_version",
    "Active schema version by screener",
    labelnames=["screener"],
)

schema_version_changes = Counter(
    "tv_collector_schema_version_changes",
    "Unexpected schema version changes detected",
    labelnames=["screener"],
)

# ── Cross-service metrics (same schema as Go services) ─────────────────────────

market_data_fetches_total = Counter(
    "market_data_fetches_total",
    "Total market data fetches by status and provider",
    labelnames=["status", "provider"],
)
