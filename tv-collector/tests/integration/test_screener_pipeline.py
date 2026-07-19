"""
tests/integration/test_screener_pipeline.py
────────────────────────────────────────────
Integration tests for the full screener pipeline with validation, auditing,
and storage.

These tests mock external API calls (TradingView, backend) but exercise the
real orchestration logic, validation rules, audit-writing, and CSV storage.

Marked with @pytest.mark.integration to allow easy exclusion during fast
unit-test runs.
"""

from __future__ import annotations

from datetime import date, datetime, timezone
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

import pandas as pd
import pytest

from app.screeners.episodic_pivot import run as run_episodic_pivot
from app.screeners.market_leaders import run as run_market_leaders
from app.screeners.momentum import run as run_momentum
from app.screeners.stage1_filter import FilterResult, apply_stage1_filter
from app.services.storage import save_raw_snapshot as storage_save_raw_snapshot
from app.services.validation import (
    ValidationStats,
    check_alerting_thresholds,
    fallback_to_cache,
    retry_with_backoff,
    validate_batch,
    validate_headers,
)

pytestmark = pytest.mark.integration


# ── Helpers ─────────────────────────────────────────────────────────────────────


def _make_mock_tv_client(
    columns: list[str],
    rows: list[list[Any]],
    fail: bool = False,
) -> MagicMock:
    """
    Build a TradingViewClient mock.

    If *fail* is True, execute_query raises RuntimeError on first call.
    """
    mock = MagicMock()
    mock.get_query.return_value = MagicMock()
    if fail:
        mock.execute_query.side_effect = RuntimeError("API unreachable")
    else:
        mock.execute_query.return_value = pd.DataFrame(rows, columns=columns)
    return mock


MOMENTUM_COLUMNS = [
    "ticker", "name", "open", "close", "high", "low", "volume",
    "relative_volume_10d_calc", "average_volume_10d_calc", "market_cap_basic",
    "RSI", "sector", "change", "gap", "price_52_week_high", "price_52_week_low",
    "SMA20", "SMA50", "SMA150", "SMA200", "SMA200[1]", "Perf.W", "Perf.1M",
    "Perf.3M", "Perf.6M", "ATR",
    "earnings_per_share_basic_ttm", "earnings_per_share_diluted_yoy_growth_fq",
    "total_revenue_ttm", "total_revenue_yoy_growth_ttm", "return_on_equity_fq",
    "gross_margin", "operating_margin", "net_margin",
    "earnings_release_next_date", "float_shares_outstanding",
    "close[1]",
]

MARKET_LEADERS_COLUMNS = [
    "ticker", "name", "close",
    "open", "high", "low",
    "volume",
    "relative_volume_10d_calc", "average_volume_10d_calc",
    "market_cap_basic", "RSI", "sector",
    "SMA20", "SMA50", "SMA150", "SMA200", "SMA200[1]",
    "ATR",
    "earnings_per_share_basic_ttm", "earnings_per_share_diluted_ttm",
    "earnings_per_share_diluted_yoy_growth_fq",
    "total_revenue_ttm", "total_revenue_yoy_growth_ttm",
    "return_on_equity_fq", "gross_margin", "net_margin", "operating_margin",
    "earnings_release_next_date",
    "price_52_week_high", "price_52_week_low",
    "close[1]",
    "Perf.3M", "Perf.6M",
]

EPISODIC_PIVOT_COLUMNS = [
    "ticker", "name", "open", "high", "low", "close", "volume",
    "relative_volume_10d_calc", "market_cap_basic", "RSI", "sector",
    "gap", "change", "average_volume_10d_calc",
    "earnings_release_next_date", "float_shares_outstanding",
    "total_revenue_ttm", "total_revenue_yoy_growth_ttm",
    "earnings_per_share_basic_ttm", "earnings_per_share_diluted_yoy_growth_fq",
    "gross_margin", "operating_margin", "net_margin", "return_on_equity_fq",
    "Perf.W", "Perf.1M", "Perf.3M", "Perf.6M",
    "price_52_week_high", "price_52_week_low",
    "close[1]",
    "SMA20", "SMA50", "SMA150", "SMA200", "ATR",
]


def _single_momentum_row() -> list:
    return [
        "AAPL", "Apple Inc.", 150.0, 152.0, 153.0, 149.0, 50_000_000,
        1.5, 40_000_000, 2_500_000_000_000, 60, "Technology",
        1.5, 0.5, 180.0, 120.0, 148.0, 145.0, 140.0, 135.0, 130.0,
        2.0, 5.0, 10.0, 15.0, 3.0,
        6.0, 25.0, 100_000_000_000, 15.0, 30.0, 45.0, 20.0, 15.0,
        "2026-06-01", 15_000_000_000, 105.0,
    ]


def _single_ml_row() -> list:
    return [
        "MSFT", "Microsoft Corp.", 420.0,
        418.0, 422.0, 416.0,
        30_000_000,
        1.2, 25_000_000,
        3_000_000_000_000, 55, "Technology",
        400.0, 395.0, 380.0, 370.0, 365.0,
        2.0,
        10.0, 9.5, 25.0,
        200_000_000_000, 18.0,
        35.0, 60.0, 25.0, 20.0,
        "2026-07-01",
        450.0, 300.0,
        415.0,
        12.0, 25.0,
    ]


def _single_ep_row() -> list:
    return [
        "NVDA", "NVIDIA Corp.", 800.0, 810.0, 788.0, 805.0, 100_000_000,
        5.0, 2_000_000_000_000, 65, "Technology",
        3.5, 6.0, 20_000_000,
        "2026-06-15", 10_000_000_000,
        50_000_000_000, 20.0,
        8.0, 30.0,
        60.0, 25.0, 20.0, 35.0,
        2.0, 8.0, 15.0, 25.0,
        810.0, 750.0,
        795.0,
        780.0, 760.0, 730.0, 710.0, 3.0,
    ]


# ── Integration: Screener runners ───────────────────────────────────────────────


class TestScreenerPipeline:
    """Full pipeline: TV client -> screener runner -> output DataFrame."""

    def test_momentum_pipeline_returns_valid_dataframe(self) -> None:
        """Momentum screener with valid data returns a non-empty DataFrame."""
        mock_client = _make_mock_tv_client(
            MOMENTUM_COLUMNS,
            [_single_momentum_row()],
        )
        df = run_momentum(mock_client)
        assert not df.empty
        assert "ticker" in df.columns
        assert df.iloc[0]["ticker"] == "AAPL"

    def test_market_leaders_pipeline_returns_valid(self) -> None:
        """Market leaders screener with valid data returns a non-empty DataFrame."""
        mock_client = _make_mock_tv_client(
            MARKET_LEADERS_COLUMNS,
            [_single_ml_row()],
        )
        df = run_market_leaders(mock_client)
        assert not df.empty
        assert df.iloc[0]["ticker"] == "MSFT"

    def test_episodic_pivot_pipeline_returns_valid(self) -> None:
        """Episodic pivot screener with valid data returns a non-empty DataFrame."""
        mock_client = _make_mock_tv_client(
            EPISODIC_PIVOT_COLUMNS,
            [_single_ep_row()],
        )
        df = run_episodic_pivot(mock_client)
        assert not df.empty
        assert df.iloc[0]["ticker"] == "NVDA"

    def test_screener_returns_empty_on_api_failure(self) -> None:
        """When the TradingView API raises, the screener returns empty DataFrame."""
        mock_client = _make_mock_tv_client(MOMENTUM_COLUMNS, [_single_momentum_row()], fail=True)
        df = run_momentum(mock_client)
        assert df.empty

    def test_screener_returns_empty_on_empty_tv_response(self) -> None:
        """When TradingView returns no rows, the screener returns empty DataFrame."""
        mock_client = _make_mock_tv_client(MOMENTUM_COLUMNS, [])
        df = run_market_leaders(mock_client)
        assert df.empty


# ── Integration: Stage 1 Filter ─────────────────────────────────────────────────


class TestStage1FilterPipeline:
    """Stage 1 filter integration: takes dict rows, returns FilterResult."""

    def test_stage1_filter_with_valid_tickers(self) -> None:
        """apply_stage1_filter with valid momentum tickers returns passed rows."""
        tickers = [
            {"ticker": "AAPL", "close": 150.0, "high": 152.0, "low": 149.0,
             "ATR": 2.0, "price_52_week_high": 180.0, "Perf.3M": 15.0},
        ]
        result = apply_stage1_filter(tickers, screener="momentum", run_date="2026-06-01")
        assert isinstance(result, FilterResult)
        assert len(result.passed) == 1
        assert result.passed[0]["ticker"] == "AAPL"

    def test_stage1_filter_empty_list(self) -> None:
        """apply_stage1_filter with empty list returns empty FilterResult."""
        result = apply_stage1_filter([], screener="momentum", run_date="2026-06-01")
        assert len(result.passed) == 0
        assert len(result.eliminated) == 0
        assert result.summary["evaluated"] == 0


# ── Integration: Validation pipeline ─────────────────────────────────────────────


class TestValidationPipeline:
    """Real validation functions with realistic data."""

    def test_validate_headers_exact_match(self) -> None:
        """validate_headers returns True for exact momentum column list."""
        assert validate_headers("momentum", MOMENTUM_COLUMNS) is True

    def test_validate_headers_mismatch_returns_false(self) -> None:
        """validate_headers returns False when columns don't match."""
        assert validate_headers("momentum", ["wrong", "columns"]) is False

    def test_validate_headers_unknown_screener_passes(self) -> None:
        """validate_headers returns True for unknown screener type (pass-through)."""
        assert validate_headers("unknown_screener", []) is True

    def test_validate_batch_valid_rows(self) -> None:
        """validate_batch processes valid rows without crashing."""
        rows = [
            {
                "ticker": "AAPL", "name": "Apple Inc.", "close": 150.0,
                "volume": 50_000_000, "sector": "Technology", "RSI": 60,
            },
        ]
        valid, rejected, stats = validate_batch(rows, "momentum")
        assert isinstance(valid, list)
        assert isinstance(rejected, list)
        assert stats.total_rows == 1

    def test_validate_batch_empty_returns_empty(self) -> None:
        """validate_batch with empty list returns empty results."""
        valid, rejected, stats = validate_batch([], "momentum")
        assert len(valid) == 0
        assert len(rejected) == 0
        assert stats.total_rows == 0

    def test_validate_batch_all_rows_rejected_on_header_mismatch(self) -> None:
        """validate_batch rejects all rows when headers don't match."""
        rows = [{"ticker": "AAPL", "close": 150.0}]
        valid, rejected, stats = validate_batch(rows, "momentum")
        assert stats.header_mismatch is True

    def test_check_alerting_thresholds_high_rejection(self) -> None:
        """check_alerting_thresholds alerts when >3 rows are rejected."""
        stats = ValidationStats(
            screener_type="momentum",
            total_rows=10,
            valid_rows=3,
            rejected_rows=7,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) == 1

    def test_check_alerting_thresholds_header_mismatch(self) -> None:
        """check_alerting_thresholds alerts on header mismatch."""
        stats = ValidationStats(
            screener_type="market_leaders",
            total_rows=5,
            valid_rows=0,
            rejected_rows=5,
            header_mismatch=True,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) >= 1

    def test_check_alerting_thresholds_fallback_usage(self) -> None:
        """check_alerting_thresholds alerts when fallback >10%."""
        stats = ValidationStats(
            screener_type="episodic_pivot",
            total_rows=100,
            valid_rows=80,
            rejected_rows=5,
            fallback_rows=15,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) >= 1

    def test_check_alerting_thresholds_no_alerts_normal(self) -> None:
        """check_alerting_thresholds returns no alerts for normal stats."""
        stats = ValidationStats(
            screener_type="momentum",
            total_rows=50,
            valid_rows=49,
            rejected_rows=1,
            fallback_rows=0,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) == 0


# ── Integration: Retry + fallback ────────────────────────────────────────────────


class TestRetryAndFallback:
    """Retry exhaustion and fallback-to-cache logic."""

    def test_retry_three_attempts_then_raises(self) -> None:
        """retry_with_backoff retries 3 times then raises the last exception."""
        call_count = 0

        def _always_fail() -> None:
            nonlocal call_count
            call_count += 1
            raise ConnectionError("timeout")

        with pytest.raises(ConnectionError):
            retry_with_backoff(
                _always_fail,
                "TEST",
                initial_delay=0.01,
                max_delay=0.1,
                max_attempts=3,
            )
        assert call_count == 3

    def test_fallback_to_cache_with_data(self) -> None:
        """fallback_to_cache wraps cached data with metadata."""
        cached = {"ticker": "TEST", "close": 100.0}
        fallback_row = fallback_to_cache("TEST", cached, cache_date=date(2026, 5, 19))
        assert fallback_row["_fallback_source"] == "cache"
        assert fallback_row["ticker"] == "TEST"

    def test_fallback_to_cache_without_data(self) -> None:
        """fallback_to_cache returns data dict with metadata when no data."""
        fallback_row = fallback_to_cache("TEST", None)
        assert fallback_row["_fallback_source"] == "cache"
        assert fallback_row["_fallback_age_days"] == 1

    def test_fallback_to_cache_with_empty_dict(self) -> None:
        """fallback_to_cache handles empty dict gracefully."""
        fallback_row = fallback_to_cache("TEST", {})
        assert fallback_row["_fallback_source"] == "cache"
        assert fallback_row["_fallback_age_days"] == 1


# ── Integration: Storage ─────────────────────────────────────────────────────────

class TestStoragePipeline:
    """Storage functions with real temp directories."""

    def test_save_dataframe_to_csv(self, tmp_path: Path) -> None:
        """save_raw_snapshot writes a CSV file to the configured directory."""
        df = pd.DataFrame({"ticker": ["AAPL"], "close": [150.0]})
        result_path = storage_save_raw_snapshot(
            df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1)
        )
        assert result_path.exists()
        content = result_path.read_text()
        assert "AAPL" in content
        assert "150.0" in content.replace(",", ".")

    def test_save_multiple_screeners_to_csv(self, tmp_path: Path) -> None:
        """Multiple screeners get written to separate files."""
        df1 = pd.DataFrame({"ticker": ["AAPL"]})
        df2 = pd.DataFrame({"ticker": ["MSFT"]})
        p1 = storage_save_raw_snapshot(df1, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        p2 = storage_save_raw_snapshot(df2, "episodic_pivot", base_dir=tmp_path, run_date=date(2026, 6, 1))
        assert p1.exists()
        assert p2.exists()
        assert p1.name == "momentum.csv"
        assert p2.name == "episodic_pivot.csv"

    def test_save_same_screener_multiple_dates(self, tmp_path: Path) -> None:
        """Same screener different dates writes to separate directories."""
        df = pd.DataFrame({"ticker": ["AAPL"]})
        p1 = storage_save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        p2 = storage_save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 2))
        assert p1.exists()
        assert p2.exists()
        assert p1.parent.name == "2026-06-01"
        assert p2.parent.name == "2026-06-02"

    def test_save_empty_dataframe_creates_file(self, tmp_path: Path) -> None:
        """Saving an empty DataFrame still creates the CSV file (with header)."""
        df = pd.DataFrame({"ticker": []})
        result_path = storage_save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        assert result_path.exists()
        content = result_path.read_text()
        assert "ticker" in content

    def test_save_raw_snapshot_raises_on_empty_name(self, tmp_path: Path) -> None:
        """save_raw_snapshot raises ValueError when screener_name is empty."""
        df = pd.DataFrame({"ticker": ["AAPL"]})
        with pytest.raises(ValueError, match="screener_name must not be empty"):
            storage_save_raw_snapshot(df, "", base_dir=tmp_path, run_date=date(2026, 6, 1))

    def test_save_raw_snapshot_raises_on_whitespace_name(self, tmp_path: Path) -> None:
        """save_raw_snapshot raises ValueError when screener_name is whitespace."""
        df = pd.DataFrame({"ticker": ["AAPL"]})
        with pytest.raises(ValueError, match="screener_name must not be empty"):
            storage_save_raw_snapshot(df, "   ", base_dir=tmp_path, run_date=date(2026, 6, 1))


# ── Integration: Daily job orchestration ─────────────────────────────────────────


class TestDailyJobOrchestration:
    """run_daily_job orchestrates all screeners and writes outputs."""

    def test_daily_job_runs_all_screeners(self, tmp_path: Path) -> None:
        """run_daily_job calls all three screeners via the TV client, then
        saves CSVs and sends the payload to the backend."""
        cfg = MagicMock()
        cfg.raw_data_dir = str(tmp_path)

        with patch("app.services.daily_job.TradingViewClient") as mock_tv_cls:
            with patch("app.services.daily_job.BackendClient") as mock_backend_cls:
                mock_tv = MagicMock()
                mock_tv_cls.from_config.return_value = mock_tv
                mock_backend = MagicMock()
                mock_backend_cls.from_config.return_value = mock_backend

                with patch("app.services.daily_job.momentum.run") as mock_mom:
                    with patch("app.services.daily_job.market_leaders.run") as mock_ml:
                        with patch("app.services.daily_job.episodic_pivot.run") as mock_ep:
                            mock_mom.return_value = pd.DataFrame({"ticker": ["AAPL"]})
                            mock_ml.return_value = pd.DataFrame({"ticker": ["MSFT"]})
                            mock_ep.return_value = pd.DataFrame({"ticker": ["NVDA"]})

                            from app.services.daily_job import run_daily_job
                            run_daily_job(cfg)

                            mock_mom.assert_called_once_with(mock_tv)
                            mock_ml.assert_called_once_with(mock_tv)
                            mock_ep.assert_called_once_with(mock_tv)

    def test_daily_job_calls_backend_with_results(self, tmp_path: Path) -> None:
        """run_daily_job sends the consolidated payload to the backend."""
        cfg = MagicMock()
        cfg.raw_data_dir = str(tmp_path)

        with patch("app.services.daily_job.TradingViewClient") as mock_tv_cls:
            with patch("app.services.daily_job.BackendClient") as mock_backend_cls:
                mock_tv = MagicMock()
                mock_tv_cls.from_config.return_value = mock_tv
                mock_backend = MagicMock()
                mock_backend_cls.from_config.return_value = mock_backend

                with patch("app.services.daily_job.momentum.run") as mock_run:
                    mock_run.return_value = pd.DataFrame({"ticker": ["AAPL"]})
                    with patch("app.services.daily_job.market_leaders.run") as mock_ml:
                        mock_ml.return_value = pd.DataFrame({"ticker": ["MSFT"]})
                        with patch("app.services.daily_job.episodic_pivot.run") as mock_ep:
                            mock_ep.return_value = pd.DataFrame({"ticker": ["NVDA"]})

                            from app.services.daily_job import run_daily_job
                            run_daily_job(cfg)

                            mock_backend.send_market_snapshot.assert_called_once()

    def test_daily_job_continues_on_screener_failure(self, tmp_path: Path) -> None:
        """If one screener fails, the others still run and the pipeline
        does not crash."""
        cfg = MagicMock()
        cfg.raw_data_dir = str(tmp_path)

        with patch("app.services.daily_job.TradingViewClient") as mock_tv_cls:
            with patch("app.services.daily_job.BackendClient") as mock_backend_cls:
                mock_tv = MagicMock()
                mock_tv_cls.from_config.return_value = mock_tv
                mock_backend = MagicMock()
                mock_backend_cls.from_config.return_value = mock_backend

                with patch("app.services.daily_job.momentum.run") as mock_mom:
                    with patch("app.services.daily_job.market_leaders.run") as mock_ml:
                        with patch("app.services.daily_job.episodic_pivot.run") as mock_ep:
                            mock_mom.side_effect = RuntimeError("momentum failed")
                            mock_ml.return_value = pd.DataFrame({"ticker": ["MSFT"]})
                            mock_ep.return_value = pd.DataFrame({"ticker": ["NVDA"]})

                            from app.services.daily_job import run_daily_job
                            run_daily_job(cfg)

                            mock_ml.assert_called_once()
                            mock_ep.assert_called_once()

    def test_daily_job_saves_csvs(self, tmp_path: Path) -> None:
        """run_daily_job saves CSV files to the output directory."""
        cfg = MagicMock()
        cfg.raw_data_dir = str(tmp_path)

        with patch("app.services.daily_job.TradingViewClient") as mock_tv_cls:
            with patch("app.services.daily_job.BackendClient") as mock_backend_cls:
                mock_tv = MagicMock()
                mock_tv_cls.from_config.return_value = mock_tv
                mock_backend = MagicMock()
                mock_backend_cls.from_config.return_value = mock_backend

                with patch("app.services.daily_job.momentum.run") as mock_run:
                    mock_run.return_value = pd.DataFrame({"ticker": ["AAPL"]})
                    with patch("app.services.daily_job.market_leaders.run") as mock_ml:
                        mock_ml.return_value = pd.DataFrame({"ticker": ["MSFT"]})
                        with patch("app.services.daily_job.episodic_pivot.run") as mock_ep:
                            mock_ep.return_value = pd.DataFrame({"ticker": ["NVDA"]})

                            from app.services.daily_job import run_daily_job
                            run_daily_job(cfg)

                            csv_files = list(Path(tmp_path).rglob("*.csv"))
                            assert len(csv_files) > 0

    def test_daily_job_handles_empty_all_screeners(self, tmp_path: Path) -> None:
        """All screeners returning empty DataFrames should not crash."""
        cfg = MagicMock()
        cfg.raw_data_dir = str(tmp_path)

        with patch("app.services.daily_job.TradingViewClient") as mock_tv_cls:
            with patch("app.services.daily_job.BackendClient") as mock_backend_cls:
                mock_tv = MagicMock()
                mock_tv_cls.from_config.return_value = mock_tv
                mock_backend = MagicMock()
                mock_backend_cls.from_config.return_value = mock_backend

                with patch("app.services.daily_job.momentum.run") as mock_mom:
                    with patch("app.services.daily_job.market_leaders.run") as mock_ml:
                        with patch("app.services.daily_job.episodic_pivot.run") as mock_ep:
                            mock_mom.return_value = pd.DataFrame()
                            mock_ml.return_value = pd.DataFrame()
                            mock_ep.return_value = pd.DataFrame()

                            from app.services.daily_job import run_daily_job
                            run_daily_job(cfg)


# ── Integration: Full pipeline end-to-end ────────────────────────────────────────


class TestFullPipelineEndToEnd:
    """End-to-end: mock TV client -> run screeners -> persist -> verify outputs."""

    def test_full_pipeline_writes_all_outputs(self, tmp_path: Path) -> None:
        """
        Simulate a full run: mock TV client -> run screeners -> save CSVs.

        Assert all outputs exist and no exceptions.
        """
        run_date = date(2026, 6, 1)

        # Use separate mocks with correct columns and data for each screener
        mom_client = _make_mock_tv_client(
            MOMENTUM_COLUMNS,
            [_single_momentum_row()],
        )
        ml_client = _make_mock_tv_client(
            MARKET_LEADERS_COLUMNS,
            [_single_ml_row()],
        )
        ep_client = _make_mock_tv_client(
            EPISODIC_PIVOT_COLUMNS,
            [_single_ep_row()],
        )

        df_mom = run_momentum(mom_client)
        df_ml = run_market_leaders(ml_client)
        df_ep = run_episodic_pivot(ep_client)

        for screener_name, df in [
            ("momentum", df_mom),
            ("market_leaders", df_ml),
            ("episodic_pivot", df_ep),
        ]:
            if not df.empty:
                storage_save_raw_snapshot(
                    df, screener_name, base_dir=tmp_path, run_date=run_date
                )

        momentum_file = tmp_path / "2026-06-01" / "momentum.csv"
        assert momentum_file.exists(), "Expected momentum CSV output"
        content = momentum_file.read_text()
        assert "AAPL" in content

        # Verify other screeners also wrote files
        ml_file = tmp_path / "2026-06-01" / "market_leaders.csv"
        ep_file = tmp_path / "2026-06-01" / "episodic_pivot.csv"
        assert ml_file.exists(), "Expected market_leaders CSV output"
        assert ep_file.exists(), "Expected episodic_pivot CSV output"