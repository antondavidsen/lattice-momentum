"""
config.py
─────────
Single source of truth for all runtime configuration.
Values are read from environment variables; .env is loaded automatically
when the process starts (via python-dotenv in main.py).
"""

import dataclasses
import os
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Config:
    # ── Backend ───────────────────────────────────────────────────────────────
    # Base URL of the Go API service, e.g. http://api:8080
    backend_api_url: str

    def __repr__(self) -> str:
        """Return a repr that does NOT leak the API key."""
        fields = []
        for f in dataclasses.fields(self):
            val = getattr(self, f.name)
            if f.name == "api_key" and val:
                val = "****"
            fields.append(f"{f.name}={val!r}")
        return f"{type(self).__name__}({', '.join(fields)})"


    # ── Scheduler ─────────────────────────────────────────────────────────────
    # Time to run daily collection in HH:MM (24-hour) format.
    collect_time: str

    # Optional full cron expression, e.g. "*/2 * * * *".
    # When set, this takes priority over COLLECT_TIME.
    # Useful for local testing without waiting until 18:00.
    collect_cron: str  # empty string means "use COLLECT_TIME instead"

    # IANA timezone for the scheduler, e.g. America/New_York.
    timezone: str

    # ── TradingView ───────────────────────────────────────────────────────────
    # Browser engine used by the TV scraping layer: "chromium" | "firefox" etc.
    tv_browser: str

    # Optional path to a Netscape-format cookies.txt file exported from the
    # host browser. Takes priority over browser-based cookie extraction.
    # Use this in Docker where no browser is installed.
    tv_cookies_file: str  # empty string = disabled

    # ── Logging ───────────────────────────────────────────────────────────────
    log_level: str

    # ── Data ──────────────────────────────────────────────────────────────────
    # Local directory where raw screener CSVs are stored before upload.
    raw_data_dir: str

    # ── API auth ─────────────────────────────────────────────────────────────
    # Shared API key for authenticating with the Go backend.
    # Must match the API_KEY env var on the Go service. Empty = auth disabled.
    api_key: str


def load_config() -> Config:
    """
    Build Config from environment variables.
    Raises ValueError for any missing required variable.
    """

    def require(key: str) -> str:
        value = os.getenv(key)
        if not value or not value.strip():
            raise ValueError(f"Required environment variable '{key}' is not set.")
        return value.strip()

    def optional(key: str, default: str) -> str:
        return os.getenv(key, default)

    return Config(
        backend_api_url=require("BACKEND_API_URL"),
        collect_time=optional("COLLECT_TIME", "18:00"),
        collect_cron=optional("COLLECT_CRON", ""),        # empty = disabled
        timezone=optional("TIMEZONE", "America/New_York"),
        tv_browser=optional("TV_BROWSER", "chromium"),
        tv_cookies_file=optional("TV_COOKIES_FILE", ""),  # empty = disabled
        log_level=optional("LOG_LEVEL", "INFO"),
        raw_data_dir=optional("RAW_DATA_DIR", "./data/raw"),
        api_key=optional("API_KEY", ""),
    )

