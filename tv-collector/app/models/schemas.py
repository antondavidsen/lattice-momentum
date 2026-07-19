"""
app/models/schemas.py
─────────────────────
Versioned Pydantic models for TradingView screener row validation.

Each screener type (momentum, episodic_pivot, market_leaders) produces rows
that must conform to a known schema.  The SchemaRegistry manages version
mappings, detection, and forward-compatible migrations.

Usage
─────
    registry = SchemaRegistry()
    schema_cls = registry.get_schema(version=1)
    row = schema_cls(**raw_dict)          # validates + coerces
    cleaned = row.model_dump(by_alias=True)
"""

from __future__ import annotations

from typing import Any, ClassVar, Optional

import structlog
from pydantic import BaseModel, Field, field_validator

log = structlog.get_logger(__name__)

# ── Shared field definitions ───────────────────────────────────────────────────

# All numeric fields that appear across screeners, with their valid ranges.
# Used by validators to flag out-of-bounds values.
_FIELD_RANGES: dict[str, tuple[float | None, float | None]] = {
    "close": (0.0, None),               # price must be positive
    "volume": (0, None),                 # volume must be positive
    "market_cap_basic": (0.0, None),     # market cap must be positive
    "RSI": (0.0, 100.0),                 # RSI is 0-100
    "change": (None, None),              # no hard bound
    "gap": (None, None),                 # no hard bound
    "gross_margin": (-100.0, 100.0),     # percentage: bounded by accounting definition (max loss = cost = 2× revenue)
    "net_margin": (-10000.0, 150.0),     # percentage: can exceed 100% from one-time items (tax benefits, extraordinary gains)
    "operating_margin": (-50000.0, 100.0), # percentage: biotechs/R&D-heavy cos have legitimate extreme negative margins
    "return_on_equity_fq": (-10000.0, 10000.0),  # percentage (can be extreme for high-ROE or distressed cos)
    "earnings_per_share_basic_ttm": (None, None),
    "earnings_per_share_diluted_yoy_growth_fq": (None, None),
    "total_revenue_ttm": (0.0, None),    # revenue must be non-negative
    "total_revenue_yoy_growth_ttm": (None, None),
    "Perf.W": (None, None),
    "Perf.1M": (None, None),
    "Perf.3M": (None, None),
    "Perf.6M": (None, None),
    "relative_volume_10d_calc": (0.0, None),
    "average_volume_10d_calc": (0.0, None),
    "SMA20": (0.0, None),
    "SMA50": (0.0, None),
    "SMA150": (0.0, None),
    "SMA200": (0.0, None),
    "ATR": (0.0, None),
    "price_52_week_high": (0.0, None),
    "price_52_week_low": (0.0, None),
    "float_shares_outstanding": (0, None),
}


def _check_range(value: Any, field_name: str) -> Any:
    """Validate a numeric value is within the defined range for *field_name*."""
    if value is None:
        return value
    lo, hi = _FIELD_RANGES.get(field_name, (None, None))
    if lo is not None and value < lo:
        raise ValueError(f"{field_name}={value} is below minimum {lo}")
    if hi is not None and value > hi:
        raise ValueError(f"{field_name}={value} is above maximum {hi}")
    return value


# ── Schema V1 ──────────────────────────────────────────────────────────────────


class ScreenerRowSchemaV1(BaseModel):
    """
    Base schema for all screener rows (version 1).

    All fields that appear across the three screeners are declared here as
    Optional — a given screener may not populate every field.  The Go backend
    handles missing fields gracefully.

    Range validators flag out-of-bounds values (e.g. gross_margin=150%).
    """

    # ── Identifiers ───────────────────────────────────────────────────────────
    ticker: str = Field(..., alias="ticker", description="Stock ticker symbol")
    name: Optional[str] = Field(None, alias="name")
    sector: Optional[str] = Field(None, alias="sector")

    # ── Price / volume ────────────────────────────────────────────────────────
    open: Optional[float] = Field(None, alias="open")
    high: Optional[float] = Field(None, alias="high")
    low: Optional[float] = Field(None, alias="low")
    close: Optional[float] = Field(None, alias="close")
    volume: Optional[int] = Field(None, alias="volume")
    change: Optional[float] = Field(None, alias="change")
    gap: Optional[float] = Field(None, alias="gap")
    market_cap_basic: Optional[float] = Field(None, alias="market_cap_basic")
    RSI: Optional[float] = Field(None, alias="RSI")

    # ── Moving averages ───────────────────────────────────────────────────────
    SMA20: Optional[float] = Field(None, alias="SMA20")
    SMA50: Optional[float] = Field(None, alias="SMA50")
    SMA150: Optional[float] = Field(None, alias="SMA150")
    SMA200: Optional[float] = Field(None, alias="SMA200")
    SMA200_prev: Optional[float] = Field(None, alias="SMA200[1]")

    # ── Volume / liquidity ────────────────────────────────────────────────────
    relative_volume_10d_calc: Optional[float] = Field(
        None, alias="relative_volume_10d_calc"
    )
    average_volume_10d_calc: Optional[float] = Field(
        None, alias="average_volume_10d_calc"
    )

    # ── Performance ───────────────────────────────────────────────────────────
    Perf_W: Optional[float] = Field(None, alias="Perf.W")
    Perf_1M: Optional[float] = Field(None, alias="Perf.1M")
    Perf_3M: Optional[float] = Field(None, alias="Perf.3M")
    Perf_6M: Optional[float] = Field(None, alias="Perf.6M")

    # ── Volatility ────────────────────────────────────────────────────────────
    ATR: Optional[float] = Field(None, alias="ATR")

    # ── 52-week range ─────────────────────────────────────────────────────────
    price_52_week_high: Optional[float] = Field(None, alias="price_52_week_high")
    price_52_week_low: Optional[float] = Field(None, alias="price_52_week_low")

    # ── Fundamentals ──────────────────────────────────────────────────────────
    earnings_per_share_basic_ttm: Optional[float] = Field(
        None, alias="earnings_per_share_basic_ttm"
    )
    earnings_per_share_diluted_ttm: Optional[float] = Field(
        None, alias="earnings_per_share_diluted_ttm"
    )
    earnings_per_share_diluted_yoy_growth_fq: Optional[float] = Field(
        None, alias="earnings_per_share_diluted_yoy_growth_fq"
    )
    total_revenue_ttm: Optional[float] = Field(None, alias="total_revenue_ttm")
    total_revenue_yoy_growth_ttm: Optional[float] = Field(
        None, alias="total_revenue_yoy_growth_ttm"
    )
    return_on_equity_fq: Optional[float] = Field(None, alias="return_on_equity_fq")
    gross_margin: Optional[float] = Field(None, alias="gross_margin")
    net_margin: Optional[float] = Field(None, alias="net_margin")
    operating_margin: Optional[float] = Field(None, alias="operating_margin")
    float_shares_outstanding: Optional[int] = Field(
        None, alias="float_shares_outstanding"
    )

    # ── Events ────────────────────────────────────────────────────────────────
    earnings_release_next_date: Optional[str] = Field(
        None, alias="earnings_release_next_date"
    )

    @field_validator("earnings_release_next_date", mode="before")
    @classmethod
    def _coerce_earnings_date(cls, value: Any) -> Any:
        """Convert Unix timestamp (int) to date string, pass through strings."""
        import math
        if value is None:
            return None
        if isinstance(value, str):
            return value
        if isinstance(value, (int, float)):
            if math.isnan(value):
                return None
            from datetime import datetime
            return datetime.utcfromtimestamp(value).strftime("%Y-%m-%d")
        return str(value)

    # ── Schema metadata ───────────────────────────────────────────────────────
    schema_version: int = Field(default=1, alias="_schema_version")

    # ── Range validators ──────────────────────────────────────────────────────

    @field_validator(
        "close",
        "market_cap_basic",
        "RSI",
        "gross_margin",
        "net_margin",
        "operating_margin",
        "return_on_equity_fq",
        "total_revenue_ttm",
        "relative_volume_10d_calc",
        "average_volume_10d_calc",
        "SMA20",
        "SMA50",
        "SMA150",
        "SMA200",
        "ATR",
        "price_52_week_high",
        "price_52_week_low",
        mode="before",
    )
    @classmethod
    def _check_numeric_range(cls, value: Any, info: Any) -> Any:
        if value is None:
            return None
        try:
            val = float(value)
        except (TypeError, ValueError):
            raise ValueError(
                f"field {info.field_name} has value {value!r} (expected float, schema_v1)"
            )
        return _check_range(val, info.field_name)

    @field_validator("volume", "float_shares_outstanding", mode="before")
    @classmethod
    def _check_int_range(cls, value: Any, info: Any) -> Any:
        if value is None:
            return None
        try:
            val = int(value)
        except (TypeError, ValueError):
            raise ValueError(
                f"field {info.field_name} has value {value!r} (expected int, schema_v1)"
            )
        return _check_range(val, info.field_name)

    model_config = {"populate_by_name": True, "extra": "allow"}


# ── Schema V2 (future placeholder) ─────────────────────────────────────────────


class ScreenerRowSchemaV2(ScreenerRowSchemaV1):
    """
    Schema V2 — forward-compatible extension of V1.

    When TradingView adds new metrics, add them here as Optional fields
    with sensible defaults.  The migration layer handles V1→V2 transitions.
    """

    schema_version: int = Field(default=2, alias="_schema_version")

    # Example future fields (commented out until needed):
    # book_value_per_share: Optional[float] = Field(None, alias="book_value_per_share")
    # price_to_book: Optional[float] = Field(None, alias="price_to_book")


# ── Schema Registry ────────────────────────────────────────────────────────────


class SchemaRegistry:
    """
    Manages versioned schemas and forward-compatible migrations.

    Responsibilities:
    - Map version numbers to Pydantic model classes.
    - Detect the schema version of a raw row by column presence heuristics.
    - Migrate rows between versions (forward only).
    - Log schema version on each ingestion.
    """

    _VERSIONS: dict[int, type[BaseModel]] = {
        1: ScreenerRowSchemaV1,
        2: ScreenerRowSchemaV2,
    }

    # Columns that uniquely identify each schema version.
    # V1 is the baseline; V2+ adds columns not present in V1.
    _VERSION_SIGNALS: dict[int, set[str]] = {
        1: set(),  # baseline — no extra signals needed
        2: {"book_value_per_share", "price_to_book"},  # future
    }

    def __init__(self) -> None:
        self._log = structlog.get_logger(__name__)

    # ── Public API ─────────────────────────────────────────────────────────────

    def get_schema(self, version: int) -> type[BaseModel]:
        """Return the Pydantic model class for *version*."""
        cls = self._VERSIONS.get(version)
        if cls is None:
            raise ValueError(f"Unknown schema version {version}")
        return cls

    @property
    def latest_version(self) -> int:
        return max(self._VERSIONS)

    def detect_schema_version(self, row: dict[str, Any]) -> int:
        """
        Heuristically detect which schema version *row* conforms to.

        Iterates versions from highest to lowest, checking for version-specific
        signal columns.  Falls back to version 1 (baseline).
        """
        detected = 1
        for version, signals in sorted(self._VERSION_SIGNALS.items(), reverse=True):
            if signals and signals.issubset(row.keys()):
                detected = version
                break
        return detected

    def apply_migration(
        self,
        row: dict[str, Any],
        from_version: int,
        to_version: int,
    ) -> dict[str, Any]:
        """
        Migrate a raw row dict from *from_version* to *to_version*.

        Currently handles V1→V2 by adding None defaults for new fields.
        Raises ValueError for backward migrations (to_version < from_version).
        """
        if to_version < from_version:
            raise ValueError(
                f"Cannot migrate backward from v{from_version} to v{to_version}"
            )
        if to_version == from_version:
            return row

        migrated = dict(row)
        for v in range(from_version + 1, to_version + 1):
            schema_cls = self.get_schema(v)
            # Get all field names defined in this version
            schema_fields = set(schema_cls.model_fields.keys())
            # Add None for any field not present in the row
            for field_name in schema_fields:
                # Skip internal fields and aliases
                if field_name.startswith("_") or field_name == "schema_version":
                    continue
                alias = schema_cls.model_fields[field_name].alias or field_name
                if alias not in migrated:
                    migrated[alias] = None

        migrated["_schema_version"] = to_version
        return migrated

    def validate_and_clean(
        self,
        row: dict[str, Any],
        *,
        target_version: int | None = None,
    ) -> tuple[bool, dict[str, Any], list[str]]:
        """
        Validate a raw row dict against the schema.

        Returns (is_valid, cleaned_row, error_messages).

        - Detects schema version automatically.
        - Migrates to *target_version* (or latest) if needed.
        - Validates all fields via Pydantic.
        - Collects all validation errors (does not fail fast).
        """
        errors: list[str] = []

        # 1. Detect version
        detected_version = self.detect_schema_version(row)
        target = target_version or self.latest_version

        # 2. Migrate if needed
        if detected_version != target:
            try:
                row = self.apply_migration(row, detected_version, target)
            except ValueError as exc:
                errors.append(str(exc))
                return False, row, errors

        # 3. Validate against target schema
        schema_cls = self.get_schema(target)
        try:
            validated = schema_cls(**row)
            cleaned = validated.model_dump(by_alias=True, exclude_none=False)
            cleaned["_schema_version"] = target
            return True, cleaned, []
        except Exception as exc:
            errors.append(str(exc))
            return False, row, errors
