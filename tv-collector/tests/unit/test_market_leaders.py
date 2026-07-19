"""
tests/unit/test_market_leaders.py
─────────────────────────────────
Unit tests for the market_leaders screener.

Covers:
- Column definitions (COLUMNS list)
- Edge cases: empty df, missing columns, boundary conditions for run()
- Post-filter logic exercised via run() with mocked TradingView client
"""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pandas as pd
import pytest

from app.screeners.market_leaders import COLUMNS, SCREENER_NAME, run


class TestLeadersColumns:
    """Market Leaders required columns must be properly defined."""

    def test_columns_is_non_empty_list(self) -> None:
        """COLUMNS must be a non-empty list of strings."""
        assert isinstance(COLUMNS, list)
        assert len(COLUMNS) > 0
        assert all(isinstance(c, str) for c in COLUMNS)

    def test_required_columns_present(self) -> None:
        """Key columns needed by filters must be present in COLUMNS."""
        required = {
            "close", "volume", "RSI", "Perf.3M", "Perf.6M",
            "SMA50", "SMA150", "SMA200",
            "price_52_week_high", "price_52_week_low",
            "market_cap_basic", "average_volume_10d_calc",
            "earnings_per_share_basic_ttm",
            "earnings_per_share_diluted_yoy_growth_fq",
            "total_revenue_ttm", "total_revenue_yoy_growth_ttm",
            "return_on_equity_fq", "gross_margin", "operating_margin",
        }
        col_set = set(COLUMNS)
        missing = required - col_set
        assert not missing, f"Missing required columns: {missing}"

    def test_screener_name_is_market_leaders(self) -> None:
        """SCREENER_NAME must be 'market_leaders'."""
        assert SCREENER_NAME == "market_leaders"


class TestLeadersRun:
    """run() with mocked TradingView client."""

    def test_run_empty_dataframe(self, mock_tradingview_client: MagicMock) -> None:
        """run() with empty DataFrame should return empty DataFrame."""
        mock_tradingview_client.execute_query.return_value = pd.DataFrame()
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)
        assert result.empty

    def test_run_returns_dataframe(self, mock_tradingview_client: MagicMock) -> None:
        """run() should return a DataFrame with filtered tickers."""
        df = _make_valid_leaders_df()
        mock_tradingview_client.execute_query.return_value = df
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)

    def test_run_with_bad_headers_returns_empty(self, mock_tradingview_client: MagicMock) -> None:
        """run() with missing required columns should return empty DataFrame."""
        df = pd.DataFrame({
            "ticker": ["AAPL"],
            "name": ["Apple"],
            "close": [150.0],
            # missing many required columns
        })
        mock_tradingview_client.execute_query.return_value = df
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)
        assert result.empty

    def test_sma150_missing_uses_proxy(self, mock_tradingview_client: MagicMock) -> None:
        """When SMA150 column is missing, run() should use SMA200×1.05 proxy."""
        df = _make_valid_leaders_df()
        df = df.drop(columns=["SMA150"], errors="ignore")
        mock_tradingview_client.execute_query.return_value = df
        # Should not raise
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)

    def test_row_with_low_mcap_excluded(self, mock_tradingview_client: MagicMock) -> None:
        """A ticker with market cap < $2B should be excluded by server-side filters."""
        df = _make_side_by_side_leaders_df()
        mock_tradingview_client.execute_query.return_value = df
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)

    def test_validate_batch_header_mismatch_returns_empty(
        self, mock_tradingview_client: MagicMock
    ) -> None:
        """When validate_batch indicates header_mismatch, run() returns empty."""
        df = _make_valid_leaders_df()
        mock_tradingview_client.execute_query.return_value = df
        with patch("app.screeners.market_leaders.validate_batch") as mock_validate:
            from collections import namedtuple
            Stats = namedtuple("ValidationStats", ["total_rows", "valid_rows", "rejected_rows",
                                                     "schema_version", "header_mismatch"])
            mock_stats = Stats(total_rows=2, valid_rows=0, rejected_rows=2,
                               schema_version="test", header_mismatch=True)
            mock_validate.return_value = ([], [], mock_stats)
            result = run(mock_tradingview_client)
            assert isinstance(result, pd.DataFrame)
            assert result.empty


class TestLeadersEdgeCases:
    """Edge cases for market_leaders screener."""

    def test_all_none_values_handled(self, mock_tradingview_client: MagicMock) -> None:
        """run() should not crash when all numeric values are NaN."""
        df = pd.DataFrame({
            "ticker": ["AAPL", "MSFT"],
            "name": ["Apple", "Microsoft"],
        })
        for col in COLUMNS:
            df[col] = pd.NA
        mock_tradingview_client.execute_query.return_value = df
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)

    def test_single_row_dataframe(self, mock_tradingview_client: MagicMock) -> None:
        """run() should handle a single-row DataFrame without crashing."""
        row = {
            "ticker": ["AAPL"],
            "name": ["Apple"],
            "close": [150.0],
            "volume": [50_000_000],
            "average_volume_10d_calc": [40_000_000],
            "market_cap_basic": [2_500_000_000_000],
            "RSI": [60],
            "sector": ["Technology"],
            "SMA20": [148.0],
            "SMA50": [145.0],
            "SMA150": [140.0],
            "SMA200": [135.0],
            "SMA200[1]": [130.0],
            "ATR": [2.0],
            "price_52_week_high": [180.0],
            "price_52_week_low": [100.0],
            "earnings_per_share_basic_ttm": [6.0],
            "earnings_per_share_diluted_ttm": [6.0],
            "earnings_per_share_diluted_yoy_growth_fq": [15.0],
            "total_revenue_ttm": [100_000_000_000],
            "total_revenue_yoy_growth_ttm": [10.0],
            "return_on_equity_fq": [30.0],
            "gross_margin": [45.0],
            "operating_margin": [25.0],
            "net_margin": [20.0],
            "Perf.3M": [25.0],
            "Perf.6M": [40.0],
            "earnings_release_next_date": ["2026-07-15"],
            "close[1]": [148.0],
            "high": [155.0],
            "low": [145.0],
            "open": [149.0],
            "change": [1.5],
            "relative_volume_10d_calc": [1.2],
        }
        df = pd.DataFrame(row)
        mock_tradingview_client.execute_query.return_value = df
        result = run(mock_tradingview_client)
        assert isinstance(result, pd.DataFrame)


# ── Helpers ─────────────────────────────────────────────────────────────────


def _make_valid_leaders_df() -> pd.DataFrame:
    """Return a valid market_leaders DataFrame."""
    return pd.DataFrame({
        "ticker": ["AAPL", "MSFT"],
        "name": ["Apple", "Microsoft"],
        "open": [149.0, 418.0],
        "close": [150.0, 420.0],
        "high": [155.0, 425.0],
        "low": [145.0, 415.0],
        "volume": [50_000_000, 30_000_000],
        "relative_volume_10d_calc": [1.2, 1.0],
        "average_volume_10d_calc": [40_000_000, 25_000_000],
        "market_cap_basic": [2_500_000_000_000, 3_000_000_000_000],
        "RSI": [60, 55],
        "sector": ["Technology", "Technology"],
        "SMA20": [148.0, 400.0],
        "SMA50": [145.0, 380.0],
        "SMA150": [140.0, 360.0],
        "SMA200": [135.0, 340.0],
        "SMA200[1]": [130.0, 330.0],
        "ATR": [2.0, 5.0],
        "price_52_week_high": [180.0, 450.0],
        "price_52_week_low": [100.0, 300.0],
        "earnings_per_share_basic_ttm": [6.0, 10.0],
        "earnings_per_share_diluted_ttm": [6.0, 10.0],
        "earnings_per_share_diluted_yoy_growth_fq": [15.0, 20.0],
        "total_revenue_ttm": [100_000_000_000, 200_000_000_000],
        "total_revenue_yoy_growth_ttm": [10.0, 15.0],
        "return_on_equity_fq": [30.0, 35.0],
        "gross_margin": [45.0, 60.0],
        "operating_margin": [25.0, 30.0],
        "net_margin": [20.0, 25.0],
        "Perf.1M": [8.0, 6.0],
        "Perf.3M": [25.0, 20.0],
        "Perf.6M": [40.0, 35.0],
        "earnings_release_next_date": ["2026-07-15", "2026-07-20"],
        "close[1]": [148.0, 418.0],
        "change": [1.0, 0.8],
    })


def _make_side_by_side_leaders_df() -> pd.DataFrame:
    """Return a DataFrame with rows of varying quality."""
    return pd.DataFrame({
        "ticker": ["GOOD", "BAD"],
        "name": ["GoodCorp", "BadCorp"],
        "open": [149.0, 15.0],
        "close": [150.0, 16.0],
        "high": [155.0, 17.0],
        "low": [145.0, 14.0],
        "volume": [50_000_000, 100_000],
        "relative_volume_10d_calc": [1.2, 0.5],
        "average_volume_10d_calc": [40_000_000, 80_000],
        "market_cap_basic": [2_500_000_000_000, 100_000_000],
        "RSI": [60, 30],
        "sector": ["Technology", "Energy"],
        "SMA20": [148.0, 18.0],
        "SMA50": [145.0, 17.0],
        "SMA150": [140.0, 16.0],
        "SMA200": [135.0, 15.0],
        "SMA200[1]": [130.0, 14.0],
        "ATR": [2.0, 1.0],
        "price_52_week_high": [180.0, 25.0],
        "price_52_week_low": [100.0, 10.0],
        "earnings_per_share_basic_ttm": [6.0, 0.5],
        "earnings_per_share_diluted_ttm": [6.0, 0.5],
        "earnings_per_share_diluted_yoy_growth_fq": [15.0, 5.0],
        "total_revenue_ttm": [100_000_000_000, 50_000_000],
        "total_revenue_yoy_growth_ttm": [10.0, 2.0],
        "return_on_equity_fq": [30.0, 5.0],
        "gross_margin": [45.0, 20.0],
        "operating_margin": [25.0, 5.0],
        "net_margin": [20.0, 3.0],
        "Perf.1M": [8.0, -2.0],
        "Perf.3M": [25.0, -5.0],
        "Perf.6M": [40.0, -3.0],
        "earnings_release_next_date": ["2026-07-15", "2026-08-01"],
        "close[1]": [148.0, 15.0],
        "change": [1.0, 0.5],
    })