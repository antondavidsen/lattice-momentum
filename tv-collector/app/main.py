"""
main.py
───────
Process entry point.

Boot order:
  1. Load .env file
  2. Load and validate Config
  3. Initialise structured logging
  4. Build the FastAPI app and start uvicorn (blocks until SIGTERM)

The APScheduler has been removed.  The Go nightly pipeline is now the sole
orchestrator: it calls POST /run on this service to trigger a TradingView
collection run.  This keeps scheduling logic in one place and ensures
tv-collector data is always collected as part of the nightly pipeline, not on
an independent timer that may drift relative to market-data ingestion.
"""

import sys

import structlog
import uvicorn
from dotenv import load_dotenv

from app.config import load_config
from app.logging_config import setup_logging
from app.server import build_app


def main() -> None:
    # 1. Load .env (no-op if the file is absent — env vars are already set in Docker)
    load_dotenv()

    # 2. Config — fail fast if required variables are missing
    try:
        config = load_config()
    except ValueError as exc:
        print(f"[FATAL] Configuration error: {exc}", file=sys.stderr)
        sys.exit(1)

    # 3. Logging
    setup_logging(config.log_level)
    log = structlog.get_logger(__name__)
    log.info(
        "tv_collector.starting",
        backend_api_url=config.backend_api_url,
        listen="0.0.0.0:8001",
        hint="waiting for POST /run from the nightly Go pipeline",
    )

    # 4. Build app and serve — uvicorn blocks here until SIGTERM.
    app = build_app(config)
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=8001,
        log_level=config.log_level.lower(),
        # Propagate SIGTERM cleanly so Docker stop works within the timeout.
        timeout_graceful_shutdown=30,
    )


if __name__ == "__main__":
    main()
