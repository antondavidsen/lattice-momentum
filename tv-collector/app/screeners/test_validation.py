"""
app/screeners/test_validation.py
─────────────────────────────────
Unit tests for the validation pipeline (Story 1).

Tests cover:
- Valid rows with known fundamentals → passes
- Misaligned columns (header order changed, shifted values) → rejected
- Missing fields → rejected or filled with fallback
- Out-of-range values (e.g. gross_margin=150%) → flagged
- Schema version transitions (v1→v2) → handled gracefully
- Retry exhaustion → fallback to cache
- Alerting thresholds trigger correctly
"""

from __future__ import annotations

from datetime import date, timedelta
from typing import Any

import pytest

from app.models.schemas import SchemaRegistry, ScreenerRowSchemaV1, ScreenerRowSchemaV2
from app.services.validation import (
    ValidationStats,
    check_alerting_thresholds,
    fallback_to_cache,
    retry_with_backoff,
    validate_batch,
    validate_headers,
    validate_row,
)


# ── Fixtures ───────────────────────────────────────────────────────────────────


@pytest.fixture
def registry() -> SchemaRegistry:
    return SchemaRegistry()


def _make_momentum_row(overrides: dict[str, Any] | None = None) -> dict[str, Any]:
    """Build a row with all momentum columns in the expected order."""
    row = {
        "ticker": "TEST",
        "name": "Test Corp",
        "open": 100.0,
        "close": 105.0,
        "high": 106.0,
        "low": 99.0,
        "volume": 1_000_000,
        "relative_volume_10d_calc": 1.2,
        "average_volume_10d_calc": 800_000.0,
        "market_cap_basic": 10_000_000_000.0,
        "RSI": 60.0,
        "sector": "Technology",
        "change": 5.0,
        "gap": 2.0,
        "price_52_week_high": 120.0,
        "price_52_week_low": 70.0,
        "SMA20": 98.0,
        "SMA50": 95.0,
        "SMA150": 85.0,
        "SMA200": 80.0,
        "SMA200[1]": 79.0,
        "Perf.W": 3.0,
        "Perf.1M": 8.0,
        "Perf.3M": 15.0,
        "Perf.6M": 25.0,
        "ATR": 3.5,
        "earnings_per_share_basic_ttm": 5.00,
        "earnings_per_share_diluted_yoy_growth_fq": 20.0,
        "total_revenue_ttm": 5_000_000_000.0,
        "total_revenue_yoy_growth_ttm": 15.0,
        "return_on_equity_fq": 25.0,
        "gross_margin": 55.0,
        "operating_margin": 28.0,
        "net_margin": 20.0,
        "earnings_release_next_date": "2026-07-15",
        "float_shares_outstanding": 500_000_000,
        "close[1]": 104.0,
    }
    if overrides:
        row.update(overrides)
    return row


@pytest.fixture
def valid_lrcx_row() -> dict[str, Any]:
    """LRCX (Lam Research) — known fundamentals, all fields present."""
    return _make_momentum_row({
        "ticker": "LRCX",
        "name": "Lam Research Corp",
        "close": 85.50,
        "volume": 2_500_000,
        "market_cap_basic": 110_000_000_000.0,
        "RSI": 62.0,
        "gross_margin": 50.2,
        "net_margin": 25.1,
        "operating_margin": 30.5,
        "return_on_equity_fq": 45.0,
        "earnings_per_share_basic_ttm": 8.50,
        "total_revenue_ttm": 18_000_000_000.0,
        "total_revenue_yoy_growth_ttm": 15.0,
        "earnings_per_share_diluted_yoy_growth_fq": 22.0,
        "relative_volume_10d_calc": 1.2,
        "average_volume_10d_calc": 2_000_000.0,
        "SMA50": 80.0,
        "price_52_week_high": 95.0,
        "price_52_week_low": 55.0,
        "Perf.3M": 12.0,
        "Perf.6M": 25.0,
        "change": 2.5,
        "gap": 0.5,
        "ATR": 3.0,
        "float_shares_outstanding": 1_300_000_000,
    })


@pytest.fixture
def valid_nvda_row() -> dict[str, Any]:
    """NVDA — high-growth fundamentals."""
    return _make_momentum_row({
        "ticker": "NVDA",
        "name": "NVIDIA Corp",
        "close": 950.00,
        "volume": 45_000_000,
        "market_cap_basic": 2_350_000_000_000.0,
        "RSI": 72.0,
        "gross_margin": 75.0,
        "net_margin": 55.0,
        "operating_margin": 62.0,
        "return_on_equity_fq": 80.0,
        "earnings_per_share_basic_ttm": 25.0,
        "total_revenue_ttm": 130_000_000_000.0,
        "total_revenue_yoy_growth_ttm": 120.0,
        "earnings_per_share_diluted_yoy_growth_fq": 150.0,
        "relative_volume_10d_calc": 1.5,
        "average_volume_10d_calc": 40_000_000.0,
        "SMA50": 880.0,
        "price_52_week_high": 1000.0,
        "price_52_week_low": 450.0,
        "Perf.3M": 30.0,
        "Perf.6M": 80.0,
        "change": 3.2,
        "gap": 1.0,
        "ATR": 25.0,
        "float_shares_outstanding": 2_500_000_000,
    })


@pytest.fixture
def valid_googl_row() -> dict[str, Any]:
    """GOOGL — stable mega-cap fundamentals."""
    return _make_momentum_row({
        "ticker": "GOOGL",
        "name": "Alphabet Inc",
        "close": 175.00,
        "volume": 20_000_000,
        "market_cap_basic": 2_100_000_000_000.0,
        "RSI": 55.0,
        "gross_margin": 58.0,
        "net_margin": 28.0,
        "operating_margin": 32.0,
        "return_on_equity_fq": 30.0,
        "earnings_per_share_basic_ttm": 7.50,
        "total_revenue_ttm": 350_000_000_000.0,
        "total_revenue_yoy_growth_ttm": 15.0,
        "earnings_per_share_diluted_yoy_growth_fq": 18.0,
        "relative_volume_10d_calc": 0.9,
        "average_volume_10d_calc": 22_000_000.0,
        "SMA50": 170.0,
        "price_52_week_high": 195.0,
        "price_52_week_low": 130.0,
        "Perf.3M": 8.0,
        "Perf.6M": 15.0,
        "change": 1.2,
        "gap": 0.3,
        "ATR": 4.0,
        "float_shares_outstanding": 12_500_000_000,
    })


# ── Test: Valid rows pass validation ───────────────────────────────────────────


class TestValidRows:
    """Known fundamentals for LRCX, NVDA, GOOGL should pass validation."""

    def test_lrcx_passes(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        is_valid, cleaned, errors = registry.validate_and_clean(valid_lrcx_row, target_version=1)
        assert is_valid, f"LRCX should pass validation: {errors}"
        assert cleaned["_schema_version"] == 1
        assert cleaned["gross_margin"] == 50.2

    def test_nvda_passes(self, registry: SchemaRegistry, valid_nvda_row: dict[str, Any]) -> None:
        is_valid, cleaned, errors = registry.validate_and_clean(valid_nvda_row, target_version=1)
        assert is_valid, f"NVDA should pass validation: {errors}"
        assert cleaned["_schema_version"] == 1
        assert cleaned["gross_margin"] == 75.0

    def test_googl_passes(self, registry: SchemaRegistry, valid_googl_row: dict[str, Any]) -> None:
        is_valid, cleaned, errors = registry.validate_and_clean(valid_googl_row, target_version=1)
        assert is_valid, f"GOOGL should pass validation: {errors}"
        assert cleaned["_schema_version"] == 1
        assert cleaned["gross_margin"] == 58.0

    def test_batch_valid_rows(
        self,
        registry: SchemaRegistry,
        valid_lrcx_row: dict[str, Any],
        valid_nvda_row: dict[str, Any],
        valid_googl_row: dict[str, Any],
    ) -> None:
        rows = [valid_lrcx_row, valid_nvda_row, valid_googl_row]
        valid, rejected, stats = validate_batch(rows, "momentum", registry=registry)
        assert len(valid) == 3
        assert len(rejected) == 0
        assert stats.valid_rows == 3
        assert stats.rejected_rows == 0


# ── Test: Misaligned columns ───────────────────────────────────────────────────


class TestMisalignedColumns:
    """Header order changed or shifted values → rejected."""

    def test_header_mismatch_rejected(self) -> None:
        """Columns in wrong order should be rejected."""
        rows = [
            {
                "close": 85.50,  # wrong position — should be after name/open
                "ticker": "LRCX",
                "name": "Lam Research Corp",
                "volume": 2_500_000,
            }
        ]
        valid, rejected, stats = validate_batch(rows, "momentum")
        assert len(valid) == 0
        assert len(rejected) == 1
        assert stats.header_mismatch is True

    def test_extra_columns_rejected(self) -> None:
        """Extra unexpected columns should trigger header mismatch."""
        rows = [
            {
                "ticker": "LRCX",
                "name": "Lam Research Corp",
                "close": 85.50,
                "volume": 2_500_000,
                "unexpected_column_xyz": "garbage",
            }
        ]
        valid, rejected, stats = validate_batch(rows, "momentum")
        assert len(valid) == 0
        assert stats.header_mismatch is True

    def test_missing_columns_rejected(self) -> None:
        """Missing critical columns should trigger header mismatch."""
        rows = [
            {
                "ticker": "LRCX",
                # missing: name, close, volume, etc.
            }
        ]
        valid, rejected, stats = validate_batch(rows, "momentum")
        assert len(valid) == 0
        assert stats.header_mismatch is True

    def test_empty_batch(self) -> None:
        """Empty batch should return empty results."""
        valid, rejected, stats = validate_batch([], "momentum")
        assert len(valid) == 0
        assert len(rejected) == 0
        assert stats.total_rows == 0


# ── Test: Missing fields ───────────────────────────────────────────────────────


class TestMissingFields:
    """Missing fields should be rejected or filled with fallback."""

    def test_missing_ticker_rejected(self, registry: SchemaRegistry) -> None:
        """Row without ticker should fail validation."""
        row = {
            "name": "Unknown Corp",
            "close": 50.0,
            "volume": 1_000_000,
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid

    def test_fallback_to_cache(self) -> None:
        """Fallback should tag row with cache metadata."""
        cached = {"gross_margin": 50.2, "close": 85.0}
        cache_date = date.today() - timedelta(days=1)
        result = fallback_to_cache("LRCX", cached, cache_date)
        assert result["_fallback_source"] == "cache"
        assert result["_fallback_age_days"] == 1
        assert result["gross_margin"] == 50.2

    def test_fallback_no_cache(self) -> None:
        """Fallback with no cached data should return tagged empty dict."""
        result = fallback_to_cache("UNKNOWN", None)
        assert result["_fallback_source"] == "cache"
        assert result["_fallback_age_days"] >= 1


# ── Test: Out-of-range values ──────────────────────────────────────────────────


class TestOutOfRangeValues:
    """Out-of-range values should be flagged, not silently cast."""

    def test_gross_margin_too_high(self, registry: SchemaRegistry) -> None:
        """gross_margin=150% should fail range validation."""
        row = {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": 50.0,
            "volume": 1_000_000,
            "gross_margin": 150.0,  # > 100 → invalid
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid
        assert any("gross_margin" in e for e in errors)

    def test_gross_margin_too_low(self, registry: SchemaRegistry) -> None:
        """gross_margin=-15000% should fail range validation."""
        row = {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": 50.0,
            "volume": 1_000_000,
            "gross_margin": -15000.0,  # < -10000 → invalid
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid
        assert any("gross_margin" in e for e in errors)

    def test_rsi_out_of_range(self, registry: SchemaRegistry) -> None:
        """RSI=150 should fail range validation."""
        row = {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": 50.0,
            "volume": 1_000_000,
            "RSI": 150.0,  # > 100 → invalid
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid
        assert any("RSI" in e for e in errors)

    def test_negative_close(self, registry: SchemaRegistry) -> None:
        """Negative close price should fail."""
        row = {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": -10.0,  # < 0 → invalid
            "volume": 1_000_000,
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid
        assert any("close" in e for e in errors)

    def test_negative_volume(self, registry: SchemaRegistry) -> None:
        """Negative volume should fail."""
        row = {
            "ticker": "TEST",
            "name": "Test Corp",
            "close": 50.0,
            "volume": -100,  # < 0 → invalid
        }
        is_valid, cleaned, errors = registry.validate_and_clean(row)
        assert not is_valid
        assert any("volume" in e for e in errors)


# ── Test: Schema version transitions ───────────────────────────────────────────


class TestSchemaVersionTransitions:
    """Schema version transitions (v1→v2) should be handled gracefully."""

    def test_detect_v1(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        """Row with only V1 fields should be detected as V1."""
        version = registry.detect_schema_version(valid_lrcx_row)
        assert version == 1

    def test_detect_v2(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        """Row with V2 signal columns should be detected as V2."""
        v2_row = dict(valid_lrcx_row)
        v2_row["book_value_per_share"] = 45.0
        v2_row["price_to_book"] = 3.5
        version = registry.detect_schema_version(v2_row)
        assert version == 2

    def test_v1_to_v2_migration(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        """V1 row migrated to V2 should have V2 fields defaulted to None."""
        migrated = registry.apply_migration(valid_lrcx_row, 1, 2)
        assert migrated["_schema_version"] == 2
        # V2-specific fields should be None
        assert migrated.get("book_value_per_share") is None
        assert migrated.get("price_to_book") is None

    def test_backward_migration_raises(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        """Backward migration (v2→v1) should raise ValueError."""
        with pytest.raises(ValueError, match="Cannot migrate backward"):
            registry.apply_migration(valid_lrcx_row, 2, 1)

    def test_same_version_noop(self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]) -> None:
        """Migration from v1→v1 should return row unchanged."""
        result = registry.apply_migration(valid_lrcx_row, 1, 1)
        assert result is valid_lrcx_row  # same object


# ── Test: Retry logic ──────────────────────────────────────────────────────────


class TestRetryLogic:
    """Retry with exponential backoff should handle transient failures."""

    def test_retry_success_on_second_attempt(self) -> None:
        """Function that fails once then succeeds should work."""
        call_count = [0]

        def flaky_fn() -> str:
            call_count[0] += 1
            if call_count[0] < 2:
                raise ConnectionError("transient failure")
            return "success"

        result = retry_with_backoff(flaky_fn, "TEST", initial_delay=0.01, max_delay=0.1, max_attempts=3)
        assert result == "success"
        assert call_count[0] == 2

    def test_retry_exhaustion_raises(self) -> None:
        """Function that always fails should raise after max attempts."""

        def always_fails() -> None:
            raise TimeoutError("always fails")

        with pytest.raises(TimeoutError):
            retry_with_backoff(always_fails, "TEST", initial_delay=0.01, max_delay=0.1, max_attempts=3)

    def test_retry_success_first_attempt(self) -> None:
        """Function that succeeds immediately should not retry."""

        def immediate_success() -> str:
            return "ok"

        result = retry_with_backoff(immediate_success, "TEST", initial_delay=0.01, max_delay=0.1, max_attempts=3)
        assert result == "ok"


# ── Test: Alerting thresholds ──────────────────────────────────────────────────


class TestAlertingThresholds:
    """Alerting rules should trigger at correct thresholds."""

    def test_high_conviction_failures_alert(self) -> None:
        """>3 rejected rows should trigger alert."""
        stats = ValidationStats(
            screener_type="market_leaders",
            total_rows=10,
            rejected_rows=5,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) > 0, f"Expected alerts, got empty list. stats={stats}"
        # Check for the alert message content (uses hyphen, not underscore)
        alert_text = alerts[0]
        assert "High-conviction" in alert_text, f"Expected 'High-conviction' in '{alert_text}'"

    def test_no_alert_below_threshold(self) -> None:
        """≤3 rejected rows should not trigger alert."""
        stats = ValidationStats(
            screener_type="market_leaders",
            total_rows=10,
            rejected_rows=2,
        )
        alerts = check_alerting_thresholds(stats)
        assert not any("High-conviction" in a for a in alerts)

    def test_header_mismatch_alert(self) -> None:
        """Header mismatch should trigger schema version change alert."""
        stats = ValidationStats(
            screener_type="momentum",
            total_rows=10,
            header_mismatch=True,
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) > 0
        assert any("Schema version change" in a for a in alerts)

    def test_fallback_threshold_alert(self) -> None:
        """>10% fallback should trigger alert."""
        stats = ValidationStats(
            screener_type="momentum",
            total_rows=100,
            fallback_rows=15,  # 15% > 10%
        )
        alerts = check_alerting_thresholds(stats)
        assert len(alerts) > 0
        assert any("Fallback usage exceeds" in a for a in alerts)

    def test_fallback_below_threshold_no_alert(self) -> None:
        """≤10% fallback should not trigger alert."""
        stats = ValidationStats(
            screener_type="momentum",
            total_rows=100,
            fallback_rows=8,  # 8% ≤ 10%
        )
        alerts = check_alerting_thresholds(stats)
        assert not any("Fallback usage exceeds" in a for a in alerts)


# ── Test: SchemaRegistry ───────────────────────────────────────────────────────


class TestSchemaRegistry:
    """SchemaRegistry version management."""

    def test_get_schema_v1(self, registry: SchemaRegistry) -> None:
        cls = registry.get_schema(1)
        assert cls == ScreenerRowSchemaV1

    def test_get_schema_v2(self, registry: SchemaRegistry) -> None:
        cls = registry.get_schema(2)
        assert cls == ScreenerRowSchemaV2

    def test_get_schema_unknown(self, registry: SchemaRegistry) -> None:
        with pytest.raises(ValueError, match="Unknown schema version"):
            registry.get_schema(99)

    def test_latest_version(self, registry: SchemaRegistry) -> None:
        assert registry.latest_version == 2

    def test_validate_and_clean_success(
        self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]
    ) -> None:
        is_valid, cleaned, errors = registry.validate_and_clean(valid_lrcx_row)
        assert is_valid
        assert cleaned["_schema_version"] == registry.latest_version
        assert cleaned["ticker"] == "LRCX"

    def test_validate_and_clean_with_target_version(
        self, registry: SchemaRegistry, valid_lrcx_row: dict[str, Any]
    ) -> None:
        is_valid, cleaned, errors = registry.validate_and_clean(
            valid_lrcx_row, target_version=1
        )
        assert is_valid
        assert cleaned["_schema_version"] == 1


# ── Test: validate_headers ─────────────────────────────────────────────────────


class TestValidateHeaders:
    """Column header alignment checks."""

    def test_momentum_headers_match(self) -> None:
        """Momentum columns in correct order should pass."""
        from app.services.validation import _SCREENER_COLUMNS

        cols = _SCREENER_COLUMNS["momentum"]
        assert validate_headers("momentum", cols) is True

    def test_momentum_headers_mismatch(self) -> None:
        """Momentum columns in wrong order should fail."""
        cols = ["close", "ticker", "name"]  # wrong order
        assert validate_headers("momentum", cols) is False

    def test_unknown_screener_passes(self) -> None:
        """Unknown screener type should pass through defensively."""
        assert validate_headers("unknown_screener", ["col1", "col2"]) is True
