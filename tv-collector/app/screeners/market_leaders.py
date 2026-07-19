"""
app/screeners/market_leaders.py
────────────────────────────────
Market Leaders screener — Growth Momentum Investing: fundamental quality filter.

Production filter spec (v2 — April 2026 revision)
──────────────────────────────────────────────────
Universe  (server-side — tightened to match engine requirements)
  • Exchange ∈ {NASDAQ, NYSE, AMEX}
  • Market cap  ≥ $2 B              (institutional-grade minimum)
  • Price       ≥ $20               (institutional-grade minimum)
  • Avg volume  ≥ 500 k shares (10-day)

Fundamentals  (server-side)
  • EPS TTM          ≥ 1
  • EPS growth YoY   ≥ 20 %    (An earnings acceleration)
  • Revenue TTM      ≥ $100 M
  • ROE              ≥ 15 %
  • Gross margin     ≥ 35 %    (pricing power — true market leaders)
  • Operating margin ≥ 10 %
  • Net margin       ≥ 10 %

Trend  (server-side)
  • RSI    ≥ 50
  • Close  > SMA50               (must be in uptrend — institutional leaders
                                  don't trade below their 50-day)
  • Perf 3M > 0 %   (positive 3-month return)
  • Perf 6M > 0 %   (positive 6-month return)

Price location  (post-filter)
  • Close  ≥ 75 % of 52-week high (near highs — leaders trade near highs)

Design decisions (v2 — April 2026 revision)
────────────────────────────────────────────
• Raised market cap from $500M to $2B and price from $10 to $20 to match the
  engine's actual institutional-quality filters. Previously ~70% of the 200-row
  API window was wasted on tickers immediately discarded by the engine.
• Added Close > SMA50 as trend gate — a stock below its 50-day SMA is not an
  institutional leader regardless of fundamentals.
• Removed relVol ≥ 1.2 from post-filter — the best institutional leaders are
  accumulated methodically at steady 0.8-1.1× relative volume. Requiring
  above-average volume biases toward news-driven spikes, not steady accumulation.

Sort
  • Primary:   EPS growth YoY DESC  (earnings acceleration)
  • Secondary: ROE DESC
  • Tertiary:  Perf.3M DESC        (relative strength tiebreaker)
"""

from __future__ import annotations

from datetime import date

import pandas as pd
import structlog
from tradingview_screener import col

from app.clients.tradingview_client import TradingViewClient
from app.services.validation import validate_batch

log = structlog.get_logger(__name__)

SCREENER_NAME = "market_leaders"
RESULT_LIMIT = 200

COLUMNS = [
    "name",
    "close",
    "open",
    "high",
    "low",
    "volume",
    "relative_volume_10d_calc",
    "average_volume_10d_calc",            # 10-day avg volume (hard filter)
    "market_cap_basic",
    "RSI",
    "sector",
    # Moving averages — full Stage 2 stack for trend confirmation + Go engine scoring
    "SMA20",                              # short-term trend — LLM prompt Step 2 fallback
    "SMA50",
    "SMA150",
    "SMA200",
    "SMA200[1]",                          # SMA200 trending-up check in Go engine
    "ATR",                                # needed by stage1 climax-top rule
    # fundamentals — all verified against the live API
    "earnings_per_share_basic_ttm",       # EPS trailing twelve months
    "earnings_per_share_diluted_ttm",     # diluted EPS TTM
    "earnings_per_share_diluted_yoy_growth_fq",  # EPS growth YoY (last quarter)
    "total_revenue_ttm",                  # revenue TTM
    "total_revenue_yoy_growth_ttm",       # revenue growth YoY TTM
    "return_on_equity_fq",                # ROE (most recent quarter)
    "gross_margin",                       # gross margin %
    "net_margin",                         # net profit margin % (LLM assessment only)
    "operating_margin",                   # operating margin %
    "earnings_release_next_date",         # upcoming catalyst
    "price_52_week_high",
    "price_52_week_low",
    "close[1]",                           # prior session close — gap_pct for Go engine
    "Perf.3M",                            # 3-month price performance
    "Perf.6M",                            # 6-month price performance
]


def run(client: TradingViewClient) -> pd.DataFrame:
    """
    Execute the Market Leaders screener and return results as a DataFrame.

    Two-stage filtering:
      1. TradingView API-level ``where()`` filters apply all conditions that
         map directly to a single column comparison, including exchange,
         fundamental gates, and trend confirmation.
      2. Pandas post-filter handles the derived condition (close ≥ 75% of
         52-week high), then applies the three-column sort.

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
            # Universe — tightened to match engine's institutional-quality gate.
            col("exchange").isin(["NASDAQ", "NYSE", "AMEX"]),
            col("market_cap_basic") >= 2_000_000_000,        # ≥ $2 B (was $500M)
            col("close") >= 20,                              # ≥ $20 (was $10)
            col("average_volume_10d_calc") >= 500_000,       # ≥ 500 k avg volume
            # Fundamentals
            col("earnings_per_share_basic_ttm") >= 1,        # EPS TTM ≥ 1
            col("earnings_per_share_diluted_yoy_growth_fq") >= 20,  # EPS accel ≥ 20 %
            col("total_revenue_ttm") >= 100_000_000,         # revenue TTM ≥ $100 M
            col("return_on_equity_fq") >= 15,                # ROE ≥ 15 %
            col("gross_margin") >= 35,                       # gross margin ≥ 35 %
            col("operating_margin") >= 10,                   # operating margin ≥ 10 %
            # net_margin intentionally excluded — assessed by LLM Step 3 (Earnings Quality).
            # Many genuine leaders have net margins of 5–9% due to R&D reinvestment or
            # tax timing; hard-gating here eliminates valid names before the LLM can judge.
            # Trend confirmation
            col("RSI") >= 50,                                # in an uptrend
            col("close") > col("SMA50"),                     # above 50-day SMA
            col("Perf.3M") > 0,                              # positive 3-month return
            col("Perf.6M") > 0,                              # positive 6-month return
        )
        # Primary sort: earnings acceleration keeps the fastest growers at
        # the top of the 200-row API window.
        .order_by("earnings_per_share_diluted_yoy_growth_fq", ascending=False)
        .limit(RESULT_LIMIT)
    )

    try:
        df = client.execute_query(query)
    except Exception:
        return pd.DataFrame()

    # Early-return guard: if df is empty or missing required columns, return empty
    required = {"ticker", "close", "volume", "SMA150", "SMA200", "SMA50",
                "price_52_week_high"}
    if df.empty or not required.issubset(df.columns):
        return pd.DataFrame()

    pre_filter_rows = len(df)

    # ── Stage 2: pandas post-filters ─────────────────────────────────────────

    # SMA150 field availability check — TV API may not always return it.
    if "SMA150" not in df.columns or df["SMA150"].isna().all():
        log.error(
            "screener.field_missing",
            screener=SCREENER_NAME,
            field="SMA150",
            reason="SMA150 not returned by TradingView API — using SMA200×1.05 proxy",
        )
        df["SMA150"] = df["SMA200"] * 1.05

    # Stage 2 MA structure: Close > SMA150 AND SMA150 > SMA200 (Weinstein).
    # Rows with missing SMA150/SMA200 are kept (benefit of doubt — Go engine
    # will score trend_alignment lower without the data).
    df = df[
        df["SMA150"].isna() | df["SMA200"].isna() |
        (
            (df["close"] > df["SMA150"]) &
            (df["SMA150"] > df["SMA200"])
        )
    ]

    # Near-highs: Close ≥ 75% of 52-week high.
    # No relative volume gate — institutional leaders accumulate at steady
    # volume, not spikes. Requiring relVol ≥ 1.2 biased toward news events.
    df = df[df["close"] >= 0.75 * df["price_52_week_high"]]

    # ── Stage 3: final sort ───────────────────────────────────────────────────
    # Primary: EPS growth DESC  |  Secondary: ROE DESC  |  Tertiary: Perf.3M DESC
    df = df.sort_values(
        by=["earnings_per_share_diluted_yoy_growth_fq", "return_on_equity_fq", "Perf.3M"],
        ascending=[False, False, False],
    )

    # ── Stage 4: schema validation ────────────────────────────────────────────
    # Extract raw rows, validate against the market_leaders schema, and return
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

    # ── Stage 5: stage 1 hard elimination filter ──────────────────────────────
    from .stage1_filter import apply_stage1_filter
    from .audit_writer import write_audit
    from ..config import load_config

    today_str = date.today().isoformat()

    # Diagnostic logging: pre-stage1 counts
    log.info(
        "screener.pre_stage1",
        screener=SCREENER_NAME,
        rows=len(valid_rows),
    )

    result = apply_stage1_filter(
        tickers=valid_rows,
        screener="leaders",
        run_date=today_str,
    )

    # Diagnostic logging: post-stage1 breakdown
    log.info(
        "screener.post_stage1",
        screener=SCREENER_NAME,
        passed=len(result.passed),
        flagged=len(result.flagged),
        eliminated=len(result.eliminated),
    )

    write_audit(result.eliminated, load_config().backend_api_url)
    log.info(
        "stage1_summary",
        passed=result.summary.get("passed"),
        hard_eliminated=result.summary.get("hard_eliminated"),
        soft_flagged=result.summary.get("soft_flagged"),
    )
    return pd.DataFrame(result.passed + result.flagged) if (result.passed or result.flagged) else pd.DataFrame()
