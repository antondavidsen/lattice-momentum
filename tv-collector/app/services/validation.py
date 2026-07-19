"""
app/services/validation.py
──────────────────────────
Validation pipeline for TradingView screener output.

Provides column alignment checks, per-row schema validation, retry logic
for transient failures, fallback to cached data, and alerting threshold
monitoring.

Flow
────
    rows = _normalize(df)                    # list[dict]
    valid, rejected, stats = validate_batch(rows, screener_type)
    payload[screener] = valid                # only valid rows forwarded
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from datetime import date, timedelta
from typing import Any, Callable, Optional

import pandas as pd
import structlog

from app.models.schemas import SchemaRegistry

log = structlog.get_logger(__name__)

# ── Constants ──────────────────────────────────────────────────────────────────

# Expected column order per screener type.
# These must match the COLUMNS lists defined in each screener module.
_SCREENER_COLUMNS: dict[str, list[str]] = {
    "momentum": [
        "ticker",
        "name",
        "open",
        "close",
        "high",
        "low",
        "volume",
        "relative_volume_10d_calc",
        "average_volume_10d_calc",
        "market_cap_basic",
        "RSI",
        "sector",
        "change",
        "gap",
        "price_52_week_high",
        "price_52_week_low",
        "SMA20",
        "SMA50",
        "SMA150",
        "SMA200",
        "SMA200[1]",
        "Perf.W",
        "Perf.1M",
        "Perf.3M",
        "Perf.6M",
        "ATR",
        "earnings_per_share_basic_ttm",
        "earnings_per_share_diluted_yoy_growth_fq",
        "total_revenue_ttm",
        "total_revenue_yoy_growth_ttm",
        "return_on_equity_fq",
        "gross_margin",
        "operating_margin",
        "net_margin",
        "earnings_release_next_date",
        "float_shares_outstanding",
        "close[1]",
    ],
    "episodic_pivot": [
        "ticker",
        "name",
        "open",
        "high",
        "low",
        "close",
        "volume",
        "relative_volume_10d_calc",
        "market_cap_basic",
        "RSI",
        "sector",
        "gap",
        "change",
        "average_volume_10d_calc",
        "earnings_release_next_date",
        "float_shares_outstanding",
        "total_revenue_ttm",
        "total_revenue_yoy_growth_ttm",
        "earnings_per_share_basic_ttm",
        "earnings_per_share_diluted_yoy_growth_fq",
        "gross_margin",
        "operating_margin",
        "net_margin",
        "return_on_equity_fq",
        "Perf.W",
        "Perf.1M",
        "Perf.3M",
        "Perf.6M",
        "price_52_week_high",
        "price_52_week_low",
        "close[1]",
        # Moving averages & volatility — used by LLM prompt for trend alignment
        # and extension analysis (Ext% computed downstream in Go).
        "SMA20",
        "SMA50",
        "SMA150",
        "SMA200",
        "ATR",
    ],
    "market_leaders": [
        "ticker",
        "name",
        "close",
        "open",
        "high",
        "low",
        "volume",
        "relative_volume_10d_calc",
        "average_volume_10d_calc",
        "market_cap_basic",
        "RSI",
        "sector",
        "SMA20",
        "SMA50",
        "SMA150",
        "SMA200",
        "SMA200[1]",
        "ATR",
        "earnings_per_share_basic_ttm",
        "earnings_per_share_diluted_ttm",
        "earnings_per_share_diluted_yoy_growth_fq",
        "total_revenue_ttm",
        "total_revenue_yoy_growth_ttm",
        "return_on_equity_fq",
        "gross_margin",
        "net_margin",
        "operating_margin",
        "earnings_release_next_date",
        "price_52_week_high",
        "price_52_week_low",
        "close[1]",
        "Perf.3M",
        "Perf.6M",
    ],
}

# Retry configuration for external ground truth fetch.
RETRY_INITIAL_DELAY: float = 1.0  # seconds
RETRY_MAX_DELAY: float = 30.0
RETRY_MAX_ATTEMPTS: int = 3

# Alerting thresholds.
ALERT_MAX_HIGH_CONVICTION_FAILURES: int = 3
ALERT_MAX_FALLBACK_PERCENT: float = 10.0  # 10%


# ── Data classes ───────────────────────────────────────────────────────────────


@dataclass
class ValidationStats:
    """Aggregated statistics for a single validation batch."""

    screener_type: str
    total_rows: int = 0
    valid_rows: int = 0
    rejected_rows: int = 0
    fallback_rows: int = 0
    schema_version: int = 1
    header_mismatch: bool = False
    errors: list[dict[str, Any]] = field(default_factory=list)
    alerts: list[str] = field(default_factory=list)


# ── Integration wrapper functions ──────────────────────────────────────────────


def validate_screener_data(
    df: pd.DataFrame,
    screener_type: str,
    *,
    schema_version: int | None = None,
    registry: SchemaRegistry | None = None,
) -> tuple[pd.DataFrame, ValidationStats]:
    """
    Validate a screener DataFrame against the active schema.

    Converts the DataFrame to a list of dicts, runs ``validate_batch()``,
    and returns a DataFrame containing only valid rows plus the stats.

    Returns (valid_df, stats).  The valid_df is empty if all rows were
    rejected or the input was empty.
    """
    if df is None or df.empty:
        stats = ValidationStats(
            screener_type=screener_type,
            total_rows=0,
            schema_version=schema_version or (registry or SchemaRegistry()).latest_version,
        )
        return pd.DataFrame(), stats

    rows = df.to_dict(orient="records")
    valid_rows, rejected_rows, stats = validate_batch(
        rows, screener_type, schema_version=schema_version, registry=registry
    )

    valid_df = pd.DataFrame(valid_rows) if valid_rows else pd.DataFrame()

    log.info(
        "validation.screener_data_validated",
        screener=screener_type,
        total=stats.total_rows,
        valid=len(valid_rows),
        rejected=len(rejected_rows),
        header_mismatch=stats.header_mismatch,
    )

    return valid_df, stats


def process_rows_with_validation(
    rows: list[dict[str, Any]],
    screener_type: str,
    *,
    schema_version: int | None = None,
    registry: SchemaRegistry | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], ValidationStats]:
    """
    Validate a list of raw screener rows and separate valid from rejected.

    Thin wrapper around ``validate_batch()`` that provides a consistent
    entry point for the screener runners.

    Returns (valid_rows, rejected_rows, stats).
    """
    return validate_batch(rows, screener_type, schema_version=schema_version, registry=registry)


# ── Column alignment check ─────────────────────────────────────────────────────


def validate_headers(
    screener_type: str,
    actual_columns: list[str],
) -> bool:
    """
    Verify the header row matches the expected column order for *screener_type*.

    Returns True if headers match.  Logs mismatch details on failure.
    """
    expected = _SCREENER_COLUMNS.get(screener_type)
    if expected is None:
        log.warning(
            "validation.unknown_screener_type",
            screener=screener_type,
            hint="no expected columns registered for this screener",
        )
        return True  # unknown screener → pass through (defensive)

    if actual_columns == expected:
        return True

    # Find differences for diagnostic logging
    expected_set = set(expected)
    actual_set = set(actual_columns)
    missing = expected_set - actual_set
    extra = actual_set - expected_set

    log.error(
        "validation.header_mismatch",
        screener=screener_type,
        expected=expected,
        actual=actual_columns,
        missing_columns=sorted(missing) if missing else None,
        extra_columns=sorted(extra) if extra else None,
    )
    return False


# ── NaN sanitization ────────────────────────────────────────────────────────────


def _sanitize_row(row: dict[str, Any]) -> dict[str, Any]:
    """Replace NaN/Inf float values with None for JSON-safe Pydantic validation.

    Pandas ``to_dict(orient='records')`` preserves NaN floats.  Pydantic
    validators that check ``int()`` cast (e.g. ``float_shares_outstanding``)
    raise ``ValueError`` on ``int(nan)``.  This helper converts those values
    to ``None`` so schemas with ``Optional[int]`` or ``Optional[float]``
    accept them.
    """
    import math
    clean: dict[str, Any] = {}
    for key, value in row.items():
        if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
            clean[key] = None
        else:
            clean[key] = value
    return clean


# ── Per-row validation ─────────────────────────────────────────────────────────


def validate_row(
    row: dict[str, Any],
    schema_version: int,
    screener_type: str,
    registry: SchemaRegistry | None = None,
) -> tuple[bool, dict[str, Any], list[str]]:
    """
    Validate a single row against the active schema.

    Returns (is_valid, cleaned_or_original_row, error_messages).

    On success, *cleaned_or_original_row* contains the validated dict with
    proper types and ``_schema_version`` set.
    On failure, the original row is returned unchanged and errors describe
    what went wrong.
    """
    if registry is None:
        registry = SchemaRegistry()

    is_valid, cleaned, errors = registry.validate_and_clean(
        row, target_version=schema_version
    )

    if not is_valid:
        ticker = row.get("ticker", row.get("name", "unknown"))
        for err in errors:
            log.error(
                "validation.row_failed",
                ticker=ticker,
                screener=screener_type,
                schema_version=schema_version,
                error=err,
            )

    return is_valid, cleaned if is_valid else row, errors


# ── Batch validation ───────────────────────────────────────────────────────────


def validate_batch(
    rows: list[dict[str, Any]],
    screener_type: str,
    *,
    schema_version: int | None = None,
    registry: SchemaRegistry | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], ValidationStats]:
    """
    Validate a batch of rows for a given screener type.

    Steps:
    1. Check column headers (first row vs expected).
    2. Per-row schema validation.
    3. Track failure counts and alerting thresholds.

    Returns (valid_rows, rejected_rows, stats).
    """
    if registry is None:
        registry = SchemaRegistry()

    version = schema_version or registry.latest_version
    stats = ValidationStats(
        screener_type=screener_type,
        total_rows=len(rows),
        schema_version=version,
    )

    if not rows:
        log.info("validation.empty_batch", screener=screener_type)
        return [], [], stats

    # 1. Header alignment check
    first_row_cols = list(rows[0].keys())
    headers_ok = validate_headers(screener_type, first_row_cols)
    stats.header_mismatch = not headers_ok

    if not headers_ok:
        # Reject all rows — misaligned screeners produce garbage data
        log.error(
            "validation.all_rows_rejected",
            screener=screener_type,
            reason="header_mismatch",
            row_count=len(rows),
        )
        stats.rejected_rows = len(rows)
        stats.errors.append({
            "type": "header_mismatch",
            "detail": f"Expected columns for {screener_type} do not match actual",
        })
        return [], rows, stats

    # 2. Sanitize NaN/Inf floats before per-row validation
    #    (Pandas to_dict preserves NaN, but Pydantic int validators choke on it)
    sanitized_rows = [_sanitize_row(r) for r in rows]

    # 3. Per-row validation
    valid_rows: list[dict[str, Any]] = []
    rejected_rows: list[dict[str, Any]] = []

    for row in sanitized_rows:
        is_valid, cleaned, errors = validate_row(
            row, version, screener_type, registry
        )

        if is_valid:
            valid_rows.append(cleaned)
        else:
            rejected_rows.append(row)
            ticker = row.get("ticker", row.get("name", "unknown"))
            for err in errors:
                stats.errors.append({
                    "type": "validation_error",
                    "ticker": ticker,
                    "error": err,
                })

    stats.valid_rows = len(valid_rows)
    stats.rejected_rows = len(rejected_rows)

    # 3. Log summary
    if stats.rejected_rows > 0:
        log.warning(
            "validation.batch_summary",
            screener=screener_type,
            total=stats.total_rows,
            valid=stats.valid_rows,
            rejected=stats.rejected_rows,
            schema_version=version,
        )

    # 4. Check alerting thresholds
    stats.alerts = check_alerting_thresholds(stats)

    return valid_rows, rejected_rows, stats


# ── Retry logic ────────────────────────────────────────────────────────────────


def retry_with_backoff(
    fn: Callable[[], Any],
    ticker: str,
    *,
    initial_delay: float = RETRY_INITIAL_DELAY,
    max_delay: float = RETRY_MAX_DELAY,
    max_attempts: int = RETRY_MAX_ATTEMPTS,
) -> Any:
    """
    Execute *fn* with exponential backoff retry on transient failures.

    Retries on any exception.  Logs each attempt.
    Raises the last exception after all attempts are exhausted.

    This is intended for transient network failures when fetching external
    ground truth data.  Validation errors should NOT use this — they should
    fail fast.
    """
    last_exc: Exception | None = None

    for attempt in range(1, max_attempts + 1):
        try:
            result = fn()
            if attempt > 1:
                log.info(
                    "validation.retry_success",
                    ticker=ticker,
                    attempt=attempt,
                    max_attempts=max_attempts,
                )
            return result
        except Exception as exc:
            last_exc = exc
            if attempt < max_attempts:
                delay = min(initial_delay * (2 ** (attempt - 1)), max_delay)
                log.warning(
                    "validation.retry_attempt",
                    ticker=ticker,
                    attempt=attempt,
                    max_attempts=max_attempts,
                    next_delay_secs=delay,
                    error=str(exc),
                    error_type=type(exc).__name__,
                )
                time.sleep(delay)
            else:
                log.error(
                    "validation.retry_exhausted",
                    ticker=ticker,
                    attempts=max_attempts,
                    error=str(exc),
                    error_type=type(exc).__name__,
                )

    raise last_exc  # type: ignore[misc]


# ── Fallback logic ─────────────────────────────────────────────────────────────


def fallback_to_cache(
    ticker: str,
    cached_data: dict[str, Any] | None,
    cache_date: date | None = None,
) -> dict[str, Any]:
    """
    Build a fallback row tagged with cache metadata.

    When external ground truth fetch fails after all retries, use cached
    fundamentals from a prior day.  Tags the row with ``_fallback_source``
    and ``_fallback_age_days`` so downstream consumers know the data is
    not fresh.

    Returns a copy of *cached_data* with fallback tags, or an empty dict
    with fallback tags if *cached_data* is None.
    """
    if cached_data is None:
        cached_data = {}

    fallback_row = dict(cached_data)
    fallback_row["_fallback_source"] = "cache"
    fallback_row["_fallback_age_days"] = (
        (date.today() - cache_date).days
        if cache_date
        else 1
    )

    log.warning(
        "validation.fallback_used",
        ticker=ticker,
        fallback_source="cache",
        fallback_age_days=fallback_row["_fallback_age_days"],
        cache_date=cache_date.isoformat() if cache_date else "unknown",
    )

    return fallback_row


# ── Alerting thresholds ────────────────────────────────────────────────────────


def check_alerting_thresholds(stats: ValidationStats) -> list[str]:
    """
    Evaluate alerting rules against *stats*.

    Returns a list of alert messages (empty if no thresholds breached).
    """
    alerts: list[str] = []

    # Alert 1: >3 high-conviction stocks fail validation in a single run.
    if stats.rejected_rows > ALERT_MAX_HIGH_CONVICTION_FAILURES:
        msg = (
            f"High-conviction stock validation failures: "
            f"{stats.rejected_rows} rejected (threshold: {ALERT_MAX_HIGH_CONVICTION_FAILURES}) "
            f"for screener={stats.screener_type}"
        )
        alerts.append(msg)
        log.error("validation.alert.high_conviction_failures", alert=msg)

    # Alert 2: Schema version changes unexpectedly.
    # (Detected via header mismatch — TradingView changed output format)
    if stats.header_mismatch:
        msg = (
            f"Schema version change detected for screener={stats.screener_type}: "
            f"header mismatch — TradingView output format may have changed"
        )
        alerts.append(msg)
        log.error("validation.alert.schema_version_change", alert=msg)

    # Alert 3: >10% of rows fall back to cached ground truth.
    if stats.total_rows > 0:
        fallback_pct = (stats.fallback_rows / stats.total_rows) * 100.0
        if fallback_pct > ALERT_MAX_FALLBACK_PERCENT:
            msg = (
                f"Fallback usage exceeds threshold: "
                f"{fallback_pct:.1f}% fallback (threshold: {ALERT_MAX_FALLBACK_PERCENT}%) "
                f"for screener={stats.screener_type}"
            )
            alerts.append(msg)
            log.error("validation.alert.fallback_threshold", alert=msg)

    return alerts
