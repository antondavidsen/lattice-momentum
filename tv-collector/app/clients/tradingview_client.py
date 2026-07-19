"""
app/clients/tradingview_client.py
──────────────────────────────────
Reusable TradingView screener client.

Responsibilities
────────────────
- Load browser cookies via rookiepy so queries return live (real-time) data.
- Provide a clean execute_query() interface over tradingview-screener's Query.
- Retry transient network/API errors up to MAX_RETRIES with exponential back-off.
- Emit structured log events for every attempt, warning, and failure.

Typical usage
─────────────
    client = TradingViewClient.from_config(config)

    df = client.execute_query(
        client.get_query()
            .select("close", "volume", "EPS_growth_TTM_YOY")
            .set_markets("america")
            .limit(200)
    )
"""

from __future__ import annotations

import http.cookiejar
import time
from pathlib import Path
from typing import Callable

import pandas as pd
import rookiepy
import structlog
from tradingview_screener.query import Query

from app.config import Config

log = structlog.get_logger(__name__)

# ── Constants ─────────────────────────────────────────────────────────────────

MAX_RETRIES: int = 3
RETRY_BACKOFF_BASE: float = 2.0   # seconds; delay = base^(attempt-1): 1s, 2s, 4s

# Domains rookiepy will filter cookies for.
_TV_DOMAINS: list[str] = ["tradingview.com"]

# All known browser names. Loaders that don't exist on the current platform
# (e.g. safari / arc on Linux) are silently skipped via getattr.
_KNOWN_BROWSERS = [
    "chrome", "chromium", "firefox", "edge",
    "brave", "opera", "opera_gx", "vivaldi",
    "safari",     # macOS only
    "arc",        # macOS only
    "librewolf",
]

_BROWSER_LOADERS: dict[str, Callable[..., list[dict]]] = {
    name: fn
    for name in _KNOWN_BROWSERS
    if (fn := getattr(rookiepy, name, None)) is not None
}


# ── Cookie loading helpers ────────────────────────────────────────────────────


def _load_from_file(path: str) -> http.cookiejar.CookieJar | None:
    """
    Load cookies from a Netscape-format cookies.txt file.

    Returns None (not an empty jar) when the path is blank or the file is
    missing — this signals the caller to try the next source.

    How to export cookies from your browser:
      Chrome / Edge / Brave: install the "cookies.txt" extension, visit
        https://www.tradingview.com, click the extension → Export → Netscape.
      Firefox: use the "Export Cookies" add-on.

    Save the file at e.g. ./secrets/tradingview_cookies.txt and set
    TV_COOKIES_FILE=/run/secrets/tradingview_cookies.txt in docker-compose.
    """
    if not path or not path.strip():
        return None

    cookie_path = Path(path.strip())
    if not cookie_path.exists():
        log.warning(
            "tradingview_client.cookies_file_not_found",
            path=str(cookie_path),
            hint="set TV_COOKIES_FILE to a valid Netscape cookies.txt path",
        )
        return None

    try:
        jar = http.cookiejar.MozillaCookieJar(str(cookie_path))
        jar.load(ignore_discard=True, ignore_expires=True)
        cookie_count = sum(1 for _ in jar)
        log.info(
            "tradingview_client.cookies_loaded_from_file",
            path=str(cookie_path),
            cookie_count=cookie_count,
        )
        return jar
    except Exception as exc:
        log.warning(
            "tradingview_client.cookies_file_load_failed",
            path=str(cookie_path),
            error=str(exc),
        )
        return None


def _load_from_browser(browser: str) -> http.cookiejar.CookieJar | None:
    """
    Load cookies from the host browser via rookiepy.

    Returns None when the browser is not installed or has no cookie store
    (e.g. inside a Docker container).
    """
    browser = browser.lower()
    loader = _BROWSER_LOADERS.get(browser)
    if loader is None:
        log.warning(
            "tradingview_client.browser_not_supported",
            browser=browser,
            supported=sorted(_BROWSER_LOADERS),
        )
        return None

    log.info("tradingview_client.loading_cookies_from_browser", browser=browser)
    try:
        raw_cookies = loader(_TV_DOMAINS)
        jar = rookiepy.to_cookiejar(raw_cookies)
        cookie_count = sum(1 for _ in jar)
        log.info(
            "tradingview_client.cookies_loaded_from_browser",
            browser=browser,
            cookie_count=cookie_count,
        )
        return jar
    except Exception as exc:
        log.warning(
            "tradingview_client.browser_cookie_load_failed",
            browser=browser,
            error=str(exc),
            hint="no browser cookie store found — this is normal inside Docker",
        )
        return None


def _empty_jar() -> http.cookiejar.CookieJar:
    """Return an empty cookie jar with a warning. Data will be delayed ~15 min."""
    log.warning(
        "tradingview_client.no_cookies",
        hint=(
            "Running without TradingView cookies. "
            "Data will be delayed ~15 min. "
            "Set TV_COOKIES_FILE to a Netscape cookies.txt for live data."
        ),
    )
    return http.cookiejar.CookieJar()


# ── Client ────────────────────────────────────────────────────────────────────


class TradingViewClient:
    """
    Thread-safe wrapper around tradingview-screener's Query class.

    Instantiate via the factory method::

        client = TradingViewClient.from_config(config)

    The instance is designed to be long-lived (created once at startup and
    reused across all screener calls in the same process).
    """

    def __init__(self, cookiejar: http.cookiejar.CookieJar) -> None:
        self._cookiejar = cookiejar

    # ── Factory ───────────────────────────────────────────────────────────────

    @classmethod
    def from_config(cls, config: Config) -> "TradingViewClient":
        """
        Build a TradingViewClient, loading cookies via the first available source:

        1. **TV_COOKIES_FILE** — path to a Netscape cookies.txt exported from
           the host browser. Best option for Docker containers where no browser
           is installed. Export using the "cookies.txt" browser extension.

        2. **Browser via rookiepy** — reads the live browser cookie store on the
           host (macOS / Linux with a browser installed).

        3. **No cookies** — TradingView returns delayed (~15 min) data.
           The client still works; screeners just won't be real-time.
        """
        cookiejar = (
            _load_from_file(config.tv_cookies_file)
            or _load_from_browser(config.tv_browser)
            or _empty_jar()
        )
        return cls(cookiejar)

    # ── Public API ────────────────────────────────────────────────────────────

    def get_query(self) -> Query:
        """
        Return a fresh, unconfigured Query object.

        Callers chain builder methods to describe their screener::

            query = (
                client.get_query()
                    .select("close", "volume", "sector")
                    .set_markets("america")
                    .where(col("close") > col("SMA200"))
                    .order_by("volume", ascending=False)
                    .limit(150)
            )
            df = client.execute_query(query)

        Cookies are injected at execution time inside execute_query(); the
        Query object itself does not hold credentials.
        """
        return Query()

    def execute_query(self, query: Query) -> pd.DataFrame:
        """
        Execute a configured Query against the TradingView screener API.

        Cookies are automatically injected from the cookiejar loaded at
        construction time.

        Retries up to MAX_RETRIES (3) times on any exception, with
        exponential back-off (1 s → 2 s → 4 s).

        :param query: A fully-configured Query instance.
        :return: pandas DataFrame; one row per ticker returned by the screener.
        :raises RuntimeError: when all retry attempts are exhausted.
        """
        last_exc: Exception | None = None

        for attempt in range(1, MAX_RETRIES + 1):
            try:
                log.debug(
                    "tradingview_client.execute.attempt",
                    attempt=attempt,
                    max_retries=MAX_RETRIES,
                )

                total_count, df = query.get_scanner_data(cookies=self._cookiejar)

                log.info(
                    "tradingview_client.execute.success",
                    attempt=attempt,
                    total_count=total_count,
                    rows_returned=len(df),
                )
                return df

            except Exception as exc:
                last_exc = exc

                if attempt < MAX_RETRIES:
                    sleep_secs = RETRY_BACKOFF_BASE ** (attempt - 1)
                    log.warning(
                        "tradingview_client.execute.retry",
                        attempt=attempt,
                        next_attempt=attempt + 1,
                        sleep_secs=sleep_secs,
                        error=str(exc),
                        error_type=type(exc).__name__,
                    )
                    time.sleep(sleep_secs)
                else:
                    log.error(
                        "tradingview_client.execute.failed",
                        attempts=MAX_RETRIES,
                        error=str(exc),
                        error_type=type(exc).__name__,
                    )

        raise RuntimeError(
            f"TradingView query failed after {MAX_RETRIES} attempts"
        ) from last_exc

