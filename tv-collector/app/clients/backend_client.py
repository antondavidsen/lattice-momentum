"""
app/clients/backend_client.py
──────────────────────────────
HTTP client for the Momentum AI Go backend API.

Sends normalised screener data to the backend after each nightly collection.
Uses httpx (sync) with a shared persistent session for connection re-use.

Retry policy
────────────
3 attempts with exponential back-off (1 s → 2 s → 4 s).
Retries on connection errors, timeouts, and 5xx responses.
4xx errors (bad request, auth) are NOT retried — they are bugs, not transient.

Typical usage
─────────────
    client = BackendClient.from_config(config)
    client.send_market_snapshot(payload)
"""

from __future__ import annotations

import time
from typing import Any

import httpx
import structlog

from app.config import Config

log = structlog.get_logger(__name__)

# ── Constants ─────────────────────────────────────────────────────────────────

SNAPSHOT_ENDPOINT = "/api/v1/market-snapshots"

MAX_RETRIES: int = 3
RETRY_BACKOFF_BASE: float = 2.0   # delay = base^(attempt-1): 1 s, 2 s, 4 s

# Seconds to wait for the server to send response headers.
REQUEST_TIMEOUT: float = 30.0

# HTTP status codes that are worth retrying (server-side / transient errors).
_RETRYABLE_STATUS: frozenset[int] = frozenset({429, 500, 502, 503, 504})


# ── Client ────────────────────────────────────────────────────────────────────


class BackendClient:
    """
    Thin wrapper around httpx for communicating with the Go backend API.

    Instantiate once at startup via ``from_config()`` and reuse across calls.
    The underlying ``httpx.Client`` keeps a connection pool alive for the
    lifetime of this object; call ``close()`` (or use as a context manager)
    when the process exits.
    """

    def __init__(self, base_url: str, api_key: str = "") -> None:
        self._base_url = base_url.rstrip("/")
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "momentum-ai/tv-collector",
        }
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"
        self._http = httpx.Client(
            base_url=self._base_url,
            timeout=httpx.Timeout(REQUEST_TIMEOUT),
            headers=headers,
        )

    # ── Factory ───────────────────────────────────────────────────────────────

    @classmethod
    def from_config(cls, config: Config) -> "BackendClient":
        log.info("backend_client.init", base_url=config.backend_api_url)
        return cls(config.backend_api_url, api_key=getattr(config, "api_key", ""))

    # ── Context manager support ───────────────────────────────────────────────

    def __enter__(self) -> "BackendClient":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()

    def close(self) -> None:
        self._http.close()

    # ── Public API ────────────────────────────────────────────────────────────

    def send_market_snapshot(self, payload: dict[str, Any]) -> None:
        """
        POST a market snapshot payload to the backend.

        The payload is whatever the collection pipeline assembles — typically
        a dict with keys ``screener``, ``run_date``, and ``rows``.

        Retries up to MAX_RETRIES times on transient errors (network issues,
        timeouts, 5xx responses).  Raises immediately on 4xx (client errors).

        :param payload: JSON-serialisable dict to POST.
        :raises BackendClientError: after all retries are exhausted, or on a
                                    non-retryable HTTP error.
        """
        url = SNAPSHOT_ENDPOINT
        last_exc: Exception | None = None

        for attempt in range(1, MAX_RETRIES + 1):
            try:
                log.debug(
                    "backend_client.send.attempt",
                    url=url,
                    attempt=attempt,
                    max_retries=MAX_RETRIES,
                    screener=payload.get("screener"),
                )

                response = self._http.post(url, json=payload)

                # 429 / 5xx → transient; fall through to retry logic.
                if response.status_code in _RETRYABLE_STATUS:
                    raise _RetryableError(
                        f"Retryable status {response.status_code} from backend"
                    )

                # Other 4xx → bug in the caller; raise immediately, no retry.
                if 400 <= response.status_code < 500:
                    log.error(
                        "backend_client.send.client_error",
                        status=response.status_code,
                        body=response.text[:500],
                        url=url,
                        screener=payload.get("screener"),
                    )
                    raise BackendClientError(
                        f"Backend rejected request with {response.status_code}: {response.text[:200]}"
                    )

                response.raise_for_status()  # handle any other non-2xx

                log.info(
                    "backend_client.send.success",
                    status=response.status_code,
                    attempt=attempt,
                    screener=payload.get("screener"),
                    url=url,
                )
                return  # success — exit retry loop

            except BackendClientError:
                raise  # non-retryable; propagate immediately

            except (
                httpx.TimeoutException,
                httpx.ConnectError,
                httpx.RemoteProtocolError,  # server closed connection without a response (e.g. panic)
                _RetryableError,
            ) as exc:
                last_exc = exc
                if attempt < MAX_RETRIES:
                    sleep_secs = RETRY_BACKOFF_BASE ** (attempt - 1)
                    log.warning(
                        "backend_client.send.retry",
                        attempt=attempt,
                        next_attempt=attempt + 1,
                        sleep_secs=sleep_secs,
                        error=str(exc),
                        error_type=type(exc).__name__,
                        screener=payload.get("screener"),
                    )
                    time.sleep(sleep_secs)
                else:
                    log.error(
                        "backend_client.send.failed",
                        attempts=MAX_RETRIES,
                        error=str(exc),
                        error_type=type(exc).__name__,
                        screener=payload.get("screener"),
                    )

            except Exception as exc:
                # Unexpected error — log and re-raise without retry.
                log.error(
                    "backend_client.send.unexpected_error",
                    error=str(exc),
                    error_type=type(exc).__name__,
                )
                raise BackendClientError(f"Unexpected error sending snapshot: {exc}") from exc

        raise BackendClientError(
            f"Failed to send market snapshot after {MAX_RETRIES} attempts"
        ) from last_exc


# ── Exceptions ────────────────────────────────────────────────────────────────


class BackendClientError(Exception):
    """Raised when the backend client cannot deliver a request."""


class _RetryableError(Exception):
    """Internal sentinel for retryable HTTP status codes."""

