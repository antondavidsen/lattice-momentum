"""
tests/unit/test_config.py
──────────────────────────
Unit tests for the config module (load_config, Config dataclass).

Covers:
- Required env vars missing raises ValueError
- Optional env vars provide correct defaults
- All env vars parsed correctly
- Config is frozen (immutable)
- API key does not leak in repr/str
"""

from __future__ import annotations

import pytest

from app.config import Config, load_config


class TestLoadConfig:
    """load_config reads env vars and builds a Config instance."""

    def test_required_missing_raises(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Missing BACKEND_API_URL raises ValueError with the variable name."""
        monkeypatch.delenv("BACKEND_API_URL", raising=False)
        monkeypatch.setenv("COLLECT_TIME", "18:00")
        monkeypatch.setenv("TIMEZONE", "America/New_York")
        with pytest.raises(ValueError, match="BACKEND_API_URL"):
            load_config()

    def test_all_required_present_succeeds(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """When all required env vars are set, load_config returns a Config."""
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        monkeypatch.setenv("COLLECT_TIME", "18:00")
        monkeypatch.setenv("TIMEZONE", "America/New_York")
        cfg = load_config()
        assert cfg.backend_api_url == "http://api:8080"
        assert cfg.collect_time == "18:00"
        assert cfg.timezone == "America/New_York"

    def test_optional_defaults(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Optional env vars fall back to documented defaults."""
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        cfg = load_config()
        assert cfg.collect_cron == ""
        assert cfg.tv_browser == "chromium"
        assert cfg.tv_cookies_file == ""
        assert cfg.log_level == "INFO"
        assert cfg.raw_data_dir == "./data/raw"
        assert cfg.api_key == ""

    def test_optional_override(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Optional env vars can be overridden."""
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        monkeypatch.setenv("COLLECT_CRON", "*/2 * * * *")
        monkeypatch.setenv("TV_BROWSER", "firefox")
        monkeypatch.setenv("LOG_LEVEL", "DEBUG")
        monkeypatch.setenv("RAW_DATA_DIR", "/data/snapshots")
        monkeypatch.setenv("API_KEY", "sk-secret")
        cfg = load_config()
        assert cfg.collect_cron == "*/2 * * * *"
        assert cfg.tv_browser == "firefox"
        assert cfg.log_level == "DEBUG"
        assert cfg.raw_data_dir == "/data/snapshots"
        assert cfg.api_key == "sk-secret"

    def test_empty_backend_api_url_raises(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """BACKEND_API_URL set to empty string is treated as missing."""
        monkeypatch.setenv("BACKEND_API_URL", "")
        with pytest.raises(ValueError, match="BACKEND_API_URL"):
            load_config()

    def test_blank_backend_api_url_raises(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """BACKEND_API_URL set to whitespace-only is treated as missing."""
        monkeypatch.setenv("BACKEND_API_URL", "   ")
        with pytest.raises(ValueError, match="BACKEND_API_URL"):
            load_config()

    def test_multiple_required_missing_reports_first(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """When multiple required vars are missing, load_config raises on the first one checked."""
        monkeypatch.delenv("BACKEND_API_URL", raising=False)
        monkeypatch.delenv("TIMEZONE", raising=False)
        monkeypatch.setenv("COLLECT_TIME", "18:00")
        with pytest.raises(ValueError, match="BACKEND_API_URL"):
            load_config()

    def test_tv_cookies_file_optional(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """TV_COOKIES_FILE can be set to a path."""
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        monkeypatch.setenv("TV_COOKIES_FILE", "/cookies/netscape.txt")
        cfg = load_config()
        assert cfg.tv_cookies_file == "/cookies/netscape.txt"

    def test_collect_time_override(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """COLLECT_TIME can be overridden from its default."""
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        monkeypatch.setenv("COLLECT_TIME", "06:00")
        cfg = load_config()
        assert cfg.collect_time == "06:00"


class TestConfigDataclass:
    """Config dataclass behaviour."""

    def test_frozen_prevents_mutation(self) -> None:
        """Config is frozen — trying to set an attribute raises FrozenInstanceError."""
        cfg = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="",
        )
        with pytest.raises(Exception):
            cfg.backend_api_url = "http://other"

    def test_repr_does_not_leak_api_key(self) -> None:
        """repr() of Config should not include the api_key value."""
        cfg = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="my-super-secret-key",
        )
        r = repr(cfg)
        assert "my-super-secret-key" not in r
        assert "api_key" in r  # field name is fine, just not the value

    def test_repr_str_no_api_key_value(self) -> None:
        """str() representation should not show the actual api_key."""
        cfg = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="leaked-secret",
        )
        s = str(cfg)
        assert "leaked-secret" not in s

    def test_eq_works(self) -> None:
        """Two Config instances with same values are equal."""
        cfg1 = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="",
        )
        cfg2 = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="",
        )
        assert cfg1 == cfg2

    def test_hashable(self) -> None:
        """Frozen dataclass should be hashable."""
        cfg = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="",
        )
        s = {cfg}
        assert cfg in s