"""
app/services/daily_job.py
──────────────────────────
Orchestrates the full nightly data collection pipeline.

Execution order
───────────────
1. Initialise TradingViewClient (loads browser cookies once)
2. Initialise BackendClient
3. Run all three screeners — failures are isolated; one bad screener does not
   abort the others
4. Persist each result as a dated raw CSV snapshot
5. Normalise DataFrames to JSON-safe list-of-dicts (NaN / Inf → None)
6. Build the consolidated payload
7. POST payload to the Go backend API
8. Log total wall-clock duration

Call ``run_daily_job(config)`` from the scheduler.
"""

from __future__ import annotations

import math
import time
from datetime import date
from pathlib import Path
from typing import Callable

import pandas as pd
import structlog

from app.clients.backend_client import BackendClient, BackendClientError
from app.clients.tradingview_client import TradingViewClient
from app.config import Config
from app.metrics import (
    backend_posts_total,
    collection_duration_seconds,
    csv_saves_total,
    screeners_total,
)
from app.screeners import episodic_pivot, market_leaders, momentum
from app.services.storage import save_raw_snapshot

log = structlog.get_logger(__name__)

# Module-level reference to the server's validation stats dict.
# Set by server.py at startup; used to update /health/validation state.
_validation_stats_ref: dict | None = None


def set_validation_stats_ref(ref: dict) -> None:
    """Set the shared validation stats dict for the /health/validation endpoint."""
    global _validation_stats_ref
    _validation_stats_ref = ref


# ── Public entry point ────────────────────────────────────────────────────────


def run_daily_job(config: Config) -> None:
    """
    Run the full nightly collection pipeline.

    :param config: Loaded runtime configuration.
    """
    run_date = date.today()
    started_at = time.monotonic()

    log.info("daily_job.start", date=run_date.isoformat())

    # ── 1. Clients ────────────────────────────────────────────────────────────
    tv_client = TradingViewClient.from_config(config)
    backend = BackendClient.from_config(config)

    try:
        # ── 2. Run screeners (isolated — one failure does not stop the others) ─
        momentum_df = _run_screener("momentum", momentum.run, tv_client)
        ep_df = _run_screener("episodic_pivot", episodic_pivot.run, tv_client)
        ml_df = _run_screener("market_leaders", market_leaders.run, tv_client)

        screeners_ok = sum(df is not None for df in [momentum_df, ep_df, ml_df])
        log.info("daily_job.screeners_complete", succeeded=screeners_ok, total=3)

        # Diagnostic logging: pre-stage1 counts per screener
        log.info(
            "screener.pre_stage1",
            screener="momentum",
            rows=len(momentum_df) if momentum_df is not None else 0,
        )
        log.info(
            "screener.pre_stage1",
            screener="market_leaders",
            rows=len(ml_df) if ml_df is not None else 0,
        )
        log.info(
            "screener.pre_stage1",
            screener="episodic_pivot",
            rows=len(ep_df) if ep_df is not None else 0,
        )

        # ── 3. Save raw snapshots ─────────────────────────────────────────────
        base_dir = Path(config.raw_data_dir)
        _save_if_present(momentum_df, "momentum", base_dir, run_date)
        _save_if_present(ep_df, "episodic_pivot", base_dir, run_date)
        _save_if_present(ml_df, "market_leaders", base_dir, run_date)

        # ── 4. Normalise DataFrames to JSON-safe list-of-dicts ────────────────
        raw_momentum = _normalize(momentum_df)
        raw_ep = _normalize(ep_df)
        raw_ml = _normalize(ml_df)

        # ── 5. Build payload ──────────────────────────────────────────────────
        # Validation is handled inside each screener's run() function.
        # The rows returned from the screener are already validated and cleaned.
        # We use them directly — no second validation pass needed.
        payload: dict = {
            "date": run_date.isoformat(),
            "momentum": raw_momentum,
            "episodic_pivots": raw_ep,
            "market_leaders": raw_ml,
        }

        log.info(
            "daily_job.payload_built",
            date=run_date.isoformat(),
            momentum_rows=len(raw_momentum),
            episodic_pivot_rows=len(raw_ep),
            market_leaders_rows=len(raw_ml),
        )

        # ── 6. Send to backend ────────────────────────────────────────────────
        try:
            backend.send_market_snapshot(payload)
            backend_posts_total.labels(status="success").inc()
        except BackendClientError as exc:
            # Log the error but do not crash the job — the raw CSVs are already
            # saved locally and can be replayed manually.
            backend_posts_total.labels(status="failure").inc()
            log.error(
                "daily_job.backend_send_failed",
                error=str(exc),
                hint="raw CSVs saved locally; replay with the importer CLI",
            )

    finally:
        backend.close()

    elapsed = time.monotonic() - started_at
    collection_duration_seconds.set(elapsed)
    screeners_completed = sum(1 for v in payload.values() if isinstance(v, list) and len(v) > 0)
    total_tickers = sum(len(v) for v in payload.values() if isinstance(v, list))
    log.info(
        "daily_job.complete",
        step="tv_collector",
        date=run_date.isoformat(),
        duration_ms=int(elapsed * 1000),
        screeners_completed=screeners_completed,
        total_tickers=total_tickers,
    )


# ── Internal helpers ──────────────────────────────────────────────────────────


def _run_screener(
    name: str,
    screener_fn: Callable[[TradingViewClient], pd.DataFrame],
    tv_client: TradingViewClient,
) -> pd.DataFrame | None:
    """
    Execute a single screener function, returning None on failure.
    Errors are logged but do not propagate so the pipeline continues.
    """
    try:
        df = screener_fn(tv_client)
        screeners_total.labels(name=name, status="success").inc()
        log.info("daily_job.screener_ok", screener=name, rows=len(df))
        return df
    except Exception as exc:
        screeners_total.labels(name=name, status="failure").inc()
        log.error(
            "daily_job.screener_failed",
            screener=name,
            error=str(exc),
            error_type=type(exc).__name__,
        )
        return None


def _save_if_present(
    df: pd.DataFrame | None,
    screener_name: str,
    base_dir: Path,
    run_date: date,
) -> None:
    """Save a snapshot CSV only when the screener returned data."""
    if df is None or df.empty:
        log.warning("daily_job.snapshot_skipped", screener=screener_name, reason="screener failed or empty")
        return
    try:
        save_raw_snapshot(df, screener_name, base_dir=base_dir, run_date=run_date)
        csv_saves_total.labels(screener=screener_name).inc()
    except OSError as exc:
        log.error(
            "daily_job.snapshot_write_failed",
            screener=screener_name,
            error=str(exc),
        )


def _normalize(df: pd.DataFrame | None) -> list[dict]:
    """
    Convert a DataFrame to a JSON-safe list of dicts.

    - Returns an empty list when ``df`` is None (screener failed).
    - Replaces float NaN and ±Inf with None so the payload is valid JSON.
    - Converts pandas Timestamps to ISO-8601 strings.
    """
    if df is None or df.empty:
        return []

    records = df.to_dict(orient="records")
    return [_clean_record(row) for row in records]


def _clean_record(row: dict) -> dict:
    """Replace non-JSON-safe float values and normalise timestamps."""
    clean: dict = {}
    for key, value in row.items():
        if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
            clean[key] = None
        elif isinstance(value, pd.Timestamp):
            clean[key] = value.isoformat()
        else:
            clean[key] = value
    return clean

