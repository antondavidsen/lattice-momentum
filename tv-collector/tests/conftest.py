"""
tests/conftest.py
─────────────────
Shared pytest fixtures for the tv-collector test suite.

Provides:
- Factory fixtures for building synthetic ticker dicts
- Temporary directory fixtures for audit writer / storage tests
- Mocked httpx.Client fixtures for testing HTTP-based screeners
- Mock TradingViewClient fixtures
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any, Callable
from unittest.mock import MagicMock

import pandas as pd
import pytest

from app.screeners.stage1_filter import EliminationRecord


# ── Autouse: mock rookiepy only when not importable ────────────────────────────


@pytest.fixture(autouse=True, scope="session")
def _mock_rookiepy_if_unavailable():
    """Mock rookiepy when not importable, but use the real package when it is.

    CI's `poetry install` provides the real rookiepy binary; local dev on
    platforms where the native extension fails to build still sees green
    tests via this substitution. Tests that need a specific rookiepy
    behaviour should override this fixture at function scope.
    """
    import importlib
    try:
        importlib.import_module("rookiepy")
    except ImportError:
        mock = MagicMock()
        mock.__version__ = "0.0.0"
        sys.modules["rookiepy"] = mock
        yield
        sys.modules.pop("rookiepy", None)
    else:
        # Real rookiepy is available — leave it alone.
        yield


# ── Fixture: make_ticker (factory) ─────────────────────────────────────────────

@pytest.fixture
def make_ticker() -> Callable[..., dict[str, Any]]:
    """Return a factory that builds a minimal ticker dict for a given screener type.

    Call with overrides to customise specific fields.
    Default fields cover all Stage 1 filter rules.
    The *screener* parameter controls which columns are included:
        - "momentum" — all momentum columns (default)
        - "market_leaders" — market_leaders columns
        - "episodic_pivot" — episodic_pivot columns
    """
    def _momentum_base() -> dict[str, Any]:
        return {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": 110.0,
            "open": 105.0,
            "high": 111.0,
            "low": 109.0,
            "volume": 1_000_000,
            "average_volume_10d_calc": 800_000,
            "relative_volume_10d_calc": 1.25,
            "market_cap_basic": 5_000_000_000,
            "RSI": 60,
            "sector": "Technology",
            "change": 1.5,
            "gap": 0.0,
            "price_52_week_high": 115.0,
            "price_52_week_low": 80.0,
            "SMA20": 105.0,
            "SMA50": 100.0,
            "SMA150": 90.0,
            "SMA200": 85.0,
            "SMA200[1]": 82.0,
            "Perf.W": 2.0,
            "Perf.1M": 15.0,
            "Perf.3M": 25.0,
            "Perf.6M": 40.0,
            "ATR": 2.0,
            "earnings_per_share_basic_ttm": 5.0,
            "earnings_per_share_diluted_yoy_growth_fq": 30.0,
            "total_revenue_ttm": 1_000_000_000,
            "total_revenue_yoy_growth_ttm": 15.0,
            "return_on_equity_fq": 20.0,
            "gross_margin": 55.0,
            "operating_margin": 25.0,
            "net_margin": 15.0,
            "earnings_release_next_date": "2026-07-15",
            "float_shares_outstanding": 100_000_000,
            "close[1]": 100.0,
        }

    def _make(overrides: dict[str, Any] | None = None, screener: str = "momentum") -> dict[str, Any]:
        base = _momentum_base()

        if screener == "market_leaders":
            # market_leaders expects different columns from momentum
            base = {
                "ticker": "TEST",
                "name": "Test Corp",
                "close": 110.0,
                "open": 105.0,
                "high": 111.0,
                "low": 109.0,
                "volume": 1_000_000,
                "relative_volume_10d_calc": 1.25,
                "average_volume_10d_calc": 800_000,
                "market_cap_basic": 5_000_000_000,
                "RSI": 60,
                "sector": "Technology",
                "SMA20": 105.0,
                "SMA50": 100.0,
                "SMA150": 90.0,
                "SMA200": 85.0,
                "SMA200[1]": 82.0,
                "ATR": 2.0,
                "earnings_per_share_basic_ttm": 5.0,
                "earnings_per_share_diluted_ttm": 4.5,
                "earnings_per_share_diluted_yoy_growth_fq": 30.0,
                "total_revenue_ttm": 1_000_000_000,
                "total_revenue_yoy_growth_ttm": 15.0,
                "return_on_equity_fq": 20.0,
                "gross_margin": 55.0,
                "net_margin": 15.0,
                "operating_margin": 25.0,
                "earnings_release_next_date": "2026-07-15",
                "price_52_week_high": 115.0,
                "price_52_week_low": 80.0,
                "close[1]": 100.0,
                "Perf.3M": 25.0,
                "Perf.6M": 40.0,
            }
        elif screener == "episodic_pivot":
            base = {
                "ticker": "TEST",
                "name": "Test Corp",
                "open": 105.0,
                "high": 111.0,
                "low": 109.0,
                "close": 110.0,
                "volume": 1_000_000,
                "relative_volume_10d_calc": 1.25,
                "market_cap_basic": 5_000_000_000,
                "RSI": 60,
                "sector": "Technology",
                "gap": 0.0,
                "change": 1.5,
                "average_volume_10d_calc": 800_000,
                "earnings_release_next_date": "2026-07-15",
                "float_shares_outstanding": 100_000_000,
                "total_revenue_ttm": 1_000_000_000,
                "total_revenue_yoy_growth_ttm": 15.0,
                "earnings_per_share_basic_ttm": 5.0,
                "earnings_per_share_diluted_yoy_growth_fq": 30.0,
                "gross_margin": 55.0,
                "operating_margin": 25.0,
                "net_margin": 15.0,
                "return_on_equity_fq": 20.0,
                "Perf.W": 2.0,
                "Perf.1M": 15.0,
                "Perf.3M": 25.0,
                "Perf.6M": 40.0,
                "price_52_week_high": 115.0,
                "price_52_week_low": 80.0,
                "close[1]": 100.0,
                "SMA20": 105.0,
                "SMA50": 100.0,
                "SMA150": 90.0,
                "SMA200": 85.0,
                "ATR": 2.0,
            }

        if overrides:
            base.update(overrides)
        return base

    return _make


# ── Fixture: make_elimination_record (factory) ────────────────────────────────

@pytest.fixture
def valid_screener_row(make_ticker):
    """Return a valid row passing all schema validation rules."""
    return make_ticker()


@pytest.fixture
def make_elimination_record() -> Callable[..., EliminationRecord]:
    """Return a factory that builds an EliminationRecord with sensible defaults."""
    def _make(
        ticker: str = "TEST",
        screener: str = "momentum",
        rule: str = "climax_top",
        tier: str = "hard_eliminate",
        metrics: dict[str, Any] | None = None,
        reason: str = "test elimination",
        date: str = "2026-06-01",
    ) -> EliminationRecord:
        return EliminationRecord(
            ticker=ticker,
            screener=screener,
            rule=rule,
            tier=tier,
            metrics=metrics or {"today_range": 8.0, "atr": 2.0},
            reason=reason,
            date=date,
        )

    return _make


# ── Fixture: tmp_data_dir ─────────────────────────────────────────────────────

@pytest.fixture
def tmp_data_dir(tmp_path: Path) -> Path:
    """Return a temporary directory for raw snapshot storage tests."""
    data_dir = tmp_path / "data" / "raw"
    data_dir.mkdir(parents=True, exist_ok=True)
    return data_dir


# ── Fixture: tmp_log_dir ──────────────────────────────────────────────────────

@pytest.fixture
def tmp_log_dir(tmp_path: Path) -> Path:
    """Return a temporary directory for audit fallback logs."""
    log_dir = tmp_path / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    return log_dir


# ── Fixture: mock_httpx_client ────────────────────────────────────────────────

@pytest.fixture
def mock_httpx_client() -> MagicMock:
    """Return a MagicMock that mimics an httpx.Client for audit_writer tests.

    Usage:
        mock = mock_httpx_client
        mock.post.return_value.is_success = True
        mock.post.return_value.status_code = 200
    """
    mock = MagicMock()
    mock.post.return_value.is_success = True
    mock.post.return_value.status_code = 200
    return mock


# ── Autouse: set BACKEND_API_URL for all tests ────────────────────────────────


@pytest.fixture(autouse=True, scope="session")
def _set_backend_api_url() -> None:
    """Set BACKEND_API_URL so screeners can call load_config() at runtime."""
    import os
    os.environ.setdefault("BACKEND_API_URL", "http://localhost:9999")


# ── Fixture: mock_tradingview_client ──────────────────────────────────────────

@pytest.fixture
def mock_tradingview_client() -> MagicMock:
    """Return a MagicMock that mimics a TradingViewClient.

    The mock returns an empty DataFrame by default.
    Override mock.execute_query.return_value to return synthetic data.
    """
    mock = MagicMock()
    mock.get_query.return_value = MagicMock()
    mock.execute_query.return_value = pd.DataFrame()
    return mock


# ── Fixture: mock_backend_client ──────────────────────────────────────────────

@pytest.fixture
def mock_backend_client() -> MagicMock:
    """Return a MagicMock that mimics a BackendClient."""
    mock = MagicMock()
    mock.send_market_snapshot.return_value = None
    return mock


# ── Fixture: make_momentum_rows (factory) ─────────────────────────────────────

@pytest.fixture
def make_momentum_rows(make_ticker: Callable[..., dict[str, Any]]) -> Callable[..., list[dict[str, Any]]]:
    """Return a factory that builds a list of momentum screener rows.

    Each row contains all columns expected by the momentum schema.
    """
    def _make(count: int = 3, base_ticker: str = "TICK") -> list[dict[str, Any]]:
        tickers = []
        for i in range(count):
            ticker = make_ticker({
                "ticker": f"{base_ticker}{i}",
                "name": f"{base_ticker}{i} Corp",
                "close": 100.0 + i * 10,
                "volume": 1_000_000 + i * 100_000,
            })
            tickers.append(ticker)
        return tickers

    return _make


# ── Fixture: make_episodic_pivot_rows (factory) ───────────────────────────────

@pytest.fixture
def make_episodic_pivot_rows(make_ticker: Callable[..., dict[str, Any]]) -> Callable[..., list[dict[str, Any]]]:
    """Return a factory that builds a list of episodic_pivot screener rows.

    Each row contains columns expected by the episodic_pivot schema.
    """
    def _make(count: int = 3, base_ticker: str = "EP") -> list[dict[str, Any]]:
        rows = []
        for i in range(count):
            row = make_ticker({
                "ticker": f"{base_ticker}{i}",
                "name": f"{base_ticker}{i} Corp",
                "close": 200.0 + i * 20,
                "volume": 2_000_000 + i * 200_000,
                "Perf.W": 5.0 + i,
                "Perf.1M": 20.0 + i * 2,
                "Perf.3M": 30.0 + i * 3,
                "Perf.6M": 50.0 + i * 5,
            }, screener="episodic_pivot")
            rows.append(row)
        return rows

    return _make


# ── Fixture: make_market_leaders_rows (factory) ───────────────────────────────

@pytest.fixture
def make_market_leaders_rows(make_ticker: Callable[..., dict[str, Any]]) -> Callable[..., list[dict[str, Any]]]:
    """Return a factory that builds a list of market_leaders screener rows.

    Each row contains columns expected by the market_leaders schema.
    """
    def _make(count: int = 3, base_ticker: str = "ML") -> list[dict[str, Any]]:
        rows = []
        for i in range(count):
            row = make_ticker({
                "ticker": f"{base_ticker}{i}",
                "name": f"{base_ticker}{i} Corp",
                "close": 300.0 + i * 30,
                "market_cap_basic": 50_000_000_000 + i * 10_000_000_000,
                "earnings_per_share_diluted_yoy_growth_fq": 25.0 + i,
                "total_revenue_yoy_growth_ttm": 12.0 + i,
                "return_on_equity_fq": 25.0 + i,
            }, screener="market_leaders")
            rows.append(row)
        return rows

    return _make
