"""
app/screeners/test_stage1_filter.py
────────────────────────────────────
Unit tests for stage 1 hard elimination rules.

Tests cover:
- Rule A — Climax Top Detection (Momentum + Leaders)
- Rule B — Financial Engineering Flag (Leaders only)
- Rule C — Exhaustion Risk (EP only, flag-only)
- gap_pct computation correctness
- Clean pass-through for unremarkable tickers
- Audit writer (with mocked HTTP)
"""

from __future__ import annotations

from typing import Any
from unittest.mock import patch

import pytest

from app.screeners.stage1_filter import (
    EXT_RANGE_MULT_HARD,
    EXT_RANGE_MULT_SOFT,
    EXT_PERF3M_MIN,
    EXHAUST_GAP_MIN,
    EXHAUST_PERF1M_MIN,
    FUND_EPS_REV_RATIO,
    FUND_REV_GROWTH_MAX,
    EliminationRecord,
    FilterResult,
    _compute_gap_pct,
    apply_stage1_filter,
)

TEST_DATE = "2026-05-20"


# ── Helpers ─────────────────────────────────────────────────────────────────────


def _make_ticker(overrides: dict[str, Any] | None = None) -> dict[str, Any]:
    """Build a minimal ticker dict with all fields needed by stage 1 rules."""
    ticker = {
        "ticker": "TEST",
        "name": "Test Corp",
        "close": 110.0,
        "high": 111.0,
        "low": 109.0,
        "ATR": 2.0,
        "price_52_week_high": 115.0,
        "open": 105.0,
        "close[1]": 100.0,
        "Perf.3M": 25.0,
        "Perf.1M": 15.0,
        "earnings_per_share_diluted_yoy_growth_fq": 30.0,
        "total_revenue_yoy_growth_ttm": 15.0,
    }
    if overrides:
        ticker.update(overrides)
    return ticker


# ═══════════════════════════════════════════════════════════════════════════════
# Rule A — Climax Top Detection (Momentum + Leaders only)
# ═══════════════════════════════════════════════════════════════════════════════


class TestClimaxTopRule:
    """Climax top: range >= 3×ATR + close > 52W high + perf_3m > 50% → hard eliminate."""

    def test_climax_top_hard_eliminate(self) -> None:
        """range=6.0 (3×ATR=6.0), close=116 > 52W high=115, perf_3m=55% → hard eliminated."""
        ticker = _make_ticker({
            "high": 118.0, "low": 112.0, "ATR": 2.0,
            "close": 116.0, "price_52_week_high": 115.0, "Perf.3M": 55.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 1
        assert result.eliminated[0].rule == "climax_top"
        assert result.eliminated[0].tier == "hard_eliminate"
        assert result.eliminated[0].ticker == "TEST"
        assert len(result.passed) == 0
        assert len(result.flagged) == 0

    def test_climax_top_boundary_exact_3x_atr(self) -> None:
        """range=6.0 exactly 3×ATR=6.0 → hard eliminated (>= triggers)."""
        ticker = _make_ticker({
            "high": 118.0, "low": 112.0, "ATR": 2.0,
            "close": 116.0, "price_52_week_high": 115.0, "Perf.3M": 55.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 1
        assert result.eliminated[0].rule == "climax_top"

    def test_climax_top_below_52w_high_passes(self) -> None:
        """close=114 < 52W high=115 → NOT eliminated (still in base)."""
        ticker = _make_ticker({
            "high": 115.0, "low": 112.0, "ATR": 2.0,
            "close": 114.0, "price_52_week_high": 115.0, "Perf.3M": 55.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.passed) == 1

    def test_climax_top_perf3m_below_threshold_passes(self) -> None:
        """perf_3m=45% < 50% → NOT eliminated."""
        ticker = _make_ticker({
            "high": 115.0, "low": 112.0, "ATR": 2.0,
            "close": 116.0, "price_52_week_high": 115.0, "Perf.3M": 45.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.passed) == 1

    def test_climax_top_wide_range_soft_flag(self) -> None:
        """range=4.5 (2.25×ATR) >= 2×ATR, perf_3m=45% > 40% → soft flag, not eliminated."""
        ticker = _make_ticker({
            "high": 116.5, "low": 112.0, "ATR": 2.0,
            "close": 114.0, "price_52_week_high": 115.0, "Perf.3M": 45.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 1
        assert "WIDE_RANGE_WARNING" in result.flagged[0]["risk_flags"]

    def test_climax_top_rule_skipped_for_ep(self) -> None:
        """range=8.0 (4×ATR), close above 52W high, screener=ep → NOT eliminated."""
        ticker = _make_ticker({
            "high": 120.0, "low": 112.0, "ATR": 2.0,
            "close": 118.0, "price_52_week_high": 115.0, "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.passed) == 1

    def test_climax_top_missing_atr_skips_rule(self) -> None:
        """ATR=None → rule skipped, not eliminated."""
        ticker = _make_ticker({
            "high": 120.0, "low": 112.0, "ATR": None,
            "close": 118.0, "price_52_week_high": 115.0, "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.passed) == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Rule B — Financial Engineering Flag (Leaders only)
# ═══════════════════════════════════════════════════════════════════════════════


class TestFinancialEngineeringFlag:
    """Financial engineering: EPS growth > 3× rev growth + rev growth < 5% → soft flag."""

    def test_financial_engineering_flag_applied(self) -> None:
        """eps_growth=60%, rev_growth=4% → FINANCIAL_ENGINEERING_RISK flag."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 60.0,
            "total_revenue_yoy_growth_ttm": 4.0,
        })
        result = apply_stage1_filter([ticker], "leaders", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 1
        assert "FINANCIAL_ENGINEERING_RISK" in result.flagged[0]["risk_flags"]

    def test_financial_engineering_rev_above_threshold_passes(self) -> None:
        """eps_growth=60%, rev_growth=10% (above 5%) → no flag."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 60.0,
            "total_revenue_yoy_growth_ttm": 10.0,
        })
        result = apply_stage1_filter([ticker], "leaders", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_financial_engineering_eps_rev_ratio_below_threshold_passes(self) -> None:
        """eps_growth=20%, rev_growth=10% (ratio=2.0 < 3.0) → no flag."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 20.0,
            "total_revenue_yoy_growth_ttm": 10.0,
        })
        result = apply_stage1_filter([ticker], "leaders", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_financial_engineering_none_field_skips_rule(self) -> None:
        """eps_growth=None → rule skipped, no crash."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": None,
            "total_revenue_yoy_growth_ttm": 4.0,
        })
        result = apply_stage1_filter([ticker], "leaders", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_financial_engineering_rule_skipped_for_momentum(self) -> None:
        """eps_growth=60%, rev_growth=4%, screener=momentum → no flag."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 60.0,
            "total_revenue_yoy_growth_ttm": 4.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_financial_engineering_negative_rev_skips_rule(self) -> None:
        """rev_growth=-5% (negative) → rule skipped (division guard)."""
        ticker = _make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 60.0,
            "total_revenue_yoy_growth_ttm": -5.0,
        })
        result = apply_stage1_filter([ticker], "leaders", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Rule C — Exhaustion Risk (EP only, flag-only)
# ═══════════════════════════════════════════════════════════════════════════════


class TestExhaustionRiskRule:
    """Exhaustion risk: gap_pct > EXHAUST_GAP_MIN + perf_1m > EXHAUST_PERF1M_MIN → flag."""

    def test_exhaustion_flag_applied(self) -> None:
        """gap_pct=42.9%, perf_1m=60.0% → EXHAUSTION_RISK flag, not eliminated."""
        ticker = _make_ticker({
            "open": 50.0,
            "close[1]": 35.0,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 1
        assert "EXHAUSTION_RISK" in result.flagged[0]["risk_flags"]
        assert result.flagged[0]["gap_pct"] == pytest.approx(42.86, rel=0.01)

    def test_exhaustion_no_flag_below_threshold(self) -> None:
        """gap_pct=25.0%, perf_1m=60.0% → no flag (gap below 30%)."""
        ticker = _make_ticker({
            "open": 125.0,
            "close[1]": 100.0,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_exhaustion_rule_skipped_for_momentum(self) -> None:
        """gap_pct=40.0%, perf_1m=70.0%, screener=momentum → no flag."""
        ticker = _make_ticker({
            "open": 140.0,
            "close[1]": 100.0,
            "Perf.1M": 70.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_exhaustion_none_prev_close_skips_rule(self) -> None:
        """prev_close=None → rule skipped, no crash."""
        ticker = _make_ticker({
            "open": 50.0,
            "close[1]": None,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", TEST_DATE)
        assert len(result.flagged) == 0
        assert len(result.passed) == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Clean pass-through
# ═══════════════════════════════════════════════════════════════════════════════


class TestCleanPassThrough:
    """Unremarkable tickers pass through unchanged."""

    def test_clean_momentum_ticker_passes_unchanged(self) -> None:
        """range=2.0 (1×ATR), close below 52W high, perf_3m=25% → passes, risk_flags=[]."""
        ticker = _make_ticker({
            "high": 112.0, "low": 110.0, "ATR": 2.0,
            "close": 110.0, "price_52_week_high": 115.0, "Perf.3M": 25.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 0
        assert len(result.passed) == 1
        assert result.passed[0]["risk_flags"] == []


# ═══════════════════════════════════════════════════════════════════════════════
# gap_pct computation correctness
# ═══════════════════════════════════════════════════════════════════════════════


class TestGapPctComputation:
    """Verify _compute_gap_pct produces correct values."""

    def test_gap_pct_computed_correctly(self) -> None:
        """open=110.0, close[1]=100.0 → gap_pct=10.0."""
        ticker = {"open": 110.0, "close[1]": 100.0}
        assert _compute_gap_pct(ticker) == 10.0

    def test_gap_pct_none_prev_close(self) -> None:
        """close[1]=None → gap_pct=None."""
        ticker = {"open": 110.0, "close[1]": None}
        assert _compute_gap_pct(ticker) is None

    def test_gap_pct_zero_prev_close(self) -> None:
        """close[1]=0.0 → gap_pct=None (division by zero guard)."""
        ticker = {"open": 110.0, "close[1]": 0.0}
        assert _compute_gap_pct(ticker) is None

    def test_gap_pct_none_open(self) -> None:
        """open=None → gap_pct=None."""
        ticker = {"open": None, "close[1]": 100.0}
        assert _compute_gap_pct(ticker) is None


# ═══════════════════════════════════════════════════════════════════════════════
# FilterResult structure
# ═══════════════════════════════════════════════════════════════════════════════


class TestFilterResultStructure:
    """Verify FilterResult summary and split correctness."""

    def test_summary_counts(self) -> None:
        """Multiple tickers with different outcomes produce correct summary."""
        # 1 hard eliminated (climax top), 1 soft flagged (wide range), 1 clean pass
        tickers = [
            _make_ticker({
                "ticker": "A",
                "high": 120.0, "low": 112.0, "ATR": 2.0,
                "close": 118.0, "price_52_week_high": 115.0, "Perf.3M": 60.0,
            }),
            _make_ticker({
                "ticker": "B",
                "high": 116.5, "low": 112.0, "ATR": 2.0,
                "close": 114.0, "price_52_week_high": 115.0, "Perf.3M": 45.0,
            }),
            _make_ticker({
                "ticker": "C",
                "high": 112.0, "low": 110.0, "ATR": 2.0,
                "close": 110.0, "price_52_week_high": 115.0, "Perf.3M": 25.0,
            }),
        ]
        result = apply_stage1_filter(tickers, "momentum", TEST_DATE)
        assert result.summary["evaluated"] == 3
        assert result.summary["hard_eliminated"] == 1
        assert result.summary["soft_flagged"] == 1
        assert result.summary["passed"] == 1
        assert result.summary["by_rule"]["climax_top"] == 1
        assert result.summary["by_flag"]["WIDE_RANGE_WARNING"] == 1

    def test_hard_eliminated_not_in_passed_or_flagged(self) -> None:
        """Hard-eliminated tickers never appear in passed or flagged."""
        ticker = _make_ticker({
            "high": 120.0, "low": 112.0, "ATR": 2.0,
            "close": 118.0, "price_52_week_high": 115.0, "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.eliminated) == 1
        assert len(result.passed) == 0
        assert len(result.flagged) == 0

    def test_flagged_tickers_have_risk_flags(self) -> None:
        """Flagged tickers appear in result.flagged with risk_flags populated."""
        ticker = _make_ticker({
            "high": 116.5, "low": 112.0, "ATR": 2.0,
            "close": 114.0, "price_52_week_high": 115.0, "Perf.3M": 45.0,
        })
        result = apply_stage1_filter([ticker], "momentum", TEST_DATE)
        assert len(result.flagged) == 1
        assert "risk_flags" in result.flagged[0]
        assert len(result.flagged[0]["risk_flags"]) > 0

    def test_empty_input(self) -> None:
        """Empty ticker list returns empty FilterResult."""
        result = apply_stage1_filter([], "momentum", TEST_DATE)
        assert result.summary["evaluated"] == 0
        assert len(result.passed) == 0
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 0


# ═══════════════════════════════════════════════════════════════════════════════
# Audit writer
# ═══════════════════════════════════════════════════════════════════════════════


class TestAuditWriter:
    """Verify audit_writer behaviour with mocked HTTP."""

    def test_write_audit_empty_list(self) -> None:
        """Empty elimination list should not make any HTTP call."""
        from app.screeners.audit_writer import write_audit

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client:
            write_audit([], "http://localhost:8080")
            mock_client.assert_not_called()

    def test_write_audit_successful_post(self) -> None:
        """Successful POST should log success and not write fallback."""
        from app.screeners.audit_writer import write_audit

        elim = EliminationRecord(
            ticker="TEST",
            screener="momentum",
            rule="climax_top",
            tier="hard_eliminate",
            metrics={"today_range": 8.0, "atr": 2.0, "range_mult": 4.0, "close": 118.0, "price_52w_high": 115.0, "perf_3m": 60.0},
            reason="test",
            date=TEST_DATE,
        )

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client:
            mock_instance = mock_client.return_value.__enter__.return_value
            mock_instance.post.return_value.is_success = True
            mock_instance.post.return_value.status_code = 200

            with patch("app.screeners.audit_writer._write_fallback") as mock_fallback:
                write_audit([elim], "http://localhost:8080")
                mock_instance.post.assert_called_once()
                mock_fallback.assert_not_called()

    def test_write_audit_fallback_on_failure(self) -> None:
        """Failed POST should trigger fallback write."""
        from app.screeners.audit_writer import write_audit

        elim = EliminationRecord(
            ticker="TEST",
            screener="momentum",
            rule="climax_top",
            tier="hard_eliminate",
            metrics={"today_range": 8.0, "atr": 2.0, "range_mult": 4.0, "close": 118.0, "price_52w_high": 115.0, "perf_3m": 60.0},
            reason="test",
            date=TEST_DATE,
        )

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client:
            mock_instance = mock_client.return_value.__enter__.return_value
            mock_instance.post.return_value.is_success = False
            mock_instance.post.return_value.status_code = 500

            with patch("app.screeners.audit_writer._write_fallback") as mock_fallback:
                write_audit([elim], "http://localhost:8080")
                mock_instance.post.assert_called_once()
                mock_fallback.assert_called_once()
