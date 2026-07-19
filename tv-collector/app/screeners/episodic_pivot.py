"""
app/screeners/episodic_pivot.py
────────────────────────────────
Episodic Pivot (EP) screener — finds stocks with unusual single-day catalysts.

An Episodic Pivot is a stock that gaps up (or surges intraday) on dramatically
elevated volume, typically driven by an earnings surprise, FDA approval,
contract win, or macro catalyst.  These setups can produce 20–50%+ moves over
weeks when they occur in a bull market.

Production filter spec
──────────────────────
Universe  (server-side)
  • Exchange ∈ {NASDAQ, NYSE, AMEX}  (major US exchanges only)
  • Market cap ≥ $100 M
  • Price ≥ $5                       (avoid sub-penny / illiquid names)
  • Float shares ≥ 5 M              (avoid thin-float mechanical gaps)

Event  (server-side)
  • Gap ≥ 3 %                       (significant opening dislocation)
  • Daily change ≥ 5 %              (price follow-through, not a gap-and-fade)
  • Close > Open                    (bullish candle body)

Volume  (server-side + post-filter)
  • Relative volume ≥ 3×            (institutional participation)
  • Dollar volume ≥ $5 M            (post-filter: sufficient liquidity)

Price action  (post-filter)
  • Close in top 30 % of daily range (strong intraday close)

Quality  (soft post-filter)
  • Revenue growth YoY ≥ 10 %       (only applied when data present — avoids
                                     dead-cat bounces on declining-revenue names
                                     while preserving pre-revenue biotech/growth)

Sort
  • Relative volume DESC

Notes
─────
Quality fundamentals (EPS, revenue, gross margin) are intentionally **not**
used as hard filters.  Many of the best EP setups are growth companies that
are not yet profitable (biotech FDA approvals, earnings-surprise turnarounds,
SPAC catalysts).  Fundamental columns are still selected so downstream
consumers (rank-list jobs, LLM prompts) can assess quality on a case-by-case
basis.
"""

from __future__ import annotations

from datetime import date

import pandas as pd
import structlog
from tradingview_screener import col

from app.clients.tradingview_client import TradingViewClient
from app.services.validation import validate_batch

log = structlog.get_logger(__name__)

SCREENER_NAME = "episodic_pivot"
RESULT_LIMIT = 200

COLUMNS = [
    "name",
    "open",
    "high",
    "low",
    "close",
    "volume",
    "relative_volume_10d_calc",   # how unusual today's volume is vs 10-day avg
    "market_cap_basic",
    "RSI",
    "sector",
    # EP event context
    "gap",                        # opening gap % vs prior close
    "change",                     # intraday % change
    "average_volume_10d_calc",    # baseline volume for context
    "earnings_release_next_date", # is this an earnings-driven EP?
    "float_shares_outstanding",   # share-structure check for thin-float filter
    # Quality fundamentals (verified field names — selected for downstream use,
    # not used as hard filters except revenue growth as a soft gate).
    "total_revenue_ttm",              # revenue trailing twelve months ($)
    "total_revenue_yoy_growth_ttm",   # revenue growth YoY TTM (%)
    "earnings_per_share_basic_ttm",   # EPS TTM
    "earnings_per_share_diluted_yoy_growth_fq",  # EPS growth YoY (last quarter)
    "gross_margin",                   # gross margin %
    "operating_margin",               # operating margin %
    "net_margin",                     # net profit margin %
    "return_on_equity_fq",            # ROE (most recent quarter)
    # Performance / price location — needed by LLM prompt renderer.
    "Perf.W",                         # 1-week price performance (short-term RS)
    "Perf.1M",                        # 1-month price performance
    "Perf.3M",                        # 3-month price performance
    "Perf.6M",                        # 6-month price performance
    "price_52_week_high",             # 52-week high price
    "price_52_week_low",              # 52-week low price
    # Prior close — needed for gap_pct computation in stage 1 filter.
    "close[1]",                       # prior session close
    # Moving averages & volatility — used by the LLM prompt for trend alignment
    # (SMA stack: SMA20 > SMA50 > SMA150 > SMA200) and extension analysis
    # (Ext% = (price - SMA50) / SMA50 × 100, computed downstream in Go).
    "SMA20",
    "SMA50",
    "SMA150",
    "SMA200",
    "ATR",                            # 14-day Average True Range — used for ADR%
                                       # (ATR / price × 100, computed downstream in Go).
]

# Dollar-volume threshold applied as a post-filter (close × volume).
_MIN_DOLLAR_VOLUME: float = 5_000_000.0   # $5 M

# Minimum position of close within the daily range.
# 0.70 means close must be in the UPPER 30% of the day's range.
# Formula: (close − low) / (high − low) ≥ 0.70
# EP prompt Step 1 eliminates at < 0.30 (bottom 30% = weak close).
# We apply 0.70 here as a pre-filter — tighter threshold to save LLM tokens.
_MIN_CLOSE_RANGE_POSITION: float = 0.70


def run(client: TradingViewClient) -> pd.DataFrame:
    """
    Execute the Episodic Pivot screener and return results as a DataFrame.

    Two-stage filtering:
      1. TradingView API-level ``where()`` filters knock out the bulk of the
         universe using server-side evaluation (exchange, gap, change, volume).
      2. Pandas post-filters apply the derived conditions (close in top 30 % of
         range, dollar volume ≥ $5 M) that require column arithmetic.

    :param client: Authenticated TradingViewClient instance.
    :return: DataFrame with up to RESULT_LIMIT rows, one per ticker.
    """
    log.info("screener.start", screener=SCREENER_NAME, limit=RESULT_LIMIT)

    # ── Stage 1: server-side filters ─────────────────────────────────────────
    query = (
        client.get_query()
        .select(*COLUMNS)
        .set_markets("america")
        .where(
            # Universe — restrict to major exchanges so OTC penny stocks don't
            # fill the 200-row API limit with spurious relative-volume spikes.
            col("exchange").isin(["NASDAQ", "NYSE", "AMEX"]),
            col("market_cap_basic") >= 100_000_000,         # ≥ $100 M market cap
            col("close") >= 5,                              # ≥ $5 price
            col("float_shares_outstanding") >= 5_000_000,   # ≥ 5 M float shares
            # Event
            col("gap") >= 3,                                # opened ≥ 3% vs prior close
            col("change") >= 5,                             # held / extended the gap
            col("close") > col("open"),                     # bullish candle body
            # Volume
            col("relative_volume_10d_calc") >= 3,           # 3× normal volume
        )
        .order_by("relative_volume_10d_calc", ascending=False)
        .limit(RESULT_LIMIT)
    )

    try:
        df = client.execute_query(query)
    except Exception:
        return pd.DataFrame()

    # Early-return guard: if df is empty or missing required columns, return empty
    required = {"ticker", "close", "volume", "high", "low"}
    if df.empty or not required.issubset(df.columns):
        return pd.DataFrame()

    pre_filter_rows = len(df)

    # ── Stage 2: pandas post-filters ─────────────────────────────────────────
    # Exchange filtering is handled server-side via col("exchange").isin(…),
    # so only derived conditions that need column arithmetic remain here.

    # Dollar volume ≥ $5 M  (close × volume)
    df = df[df["close"] * df["volume"] >= _MIN_DOLLAR_VOLUME]

    # Close in upper 30% of daily range: (close − low) / (high − low) ≥ 0.70
    # Guard against zero-range candles (halt / no movement) — not valid EP setups.
    daily_range = df["high"] - df["low"]
    df = df[
        (daily_range > 0)
        & ((df["close"] - df["low"]) / daily_range >= _MIN_CLOSE_RANGE_POSITION)
    ]

    # Revenue growth soft gate: drop names where revenue growth data exists
    # AND YoY growth < 10 %.  Names with NaN revenue growth (pre-revenue
    # biotech, SPACs, etc.) are kept — those are valid EP candidates.
    rev_growth = df["total_revenue_yoy_growth_ttm"]
    df = df[rev_growth.isna() | (rev_growth >= 10)]

    # ── Stage 3: schema validation ────────────────────────────────────────────
    # Extract raw rows, validate against the episodic_pivot schema, and return
    # only valid rows.  Rejects batches with header mismatches.
    df = df.reset_index(drop=True)
    rows = df.to_dict(orient="records")
    valid_rows, rejected_rows, stats = validate_batch(rows, SCREENER_NAME)

    if stats.header_mismatch:
        log.error(
            "screener.header_mismatch",
            screener=SCREENER_NAME,
            reason="all rows rejected — TradingView output format may have changed",
        )
        return pd.DataFrame()

    if rejected_rows:
        log.warning(
            "screener.validation_summary",
            screener=SCREENER_NAME,
            total=stats.total_rows,
            valid=stats.valid_rows,
            rejected=stats.rejected_rows,
            schema_version=stats.schema_version,
        )

    log.info(
        "screener.complete",
        screener=SCREENER_NAME,
        rows_after_api=pre_filter_rows,
        rows_after_postfilter=len(df),
        rows_eliminated_postfilter=pre_filter_rows - len(df),
        rows_validated=len(valid_rows),
        rows_rejected=len(rejected_rows),
    )

    # ── Stage 4: stage 1 hard elimination filter ──────────────────────────────
    from .stage1_filter import apply_stage1_filter
    from .audit_writer import write_audit
    from ..config import load_config

    today_str = date.today().isoformat()
    result = apply_stage1_filter(
        tickers=valid_rows,
        screener="ep",
        run_date=today_str,
    )
    write_audit(result.eliminated, load_config().backend_api_url)
    log.info(
        "stage1_summary",
        passed=result.summary.get("passed"),
        hard_eliminated=result.summary.get("hard_eliminated"),
        soft_flagged=result.summary.get("soft_flagged"),
    )
    return pd.DataFrame(result.passed + result.flagged) if (result.passed or result.flagged) else pd.DataFrame()
