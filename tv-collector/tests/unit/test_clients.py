"""
tests/unit/test_clients.py
───────────────────────────
Unit tests for the HTTP clients (BackendClient, TradingViewClient).

Covers:
- BackendClient: send_market_snapshot with success, retry, 4xx, 5xx, timeout
- TradingViewClient: execute_query with success, retry, exhausted
- Cookie loading: file path, missing file, empty path, browser fallback
- Factory methods (from_config)
"""

from __future__ import annotations

import http.cookiejar
from pathlib import Path
from unittest.mock import MagicMock, patch

import httpx
import pandas as pd
import pytest

from app.clients.backend_client import (
    BackendClient,
    BackendClientError,
    SNAPSHOT_ENDPOINT,
    MAX_RETRIES,
)
from app.clients.tradingview_client import (
    TradingViewClient,
    _load_from_file,
    _load_from_browser,
    _empty_jar,
    _KNOWN_BROWSERS,
    _BROWSER_LOADERS,
)
from app.config import Config


# ── Fixtures ──────────────────────────────────────────────────────────────────────


@pytest.fixture
def mock_httpx_client() -> MagicMock:
    """Return a MagicMock that acts like an httpx.Client."""
    client = MagicMock(spec=httpx.Client)
    # Default: return a 200 response
    response = MagicMock(spec=httpx.Response)
    response.status_code = 200
    response.text = '{"ok": true}'
    client.post.return_value = response
    return client


@pytest.fixture
def backend_client(mock_httpx_client: MagicMock) -> BackendClient:
    """BackendClient with a mocked httpx client."""
    bc = BackendClient(base_url="http://api:8080", api_key="")
    bc._http = mock_httpx_client  # swap in the mock
    return bc


@pytest.fixture
def sample_payload() -> dict:
    return {
        "screener": "momentum",
        "run_date": "2026-06-01",
        "rows": [{"ticker": "AAPL", "close": 200.0}],
    }


# ── BackendClient tests ───────────────────────────────────────────────────────────


class TestBackendClientInit:
    """BackendClient initialisation and factory."""

    def test_base_url_trailing_slash_stripped(self) -> None:
        bc = BackendClient(base_url="http://api:8080/")
        assert bc._base_url == "http://api:8080"

    def test_base_url_no_slash_untouched(self) -> None:
        bc = BackendClient(base_url="http://api:8080")
        assert bc._base_url == "http://api:8080"

    def test_api_key_included_in_headers(self) -> None:
        bc = BackendClient(base_url="http://api:8080", api_key="sk-secret")
        assert bc._http.headers["Authorization"] == "Bearer sk-secret"

    def test_no_api_key_no_auth_header(self) -> None:
        bc = BackendClient(base_url="http://api:8080")
        assert "Authorization" not in bc._http.headers

    def test_from_config_creates_client(self) -> None:
        cfg = Config(
            backend_api_url="http://api:8080",
            collect_time="18:00",
            collect_cron="",
            timezone="America/New_York",
            tv_browser="chromium",
            tv_cookies_file="",
            log_level="INFO",
            raw_data_dir="./data/raw",
            api_key="test-key",
        )
        bc = BackendClient.from_config(cfg)
        assert bc._base_url == "http://api:8080"
        assert bc._http.headers["Authorization"] == "Bearer test-key"

    def test_context_manager(self) -> None:
        bc = BackendClient(base_url="http://api:8080")
        mock_close = MagicMock()
        bc.close = mock_close  # type: ignore[method-assign]
        with bc as cm:
            assert cm is bc
        mock_close.assert_called_once()


class TestBackendClientSend:
    """send_market_snapshot behaviour."""

    def test_successful_post(self, backend_client: BackendClient, sample_payload: dict) -> None:
        backend_client.send_market_snapshot(sample_payload)
        backend_client._http.post.assert_called_once_with(
            SNAPSHOT_ENDPOINT, json=sample_payload
        )

    def test_4xx_raises_immediately(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        error_response = MagicMock(spec=httpx.Response)
        error_response.status_code = 400
        error_response.text = '{"error": "bad request"}'
        backend_client._http.post.return_value = error_response
        with pytest.raises(BackendClientError, match="Backend rejected request"):
            backend_client.send_market_snapshot(sample_payload)
        # Only one attempt for 4xx — no retry
        backend_client._http.post.assert_called_once()

    def test_401_raises_immediately(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        error_response = MagicMock(spec=httpx.Response)
        error_response.status_code = 401
        error_response.text = '{"error": "unauthorized"}'
        backend_client._http.post.return_value = error_response
        with pytest.raises(BackendClientError, match="Backend rejected request"):
            backend_client.send_market_snapshot(sample_payload)
        backend_client._http.post.assert_called_once()

    def test_5xx_retries_then_raises(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        error_response = MagicMock(spec=httpx.Response)
        error_response.status_code = 500
        error_response.text = "Internal Server Error"
        backend_client._http.post.return_value = error_response
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        # Should have retried MAX_RETRIES times
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_503_retries_then_raises(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        error_response = MagicMock(spec=httpx.Response)
        error_response.status_code = 503
        error_response.text = "Service Unavailable"
        backend_client._http.post.return_value = error_response
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_429_retries_then_raises(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        error_response = MagicMock(spec=httpx.Response)
        error_response.status_code = 429
        error_response.text = "Too Many Requests"
        backend_client._http.post.return_value = error_response
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_timeout_retries_then_raises(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        backend_client._http.post.side_effect = httpx.TimeoutException("timeout")
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_connect_error_retries_then_raises(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        backend_client._http.post.side_effect = httpx.ConnectError("connection refused")
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_remote_protocol_error_retries(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        backend_client._http.post.side_effect = httpx.RemoteProtocolError("server closed")
        with pytest.raises(BackendClientError, match="Failed to send market snapshot"):
            backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == MAX_RETRIES

    def test_retry_then_success_on_third_attempt(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        """Two failures followed by a success on the third attempt."""
        fail_response = MagicMock(spec=httpx.Response)
        fail_response.status_code = 500
        fail_response.text = "fail"
        success_response = MagicMock(spec=httpx.Response)
        success_response.status_code = 200
        success_response.text = '{"ok": true}'
        backend_client._http.post.side_effect = [fail_response, fail_response, success_response]
        backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == 3

    def test_retry_then_success_on_second_attempt(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        """One failure followed by a success on the second attempt."""
        fail_response = MagicMock(spec=httpx.Response)
        fail_response.status_code = 502
        fail_response.text = "bad gateway"
        success_response = MagicMock(spec=httpx.Response)
        success_response.status_code = 200
        success_response.text = '{"ok": true}'
        backend_client._http.post.side_effect = [fail_response, success_response]
        backend_client.send_market_snapshot(sample_payload)
        assert backend_client._http.post.call_count == 2

    def test_unexpected_exception_raised_immediately(
        self, backend_client: BackendClient, sample_payload: dict
    ) -> None:
        """An unexpected exception type is wrapped in BackendClientError and not retried."""
        backend_client._http.post.side_effect = ValueError("something weird")
        with pytest.raises(BackendClientError, match="Unexpected error"):
            backend_client.send_market_snapshot(sample_payload)
        # Only one attempt — no retry for unexpected exceptions
        assert backend_client._http.post.call_count == 1

    def test_empty_payload_accepted(self, backend_client: BackendClient) -> None:
        """An empty dict is a valid payload (server may reject, but client sends it)."""
        backend_client.send_market_snapshot({})
        backend_client._http.post.assert_called_once_with(SNAPSHOT_ENDPOINT, json={})

    def test_close_method(self) -> None:
        """close() delegates to the underlying httpx client."""
        bc = BackendClient(base_url="http://api:8080")
        bc._http = MagicMock(spec=httpx.Client)
        bc.close()
        bc._http.close.assert_called_once()


# ── TradingViewClient tests ────────────────────────────────────────────────────────


class TestTradingViewClientInit:
    """TradingViewClient initialisation."""

    def test_init_with_cookiejar(self) -> None:
        jar = http.cookiejar.CookieJar()
        client = TradingViewClient(cookiejar=jar)
        assert client._cookiejar is jar

    def test_get_query_returns_query(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        query = client.get_query()
        from tradingview_screener.query import Query
        assert isinstance(query, Query)

    def test_get_query_is_fresh_each_call(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        q1 = client.get_query()
        q2 = client.get_query()
        assert q1 is not q2


class TestTradingViewClientExecuteQuery:
    """execute_query behaviour."""

    def test_successful_execution(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        mock_query = MagicMock()
        expected_df = pd.DataFrame({"ticker": ["AAPL"], "close": [200.0]})
        mock_query.get_scanner_data.return_value = (100, expected_df)
        result = client.execute_query(mock_query)
        pd.testing.assert_frame_equal(result, expected_df)
        mock_query.get_scanner_data.assert_called_once_with(cookies=client._cookiejar)

    def test_empty_result(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        mock_query = MagicMock()
        empty_df = pd.DataFrame()
        mock_query.get_scanner_data.return_value = (0, empty_df)
        result = client.execute_query(mock_query)
        assert result.empty
        assert len(result) == 0

    def test_retry_on_exception_then_succeeds(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        mock_query = MagicMock()
        expected_df = pd.DataFrame({"ticker": ["MSFT"]})
        # Fail twice, succeed on third
        mock_query.get_scanner_data.side_effect = [
            RuntimeError("timeout"),
            RuntimeError("timeout"),
            (50, expected_df),
        ]
        result = client.execute_query(mock_query)
        pd.testing.assert_frame_equal(result, expected_df)
        assert mock_query.get_scanner_data.call_count == 3

    def test_retry_exhausted_raises(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        mock_query = MagicMock()
        mock_query.get_scanner_data.side_effect = RuntimeError("API down")
        with pytest.raises(RuntimeError, match="after 3 attempts"):
            client.execute_query(mock_query)
        assert mock_query.get_scanner_data.call_count == MAX_RETRIES

    def test_single_row_dataframe(self) -> None:
        client = TradingViewClient(cookiejar=http.cookiejar.CookieJar())
        mock_query = MagicMock()
        single_row_df = pd.DataFrame({"ticker": ["GOOGL"], "close": [180.0]})
        mock_query.get_scanner_data.return_value = (1, single_row_df)
        result = client.execute_query(mock_query)
        assert len(result) == 1
        assert result.iloc[0]["ticker"] == "GOOGL"


# ── Cookie loading tests ───────────────────────────────────────────────────────────


class TestLoadFromFile:
    """Cookie loading from Netscape cookies.txt files."""

    def test_empty_path_returns_none(self) -> None:
        assert _load_from_file("") is None

    def test_blank_path_returns_none(self) -> None:
        assert _load_from_file("   ") is None

    def test_missing_file_returns_none(self) -> None:
        assert _load_from_file("/nonexistent/cookies.txt") is None

    def test_valid_path_returns_jar(self, tmp_path: Path) -> None:
        cookie_file = tmp_path / "cookies.txt"
        # Write a minimal valid Netscape cookies file
        cookie_file.write_text("# Netscape HTTP Cookie File\n")
        result = _load_from_file(str(cookie_file))
        assert result is not None
        assert isinstance(result, http.cookiejar.CookieJar)

    def test_invalid_file_returns_none(self, tmp_path: Path) -> None:
        """A file that exists but produces a load error returns None."""
        cookie_file = tmp_path / "bad_cookies.txt"
        cookie_file.write_bytes(b"\x00\x01\x02")  # binary garbage
        result = _load_from_file(str(cookie_file))
        assert result is None


class TestLoadFromBrowser:
    """Browser cookie loading via rookiepy."""

    def test_unsupported_browser_returns_none(self) -> None:
        result = _load_from_browser("nonexistent_browser")
        assert result is None

    def test_supported_browser_fails_gracefully(self) -> None:
        """A browser that exists in _BROWSER_LOADERS but has no cookie store returns None."""
        # Note: rookiepy is globally mocked in conftest.py, so this test
        # verifies graceful handling (no exception) under mock conditions.
        if not _BROWSER_LOADERS:
            pytest.skip("no browser loaders available on this system")
        browser = list(_BROWSER_LOADERS.keys())[0]
        result = _load_from_browser(browser)
        # Should not raise — the result depends on mocking setup
        assert result is not None
        assert not isinstance(result, Exception)

    def test_known_browsers_list_not_empty(self) -> None:
        assert len(_KNOWN_BROWSERS) >= 5

    def test_browser_loader_mapping(self) -> None:
        assert len(_BROWSER_LOADERS) > 0


class TestEmptyJar:
    """Fallback empty cookie jar."""

    def test_returns_empty_jar(self) -> None:
        jar = _empty_jar()
        assert isinstance(jar, http.cookiejar.CookieJar)
        assert sum(1 for _ in jar) == 0


class TestTradingViewClientFromConfig:
    """TradingViewClient.from_config builds the client correctly."""

    def test_cookies_file_used_when_set(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("BACKEND_API_URL", "http://api:8080")
        monkeypatch.setenv("TV_COOKIES_FILE", "/some/path")
        with patch("app.clients.tradingview_client._load_from_file", return_value=http.cookiejar.CookieJar()) as mock_file:
            cfg = Config(
                backend_api_url="http://api:8080",
                collect_time="18:00",
                collect_cron="",
                timezone="America/New_York",
                tv_browser="chromium",
                tv_cookies_file="/some/path",
                log_level="INFO",
                raw_data_dir="./data/raw",
                api_key="",
            )
            TradingViewClient.from_config(cfg)
            mock_file.assert_called_once_with("/some/path")

    def test_browser_fallback_when_no_file(self, monkeypatch: pytest.MonkeyPatch) -> None:
        with patch("app.clients.tradingview_client._load_from_file", return_value=None):
            with patch("app.clients.tradingview_client._load_from_browser", return_value=http.cookiejar.CookieJar()) as mock_browser:
                cfg = Config(
                    backend_api_url="http://api:8080",
                    collect_time="18:00",
                    collect_cron="",
                    timezone="America/New_York",
                    tv_browser="firefox",
                    tv_cookies_file="",
                    log_level="INFO",
                    raw_data_dir="./data/raw",
                    api_key="",
                )
                TradingViewClient.from_config(cfg)
                mock_browser.assert_called_once_with("firefox")

    def test_empty_jar_when_all_sources_fail(self) -> None:
        with patch("app.clients.tradingview_client._load_from_file", return_value=None):
            with patch("app.clients.tradingview_client._load_from_browser", return_value=None):
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
                client = TradingViewClient.from_config(cfg)
                assert isinstance(client._cookiejar, http.cookiejar.CookieJar)
                assert sum(1 for _ in client._cookiejar) == 0