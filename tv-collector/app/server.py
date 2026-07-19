"""
app/server.py
─────────────
FastAPI application that exposes the tv-collector trigger endpoint.

The Go nightly pipeline calls POST /run to trigger an immediate screener
collection for today.  The endpoint is synchronous: it blocks until the full
collection (all three screeners → raw CSV save → POST to Go API) completes,
then returns.  The Go side sets a generous HTTP timeout (default 10 minutes).

Routes
──────
  GET  /health  — Docker / Kubernetes liveness probe
  POST /run     — trigger an immediate collection run and wait for completion
  GET  /metrics — Prometheus metrics exposition
"""
from __future__ import annotations

import threading
from typing import Any

import structlog
from fastapi import FastAPI, HTTPException
from prometheus_fastapi_instrumentator import Instrumentator

from app.config import Config
from app.models.schemas import SchemaRegistry
from app.services.daily_job import run_daily_job, set_validation_stats_ref

log = structlog.get_logger(__name__)

# In-memory validation state updated after each collection run.
# Populated by the daily_job pipeline for the /health/validation endpoint.
_last_validation_stats: dict[str, Any] = {
    "schema_version": 1,
    "last_run": None,
    "total_validated": 0,
    "total_rejected": 0,
    "alerts": [],
    "screeners": {},
}

# Mutex that prevents two concurrent collection runs from stepping on each
# other.  If a run is already in progress the second caller receives a 409.
_run_lock = threading.Lock()


def build_app(config: Config) -> FastAPI:
    """Construct the FastAPI application and register all routes."""
    app = FastAPI(
        title="tv-collector",
        description="TradingView screener collection trigger service",
        version="1.0.0",
    )

    # Wire the shared validation stats ref so daily_job can update /health/validation state.
    set_validation_stats_ref(_last_validation_stats)

    # Instrument the app with Prometheus metrics.
    # This adds automatic HTTP request/response metrics and exposes /metrics.
    Instrumentator().instrument(app).expose(app)

    @app.get("/health", tags=["ops"])
    def health() -> dict:
        """Liveness probe — always returns 200 when the process is alive."""
        return {"status": "ok"}

    @app.get("/health/validation", tags=["ops"])
    def health_validation() -> dict:
        """
        Validation health endpoint.

        Reports the active schema version, last validation run stats,
        and current alert status.
        """
        registry = SchemaRegistry()
        return {
            "status": "ok",
            "active_schema_version": registry.latest_version,
            "last_run": _last_validation_stats["last_run"],
            "total_validated": _last_validation_stats["total_validated"],
            "total_rejected": _last_validation_stats["total_rejected"],
            "alerts": _last_validation_stats["alerts"],
            "screeners": _last_validation_stats["screeners"],
        }

    @app.post("/run", status_code=200, tags=["collection"])
    def run() -> dict:
        """
        Trigger an immediate TradingView screener collection.

        Blocks until all three screeners have been fetched and the normalised
        payload has been POSTed to the Go backend API.

        Returns 200 {"status": "ok"} on success.
        Returns 409 if a collection is already in progress.
        Returns 500 if the collection fails (partial screener failures are
        tolerated; only a total failure of all screeners or a backend POST
        error triggers a 500).
        """
        acquired = _run_lock.acquire(blocking=False)
        if not acquired:
            log.warning("trigger.already_running")
            raise HTTPException(status_code=409, detail="collection already in progress")

        try:
            log.info("trigger.start")
            run_daily_job(config)
            log.info("trigger.complete")
            return {"status": "ok"}
        except Exception as exc:
            log.exception("trigger.failed", error=str(exc))
            raise HTTPException(status_code=500, detail=str(exc)) from exc
        finally:
            _run_lock.release()

    return app

