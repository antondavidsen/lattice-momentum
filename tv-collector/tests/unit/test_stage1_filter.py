"""
tests/unit/test_stage1_filter.py
─────────────────────────────────
Unit tests for the Stage 1 filter (elimination rules in apply_stage1_filter).

Covers:
- EliminationRecord dataclass
- FilterResult dataclass
- apply_stage1_filter orchestration: rules A (climax_top), B (financial engineering),
  C (exhaustion risk)
- Edge cases: empty inputs, missing fields, none values, boundary conditions
"""

from __future__ import annotations

from typing import Any

import pytest

from app.screeners.stage1_filter import (
    EliminationRecord,
    FilterResult,
    apply_stage1_filter,
)


class TestEliminationRecord:
    """EliminationRecord dataclass validation."""

    def test_create_elimination_record(self, make_elimination_record: Any) -> None:
        """Creating an EliminationRecord with all fields sets attributes."""
        rec = make_elimination_record(
            ticker="AAPL",
            screener="momentum",
            rule="climax_top",
            tier="hard_eliminate",
            metrics={"today_range": 12.0, "atr": 2.0},
            reason="Range > 3x ATR",
            date="2026-06-01",
        )
        assert rec.ticker == "AAPL"
        assert rec.screener == "momentum"
        assert rec.rule == "climax_top"
        assert rec.tier == "hard_eliminate"
        assert rec.metrics == {"today_range": 12.0, "atr": 2.0}
        assert rec.reason == "Range > 3x ATR"
        assert rec.date == "2026-06-01"

    def test_elimination_record_defaults(self) -> None:
        """EliminationRecord with all required fields is constructed (no runtime defaults)."""
        rec = EliminationRecord(
            ticker="TSLA",
            screener="momentum",
            rule="volume_climax",
            tier="hard_eliminate",
            metrics={"volume": 5_000_000},
            reason="Volume > 4x avg",
            date="2026-06-01",
        )
        assert rec.tier == "hard_eliminate"
        assert rec.metrics == {"volume": 5_000_000}
        assert rec.date == "2026-06-01"


class TestFilterResult:
    """FilterResult dataclass validation."""

    def test_empty_filter_result_defaults(self) -> None:
        """FilterResult with no args has empty lists and empty summary."""
        result = FilterResult()
        assert result.passed == []
        assert result.eliminated == []
        assert result.flagged == []
        assert result.summary == {}

    def test_filter_result_with_data(self) -> None:
        """FilterResult with data preserves all values."""
        rec = EliminationRecord(
            ticker="AAPL", screener="momentum", rule="climax_top",
            tier="hard_eliminate", metrics={}, reason="test", date="2026-06-01",
        )
        result = FilterResult(
            passed=[{"ticker": "GOOG"}],
            eliminated=[rec],
            flagged=[{"ticker": "MSFT", "risk_flags": ["WIDE_RANGE_WARNING"]}],
            summary={"evaluated": 3, "hard_eliminated": 1},
        )
        assert len(result.passed) == 1
        assert len(result.eliminated) == 1
        assert len(result.flagged) == 1
        assert result.summary["evaluated"] == 3


class TestApplyStage1Filter:
    """Master filter: apply_stage1_filter orchestrates all elimination rules."""

    # ── Rule A — Climax Top (Momentum / Leaders) ──────────────────────────────

    def test_climax_top_eliminates(self, make_ticker: Any) -> None:
        """Range >= 3x ATR + close above 52W high + perf_3m > 50% → climax_top."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 3.0,
            "close": 118.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.eliminated) == 1
        assert result.eliminated[0].rule == "climax_top"
        assert result.eliminated[0].ticker == "TEST"

    def test_climax_top_skipped_when_close_below_52w_high(self, make_ticker: Any) -> None:
        """Close below 52W high should not trigger climax top."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 3.0,
            "close": 110.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.eliminated) == 0

    def test_climax_top_skipped_when_perf_3m_low(self, make_ticker: Any) -> None:
        """Perf.3M <= 50% should not trigger climax top."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 3.0,
            "close": 118.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 30.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.eliminated) == 0

    def test_climax_top_skipped_when_atr_zero(self, make_ticker: Any) -> None:
        """Zero ATR should not cause division by zero or crash."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 0.0,
            "close": 118.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.eliminated) == 0
        assert len(result.passed) == 1

    def test_climax_top_not_applied_for_ep(self, make_ticker: Any) -> None:
        """Rule A (climax top) only applies to momentum and leaders, not EP."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 3.0,
            "close": 118.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", "2026-06-01")
        assert len(result.eliminated) == 0

    # ── Soft flag: WIDE_RANGE_WARNING ─────────────────────────────────────────

    def test_wide_range_warning_flagged(self, make_ticker: Any) -> None:
        """Range >= 2x ATR + perf_3m > 40% → wide range warning (soft)."""
        ticker = make_ticker({
            "high": 110.0,
            "low": 104.0,
            "ATR": 2.5,
            "close": 108.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 50.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.flagged) == 1
        assert "WIDE_RANGE_WARNING" in result.flagged[0].get("risk_flags", [])

    def test_wide_range_warning_not_flagged_when_perf_low(self, make_ticker: Any) -> None:
        """Range >= 2x ATR but perf_3m <= 40% → no flag."""
        ticker = make_ticker({
            "high": 110.0,
            "low": 104.0,
            "ATR": 2.5,
            "close": 108.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 30.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.flagged) == 0

    # ── Rule B — Financial Engineering (Leaders only) ─────────────────────────

    def test_financial_engineering_flagged(self, make_ticker: Any) -> None:
        """EPS growth >> revenue growth + low revenue → financial engineering flag."""
        ticker = make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 50.0,
            "total_revenue_yoy_growth_ttm": 3.0,
        })
        result = apply_stage1_filter([ticker], "leaders", "2026-06-01")
        # Leaders trigger financial engineering check (Rule B)
        # After Rule A, if no climax top, continues to Rule B
        assert len(result.flagged) == 1
        assert "FINANCIAL_ENGINEERING_RISK" in result.flagged[0].get("risk_flags", [])
        assert len(result.passed) == 0  # flagged items don't go to passed

    def test_financial_engineering_not_flagged_when_revenue_healthy(self, make_ticker: Any) -> None:
        """EPS growth >> revenue growth but revenue >= 5% → no flag."""
        ticker = make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 50.0,
            "total_revenue_yoy_growth_ttm": 10.0,
        })
        result = apply_stage1_filter([ticker], "leaders", "2026-06-01")
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    def test_financial_engineering_not_flagged_for_momentum(self, make_ticker: Any) -> None:
        """Rule B only applies to leaders screener."""
        ticker = make_ticker({
            "earnings_per_share_diluted_yoy_growth_fq": 50.0,
            "total_revenue_yoy_growth_ttm": 3.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.flagged) == 0
        assert len(result.passed) == 1

    # ── Rule C — Exhaustion Risk (EP only) ────────────────────────────────────

    def test_exhaustion_risk_flagged(self, make_ticker: Any) -> None:
        """Gap > 30% + perf_1m > 50% → exhaustion risk flag (EP)."""
        ticker = make_ticker({
            "close[1]": 70.0,
            "open": 95.0,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", "2026-06-01")
        assert len(result.flagged) == 1
        assert "EXHAUSTION_RISK" in result.flagged[0].get("risk_flags", [])

    def test_exhaustion_risk_not_flagged_when_gap_small(self, make_ticker: Any) -> None:
        """Gap <= 30% should not trigger exhaustion risk."""
        ticker = make_ticker({
            "close[1]": 95.0,
            "open": 100.0,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "ep", "2026-06-01")
        assert len(result.flagged) == 0

    def test_exhaustion_risk_not_flagged_when_perf_low(self, make_ticker: Any) -> None:
        """Perf.1M <= 50% should not trigger exhaustion risk."""
        ticker = make_ticker({
            "close[1]": 70.0,
            "open": 95.0,
            "Perf.1M": 30.0,
        })
        result = apply_stage1_filter([ticker], "ep", "2026-06-01")
        assert len(result.flagged) == 0

    def test_exhaustion_risk_not_flagged_for_momentum(self, make_ticker: Any) -> None:
        """Rule C only applies to EP screener."""
        ticker = make_ticker({
            "close[1]": 70.0,
            "open": 95.0,
            "Perf.1M": 60.0,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.flagged) == 0

    # ── Priority: first hard elimination wins ────────────────────────────────

    def test_multiple_triggers_first_elimination_wins(self, make_ticker: Any) -> None:
        """When a ticker triggers climax top, it's eliminated and not checked for other rules."""
        ticker = make_ticker({
            "high": 120.0,
            "low": 108.0,
            "ATR": 3.0,
            "close": 118.0,
            "price_52_week_high": 115.0,
            "Perf.3M": 60.0,
        })
        result = apply_stage1_filter([ticker], "leaders", "2026-06-01")
        assert len(result.eliminated) == 1
        assert len(result.passed) == 0

    # ── Empty input ──────────────────────────────────────────────────────────

    def test_empty_tickers_returns_empty_result(self) -> None:
        """Empty ticker list returns FilterResult with zero counts."""
        result = apply_stage1_filter([], "momentum", "2026-06-01")
        assert len(result.passed) == 0
        assert len(result.eliminated) == 0
        assert len(result.flagged) == 0
        assert result.summary["evaluated"] == 0

    # ── Missing fields ──────────────────────────────────────────────────────

    def test_ticker_with_missing_fields_passes_through(self, make_ticker: Any) -> None:
        """Ticker missing optional fields should not crash and passes through."""
        ticker = make_ticker({
            "high": None,
            "ATR": None,
            "price_52_week_high": None,
        })
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.passed) == 1

    def test_ticker_with_ticker_field_missing(self) -> None:
        """Ticker with neither 'ticker' nor 'name' field uses 'unknown'."""
        ticker = {"close": 100.0}
        result = apply_stage1_filter([ticker], "momentum", "2026-06-01")
        assert len(result.passed) == 1

    # ── Summary ──────────────────────────────────────────────────────────────

    def test_summary_counts(self, make_ticker: Any) -> None:
        """Summary correctly counts evaluated, eliminated, flagged, passed."""
        ticker1 = make_ticker({
            "high": 120.0, "low": 108.0, "ATR": 3.0, "close": 118.0,
            "price_52_week_high": 115.0, "Perf.3M": 60.0, "ticker": "ELIM",
        })
        ticker2 = make_ticker({
            "high": 110.0, "low": 104.0, "ATR": 2.5, "close": 108.0,
            "price_52_week_high": 115.0, "Perf.3M": 50.0, "ticker": "FLAG",
        })
        ticker3 = make_ticker({"ticker": "PASS"})
        result = apply_stage1_filter([ticker1, ticker2, ticker3], "momentum", "2026-06-01")
        assert result.summary["evaluated"] == 3
        assert result.summary["hard_eliminated"] == 1
        assert result.summary["soft_flagged"] == 1
        assert result.summary["passed"] == 1
        assert "climax_top" in result.summary["by_rule"]
        assert "WIDE_RANGE_WARNING" in result.summary["by_flag"]