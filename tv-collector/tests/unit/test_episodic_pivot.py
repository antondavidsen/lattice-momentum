"""
tests/unit/test_episodic_pivot.py
───────────────────────────────────
Unit tests for the episodic_pivot screener.

Tests cover:
  - COLUMNS list contains expected field names
  - run() with empty DataFrame returns empty DataFrame
  - run() with valid data returns filtered DataFrame
  - Dollar-volume post-filter: rows with close * volume < $5M are excluded
  - Close-range post-filter: rows where (close - low) / (high - low) < 0.70 are excluded
  - Revenue growth soft gate: rows with revenue_growth < 10% (and not NaN) are excluded
  - DataFrame with all-zero range (high == low) is excluded via guard
  - run() with bad headers (missing columns) returns empty DataFrame via validation
"""

from __future__ import annotations

from datetime import date
from unittest.mock import MagicMock, patch

import pandas as pd
import pytest

from app.screeners.episodic_pivot import COLUMNS, run
from app.screeners.stage1_filter import apply_stage1_filter


# ── COLUMNS sanity ────────────────────────────────────────────────────────────


class TestColumns:
    """Ensure COLUMNS has expected shape and content."""

    def test_columns_is_non_empty_list(self):
        assert isinstance(COLUMNS, list)
        assert len(COLUMNS) > 0

    def test_contains_required_fields(self):
        required = [
            "gap", "change", "relative_volume_10d_calc",
            "close", "open", "high", "low", "volume",
            "market_cap_basic",
        ]
        for field in required:
            assert field in COLUMNS, f"Missing required field: {field}"

    def test_columns_no_duplicates(self):
        assert len(COLUMNS) == len(set(COLUMNS)), "COLUMNS contains duplicate entries"


# ── Helpers ────────────────────────────────────────────────────────────────────


def _make_df(rows: list[dict]) -> pd.DataFrame:
    """Build a DataFrame from a list of dict rows."""
    return pd.DataFrame(rows)


def _valid_ep_row(**overrides: object) -> dict:
    """Return a dict that passes all episodic_pivot post-filters."""
    row: dict = {
        "ticker": "TEST",
        "name": "Test Corp",
        "open": 105.0,
        "high": 111.0,
        "low": 109.0,
        "close": 110.0,
        "volume": 500_000,
        "relative_volume_10d_calc": 4.0,
        "market_cap_basic": 500_000_000,
        "RSI": 65,
        "sector": "Technology",
        "gap": 4.0,
        "change": 6.0,
        "average_volume_10d_calc": 120_000,
        "earnings_release_next_date": "2026-07-15",
        "float_shares_outstanding": 50_000_000,
        "total_revenue_ttm": 1_000_000_000,
        "total_revenue_yoy_growth_ttm": 15.0,
        "earnings_per_share_basic_ttm": 4.5,
        "earnings_per_share_diluted_yoy_growth_fq": 20.0,
        "gross_margin": 60.0,
        "operating_margin": 25.0,
        "net_margin": 15.0,
        "return_on_equity_fq": 22.0,
        "Perf.W": 3.0,
        "Perf.1M": 18.0,
        "Perf.3M": 28.0,
        "Perf.6M": 45.0,
        "price_52_week_high": 120.0,
        "price_52_week_low": 75.0,
        "close[1]": 99.0,
        "SMA20": 106.0,
        "SMA50": 102.0,
        "SMA150": 95.0,
        "SMA200": 88.0,
        "ATR": 2.5,
    }
    row.update(overrides)
    return row


# ── run() tests ────────────────────────────────────────────────────────────────


class TestEpisodicPivotRun:
    """Tests for the run() function."""

    def test_empty_dataframe_returns_empty(self, mock_tradingview_client: MagicMock):
        """When TradingView returns an empty DataFrame, run() returns empty."""
        mock_tradingview_client.execute_query.return_value = pd.DataFrame()

        with patch.object(apply_stage1_filter, "__defaults__", ([], "", "")):
            result = run(mock_tradingview_client)
        assert result.empty

    def test_valid_data_returns_filtered_dataframe(self, mock_tradingview_client: MagicMock):
        """Valid rows passing all post-filters are returned."""
        rows = [_valid_ep_row(ticker="AAPL"), _valid_ep_row(ticker="MSFT")]
        df = _make_df(rows)
        mock_tradingview_client.execute_query.return_value = df

        with patch(
            "app.screeners.stage1_filter.apply_stage1_filter",
            return_value=MagicMock(
                passed=[{"ticker": "AAPL"}, {"ticker": "MSFT"}],
                flagged=[],
                eliminated=[],
                summary={"passed": 2, "hard_eliminated": 0, "soft_flagged": 0},
            ),
        ):
            with patch("app.screeners.audit_writer.write_audit", return_value=None):
                result = run(mock_tradingview_client)

        assert not result.empty
        assert len(result) == 2

    def test_dollar_volume_below_threshold_excluded(self, mock_tradingview_client: MagicMock):
        """Rows with close * volume < $5M should be excluded."""
        low_dollar_row = _valid_ep_row(ticker="LOW_DV", close=10.0, volume=100_000)  # $1M
        good_row = _valid_ep_row(ticker="GOOD", close=100.0, volume=1_000_000)      # $100M
        df = _make_df([low_dollar_row, good_row])
        mock_tradingview_client.execute_query.return_value = df

        with patch(
            "app.screeners.stage1_filter.apply_stage1_filter",
            return_value=MagicMock(
                passed=[{"ticker": "GOOD"}],
                flagged=[],
                eliminated=[],
                summary={"passed": 1, "hard_eliminated": 0, "soft_flagged": 0},
            ),
        ):
            with patch("app.screeners.audit_writer.write_audit", return_value=None):
                result = run(mock_tradingview_client)

        tickers = result["ticker"].tolist() if not result.empty else []
        assert "LOW_DV" not in tickers
        assert "GOOD" in tickers

    def test_close_range_too_low_excluded(self, mock_tradingview_client: MagicMock):
        """Rows where (close - low) / (high - low) < 0.70 should be excluded."""
        # close=100, low=90, high=110 → (100-90)/(110-90) = 10/20 = 0.50 → excluded
        weak_close = _valid_ep_row(ticker="WEAK", close=100.0, low=90.0, high=110.0)
        # close=105, low=90, high=110 → (105-90)/(110-90) = 15/20 = 0.75 → kept
        strong_close = _valid_ep_row(ticker="STRONG", close=105.0, low=90.0, high=110.0)
        df = _make_df([weak_close, strong_close])
        mock_tradingview_client.execute_query.return_value = df

        with patch(
            "app.screeners.stage1_filter.apply_stage1_filter",
            return_value=MagicMock(
                passed=[{"ticker": "STRONG"}],
                flagged=[],
                eliminated=[],
                summary={"passed": 1, "hard_eliminated": 0, "soft_flagged": 0},
            ),
        ):
            with patch("app.screeners.audit_writer.write_audit", return_value=None):
                result = run(mock_tradingview_client)

        tickers = result["ticker"].tolist() if not result.empty else []
        assert "WEAK" not in tickers
        assert "STRONG" in tickers

    def test_revenue_growth_below_ten_excluded(self, mock_tradingview_client: MagicMock):
        """Rows with revenue growth < 10% (and not NaN) should be excluded."""
        low_growth = _valid_ep_row(ticker="LOW_GR", total_revenue_yoy_growth_ttm=3.0)
        good_growth = _valid_ep_row(ticker="GOOD_GR", total_revenue_yoy_growth_ttm=15.0)
        nan_growth = _valid_ep_row(ticker="NAN_GR", total_revenue_yoy_growth_ttm=float("nan"))
        df = _make_df([low_growth, good_growth, nan_growth])
        mock_tradingview_client.execute_query.return_value = df

        with patch(
            "app.screeners.stage1_filter.apply_stage1_filter",
            return_value=MagicMock(
                passed=[{"ticker": "GOOD_GR"}, {"ticker": "NAN_GR"}],
                flagged=[],
                eliminated=[],
                summary={"passed": 2, "hard_eliminated": 0, "soft_flagged": 0},
            ),
        ):
            with patch("app.screeners.audit_writer.write_audit", return_value=None):
                result = run(mock_tradingview_client)

        tickers = result["ticker"].tolist() if not result.empty else []
        assert "LOW_GR" not in tickers
        assert "GOOD_GR" in tickers
        assert "NAN_GR" in tickers  # NaN growth should pass

    def test_zero_range_guard_excludes_row(self, mock_tradingview_client: MagicMock):
        """Rows where high == low (zero range) should be excluded."""
        zero_range = _valid_ep_row(ticker="ZERO_RNG", high=100.0, low=100.0, close=100.0)
        normal = _valid_ep_row(ticker="NORMAL", high=110.0, low=90.0, close=105.0)
        df = _make_df([zero_range, normal])
        mock_tradingview_client.execute_query.return_value = df

        with patch(
            "app.screeners.stage1_filter.apply_stage1_filter",
            return_value=MagicMock(
                passed=[{"ticker": "NORMAL"}],
                flagged=[],
                eliminated=[],
                summary={"passed": 1, "hard_eliminated": 0, "soft_flagged": 0},
            ),
        ):
            with patch("app.screeners.audit_writer.write_audit", return_value=None):
                result = run(mock_tradingview_client)

        tickers = result["ticker"].tolist() if not result.empty else []
        assert "ZERO_RNG" not in tickers
        assert "NORMAL" in tickers

    def test_bad_headers_returns_empty(self, mock_tradingview_client: MagicMock):
        """When the DataFrame has wrong columns, run() returns empty DataFrame."""
        df = pd.DataFrame({"wrong_col": [1, 2]})
        mock_tradingview_client.execute_query.return_value = df

        with patch("app.screeners.audit_writer.write_audit", return_value=None):
            result = run(mock_tradingview_client)

        assert result.empty