"""
app/screeners/audit_writer.py
──────────────────────────────
Persists stage 1 elimination records to the Go backend API.

Primary path:  POST to {go_backend_url}/api/v1/stage1-eliminations
Fallback path: Write to local file logs/stage1_audit_{run_date}.json
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

import httpx
import structlog

from app.screeners.stage1_filter import EliminationRecord

log = structlog.get_logger(__name__)

# ── Constants ──────────────────────────────────────────────────────────────────

AUDIT_ENDPOINT = "/api/v1/stage1-eliminations"
REQUEST_TIMEOUT: float = 5.0
FALLBACK_DIR = "logs"


def _record_to_dict(rec: EliminationRecord) -> dict[str, Any]:
    """Convert an EliminationRecord to a JSON-serialisable dict."""
    return {
        "ticker": rec.ticker,
        "screener": rec.screener,
        "rule": rec.rule,
        "tier": rec.tier,
        "metrics": rec.metrics,
        "reason": rec.reason,
        "date": rec.date,
    }


def write_audit(
    eliminations: list[EliminationRecord],
    go_backend_url: str,
) -> None:
    """
    Persist elimination records to the Go backend.

    Primary path: POST all records as a JSON array to the backend.
    Fallback path: Write to a local JSON file if the POST fails.

    Never blocks the main pipeline — all errors are caught and logged.

    Parameters
    ----------
    eliminations : list[EliminationRecord]
        Records to persist (may be empty).
    go_backend_url : str
        Base URL of the Go backend API (e.g. http://api:8080).
    """
    if not eliminations:
        return  # nothing to persist

    records = [_record_to_dict(rec) for rec in eliminations]
    url = f"{go_backend_url.rstrip('/')}{AUDIT_ENDPOINT}"

    try:
        with httpx.Client(timeout=httpx.Timeout(REQUEST_TIMEOUT)) as client:
            response = client.post(url, json=records)

        if response.is_success:
            log.info(
                "audit_writer.post_success",
                count=len(records),
                status=response.status_code,
                url=url,
            )
            return

        # Non-2xx response — fall through to fallback
        log.warning(
            "audit_writer.post_failed_status",
            count=len(records),
            status=response.status_code,
            body=response.text[:300],
            url=url,
        )

    except (httpx.TimeoutException, httpx.ConnectError, httpx.RequestError) as exc:
        log.warning(
            "audit_writer.post_failed_connection",
            count=len(records),
            error=str(exc),
            error_type=type(exc).__name__,
            url=url,
        )

    # ── Fallback: write to local file ──────────────────────────────────────
    _write_fallback(records)


def _write_fallback(records: list[dict[str, Any]]) -> None:
    """
    Write elimination records to a local JSON file.

    File path: logs/stage1_audit_{run_date}.json
    Appends to the array if the file already exists.
    """
    if not records:
        return

    run_date = records[0].get("date", "unknown")
    fallback_dir = Path(FALLBACK_DIR)
    fallback_dir.mkdir(parents=True, exist_ok=True)

    fallback_path = fallback_dir / f"stage1_audit_{run_date}.json"

    try:
        # Load existing records if file exists
        existing: list[dict[str, Any]] = []
        if fallback_path.exists():
            with open(fallback_path, "r") as f:
                existing = json.load(f)

        # Append new records
        existing.extend(records)

        with open(fallback_path, "w") as f:
            json.dump(existing, f, indent=2, default=str)

        log.warning(
            "audit_writer.fallback_written",
            count=len(records),
            path=str(fallback_path),
            total_records=len(existing),
        )

    except OSError as exc:
        log.error(
            "audit_writer.fallback_failed",
            error=str(exc),
            path=str(fallback_path),
        )
