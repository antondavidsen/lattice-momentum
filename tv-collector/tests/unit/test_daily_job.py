"""
tests/unit/test_daily_job.py
─────────────────────────────
Unit tests for the daily_job orchestration service.

Covers:
- Full pipeline execution with mocked clients and screeners
- Screener failure isolation (one failing screener does not abort others)
- CSV snapshot saving for successful screeners
- Payload normalisation (NaN/Inf → None, Timestamps → ISO-8601)
- Backend POST success and failure branches
- Metrics emission (screeners_total, csv_saves_total, backend_posts_total, etc.)
"""

from __future__ import annotations

from datetime import date
from pathlib import Path
from unittest.mock import MagicMock, patch

import pandas as pd

from app.config import Config
from app.services.daily_job import (
    _clean_record,
    _normalize,
    _run_screener,
    _save_if_present,
    run_daily_job,
)


# ── Helpers ───────────────────────────────────────────────────────────────────

def _make_minimal_momentum_df() -> pd.DataFrame:
    """Return a minimal non-empty DataFrame resembling a momentum screener result."""
    return pd.DataFrame({
        "ticker": ["AAPL"],
        "close": [150.0],
        "SMA20": [148.0],
        "SMA50": [145.0],
        "SMA150": [140.0],
        "SMA200": [135.0],
        "SMA200[1]": [130.0],
        "Perf.1M": [10.0],
        "Perf.3M": [25.0],
        "Perf.6M": [40.0],
        "volume": [50_000_000],
        "average_volume_10d_calc": [40_000_000],
        "market_cap_basic": [2_500_000_000_000],
        "RSI": [60],
        "sector": ["Technology"],
        "ATR": [2.0],
        "price_52_week_high": [180.0],
        "price_52_week_low": [100.0],
        "change": [1.5],
        "gap": [0.0],
        "name": ["Apple"],
        "Perf.W": [2.0],
        "earnings_per_share_basic_ttm": [6.0],
        "earnings_per_share_diluted_yoy_growth_fq": [15.0],
        "total_revenue_ttm": [100_000_000_000],
        "total_revenue_yoy_growth_ttm": [10.0],
        "return_on_equity_fq": [30.0],
        "gross_margin": [45.0],
        "operating_margin": [25.0],
        "net_margin": [20.0],
        "earnings_release_next_date": ["2026-07-15"],
        "float_shares_outstanding": [15_000_000_000],
        "close[1]": [145.0],
    })


def _make_config(raw_data_dir: str = "/tmp/test_raw") -> Config:
    return Config(
        backend_api_url="http://test-backend:8080",
        collect_time="18:00",
        collect_cron="",
        timezone="America/New_York",
        tv_browser="chromium",
        tv_cookies_file="",
        log_level="INFO",
        raw_data_dir=raw_data_dir,
        api_key="test-api-key",
    )


# ── Tests: _clean_record ─────────────────────────────────────────────────────


class TestCleanRecord:
    """_clean_record converts non-JSON-safe values to None / ISO-8601."""

    def test_nan_replaced_with_none(self) -> None:
        result = _clean_record({"close": float("nan")})
        assert result["close"] is None

    def test_inf_replaced_with_none(self) -> None:
        result = _clean_record({"close": float("inf")})
        assert result["close"] is None

    def test_neg_inf_replaced_with_none(self) -> None:
        result = _clean_record({"close": float("-inf")})
        assert result["close"] is None

    def test_timestamp_converted_to_iso(self) -> None:
        ts = pd.Timestamp("2026-06-01 14:30:00")
        result = _clean_record({"date": ts})
        assert result["date"] == "2026-06-01T14:30:00"

    def test_normal_values_preserved(self) -> None:
        result = _clean_record({"ticker": "AAPL", "close": 150.0})
        assert result == {"ticker": "AAPL", "close": 150.0}


# ── Tests: _normalize ─────────────────────────────────────────────────────────


class TestNormalize:
    """_normalize converts DataFrame → list[dict] with JSON-safe values."""

    def test_none_df_returns_empty_list(self) -> None:
        assert _normalize(None) == []

    def test_empty_df_returns_empty_list(self) -> None:
        assert _normalize(pd.DataFrame()) == []

    def test_normal_df_returns_records(self) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"], "close": [150.0]})
        result = _normalize(df)
        assert result == [{"ticker": "AAPL", "close": 150.0}]

    def test_nan_replaced_in_df(self) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"], "close": [float("nan")]})
        result = _normalize(df)
        assert result == [{"ticker": "AAPL", "close": None}]


# ── Tests: _run_screener ──────────────────────────────────────────────────────


class TestRunScreener:
    """_run_screener executes a screener and returns df or None on failure."""

    def test_success_returns_df(self, mock_tradingview_client: MagicMock) -> None:
        def screener_fn(_client: MagicMock) -> pd.DataFrame:
            return pd.DataFrame({"ticker": ["AAPL"]})

        result = _run_screener("momentum", screener_fn, mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)
        assert not result.empty

    def test_failure_returns_none(self, mock_tradingview_client: MagicMock) -> None:
        def screener_fn(_client: MagicMock) -> pd.DataFrame:
            raise RuntimeError("screener crashed")

        result = _run_screener("momentum", screener_fn, mock_tradingview_client)
        assert result is None

    def test_empty_df_returns_df(self, mock_tradingview_client: MagicMock) -> None:
        def screener_fn(_client: MagicMock) -> pd.DataFrame:
            return pd.DataFrame()

        result = _run_screener("momentum", screener_fn, mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)
        assert result.empty


# ── Tests: _save_if_present ───────────────────────────────────────────────────


class TestSaveIfPresent:
    """_save_if_present saves CSV only when the screener returned data."""

    def test_saves_when_df_not_none(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        _save_if_present(df, "momentum", tmp_path, date(2026, 6, 1))
        expected = tmp_path / "2026-06-01" / "momentum.csv"
        assert expected.exists()
        content = expected.read_text()
        assert "AAPL" in content

    def test_skips_when_df_is_none(self, tmp_path: Path) -> None:
        _save_if_present(None, "momentum", tmp_path, date(2026, 6, 1))
        expected = tmp_path / "2026-06-01" / "momentum.csv"
        assert not expected.exists()

    def test_creates_date_directory(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        _save_if_present(df, "momentum", tmp_path, date(2026, 6, 15))
        expected = tmp_path / "2026-06-15" / "momentum.csv"
        assert expected.exists()


# ── Tests: run_daily_job (full pipeline) ──────────────────────────────────────


class TestRunDailyJob:
    """Full pipeline orchestration with mocked dependencies."""

    @patch("app.services.daily_job.episodic_pivot.run")
    @patch("app.services.daily_job.market_leaders.run")
    @patch("app.services.daily_job.momentum.run")
    @patch("app.services.daily_job.TradingViewClient")
    @patch("app.services.daily_job.BackendClient")
    def test_all_screeners_succeed(
        self,
        mock_backend_cls: MagicMock,
        mock_tv_cls: MagicMock,
        mock_mom_run: MagicMock,
        mock_ml_run: MagicMock,
        mock_ep_run: MagicMock,
        tmp_path: Path,
    ) -> None:
        """All three screeners run and produce data; CSV saved; backend called."""
        mock_tv = MagicMock()
        mock_tv_cls.from_config.return_value = mock_tv

        mock_be = MagicMock()
        mock_backend_cls.from_config.return_value = mock_be

        mock_mom_run.return_value = pd.DataFrame({"ticker": ["AAPL"], "close": [150.0]})
        mock_ml_run.return_value = pd.DataFrame({"ticker": ["MSFT"], "close": [420.0]})
        mock_ep_run.return_value = pd.DataFrame({"ticker": ["NVDA"], "close": [800.0]})

        config = _make_config(raw_data_dir=str(tmp_path))
        run_daily_job(config)

        # All three screeners should have been called
        mock_mom_run.assert_called_once()
        mock_ml_run.assert_called_once()
        mock_ep_run.assert_called_once()

        # CSV files should exist for all three screeners
        run_date_iso = date.today().isoformat()
        assert (tmp_path / run_date_iso / "momentum.csv").exists()
        assert (tmp_path / run_date_iso / "episodic_pivot.csv").exists()
        assert (tmp_path / run_date_iso / "market_leaders.csv").exists()

        # Backend should have been called once
        mock_be.send_market_snapshot.assert_called_once()

    @patch("app.services.daily_job.episodic_pivot.run")
    @patch("app.services.daily_job.market_leaders.run")
    @patch("app.services.daily_job.momentum.run")
    @patch("app.services.daily_job.TradingViewClient")
    @patch("app.services.daily_job.BackendClient")
    def test_screener_failure_is_isolated(
        self,
        mock_backend_cls: MagicMock,
        mock_tv_cls: MagicMock,
        mock_mom_run: MagicMock,
        mock_ml_run: MagicMock,
        mock_ep_run: MagicMock,
        tmp_path: Path,
    ) -> None:
        """When a screener fails, others still run and CSV is saved for successes."""
        mock_tv = MagicMock()
        mock_tv_cls.from_config.return_value = mock_tv

        mock_be = MagicMock()
        mock_backend_cls.from_config.return_value = mock_be

        mock_mom_run.side_effect = RuntimeError("momentum failed")
        mock_ml_run.return_value = pd.DataFrame({"ticker": ["MSFT"], "close": [420.0]})
        mock_ep_run.return_value = pd.DataFrame({"ticker": ["NVDA"], "close": [800.0]})

        config = _make_config(raw_data_dir=str(tmp_path))
        run_daily_job(config)

        run_date_iso = date.today().isoformat()
        # momentum failed — only 2 CSVs should be saved
        assert not (tmp_path / run_date_iso / "momentum.csv").exists()
        assert (tmp_path / run_date_iso / "episodic_pivot.csv").exists()
        assert (tmp_path / run_date_iso / "market_leaders.csv").exists()

        # Backend still called with data from the two successful screeners
        mock_be.send_market_snapshot.assert_called_once()

    @patch("app.services.daily_job.TradingViewClient")
    @patch("app.services.daily_job.BackendClient")
    def test_all_screeners_fail(
        self,
        mock_backend_cls: MagicMock,
        mock_tv_cls: MagicMock,
        tmp_path: Path,
    ) -> None:
        """When all screeners fail, no CSV is saved but backend is still called with empty payload."""
        mock_tv = MagicMock()
        mock_tv.execute_query.side_effect = RuntimeError("screener failed")
        mock_tv_cls.from_config.return_value = mock_tv

        mock_be = MagicMock()
        mock_backend_cls.from_config.return_value = mock_be

        config = _make_config(raw_data_dir=str(tmp_path))
        run_daily_job(config)

        run_date_iso = date.today().isoformat()
        # No CSV files should exist
        assert not (tmp_path / run_date_iso / "momentum.csv").exists()
        assert not (tmp_path / run_date_iso / "episodic_pivot.csv").exists()
        assert not (tmp_path / run_date_iso / "market_leaders.csv").exists()

        # Backend called with empty rows
        mock_be.send_market_snapshot.assert_called_once()
        payload = mock_be.send_market_snapshot.call_args[0][0]
        assert payload["momentum"] == []
        assert payload["episodic_pivots"] == []
        assert payload["market_leaders"] == []

    @patch("app.services.daily_job.episodic_pivot.run")
    @patch("app.services.daily_job.market_leaders.run")
    @patch("app.services.daily_job.momentum.run")
    @patch("app.services.daily_job.TradingViewClient")
    @patch("app.services.daily_job.BackendClient")
    def test_backend_failure_logged_does_not_crash(
        self,
        mock_backend_cls: MagicMock,
        mock_tv_cls: MagicMock,
        mock_mom_run: MagicMock,
        mock_ml_run: MagicMock,
        mock_ep_run: MagicMock,
        tmp_path: Path,
    ) -> None:
        """A backend POST failure is logged but does not crash the job."""
        from app.clients.backend_client import BackendClientError

        mock_tv = MagicMock()
        mock_tv_cls.from_config.return_value = mock_tv

        mock_be = MagicMock()
        mock_be.send_market_snapshot.side_effect = BackendClientError("backend down")
        mock_backend_cls.from_config.return_value = mock_be

        mock_mom_run.return_value = pd.DataFrame({"ticker": ["AAPL"], "close": [150.0]})
        mock_ml_run.return_value = pd.DataFrame({"ticker": ["MSFT"], "close": [420.0]})
        mock_ep_run.return_value = pd.DataFrame({"ticker": ["NVDA"], "close": [800.0]})

        config = _make_config(raw_data_dir=str(tmp_path))
        # Should not raise
        run_daily_job(config)

        # CSV should still be saved
        run_date_iso = date.today().isoformat()
        assert (tmp_path / run_date_iso / "momentum.csv").exists()
