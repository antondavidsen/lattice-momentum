"""
app/screeners/momentum.py
─────────────────────────
Momentum screener — identifies stocks with strong price and volume momentum.

Production filter spec
──────────────────────────────────────────────────────────
Universe  (server-side)
  • Exchange ∈ {NASDAQ, NYSE, AMEX}
  • Market cap  ≥ $500 M
  • Price       ≥ $10
  • Avg volume  ≥ 300 k shares (10-day)

Stage 2 MA structure  (server-side, all verified as live API fields)
  • Close  > SMA20   (≈ 21-day SMA — closest standard period)
  • Close  > SMA50
  • SMA50  > SMA150
  • SMA150 > SMA200
  • SMA200 trending up  (SMA200 > SMA200[1])

Relative strength / performance  (server-side)
  • Perf 1M ≥ 5 %    (short-term RS — best predictor of near-term continuation)
  • Perf 3M ≥ 15 %   (mid-term RS — confirms persistent strength)
  • Perf 6M ≥ 20 %   (long-term RS — Stage 2 trend validation)

Price location  (post-filter)
  • Close ≥ 75 % of 52-week high   (in a proper base near highs)
  • Close within 2 × ATR of 52-week high  (ATR-adjusted extension filter —
    replaces rigid 105 % cap; handles fast-movers more gracefully)

Consolidation / base quality  (post-filter)
  • Today's range ≤ 1.5 × ATR      (range contraction proxy — tight setups)

Volume / Liquidity  (post-filter)
  • Dollar volume ≥ $10 M           (sufficient for retail swing)

Design decisions (v2 — April 2026 revision)
────────────────────────────────────────────
• Removed server-side relVol ≥ 1.5× — volume DRY-UP before breakout is the
  #1 setup quality indicator (Qullamaggie). Requiring elevated volume kills
  80% of valid pre-breakout bases. Volume expansion is scored in the ranking
  engine instead, where it gets credit on breakout days without being a gate.
• Removed ADR ≥ 3% filter — contradicted the consolidation/base-tightness
  filter (range ≤ 1.5×ATR). A stock in a tight VCP base has ADR of 1.5-2.5%
  which is exactly the ideal entry. ADR filter belongs on EP, not here.
• Lowered dollar volume from $20M to $10M to capture mid-cap momentum names
  ($3-8B market cap) that produce 30-50% runs.
• Lowered avg volume from 500K to 300K for same reason.
• Added Perf.1M as the strongest short-term RS signal.
• Lowered Perf.3M from 20% to 15% and Perf.6M from 30% to 20% to cast a wider
  net — the ranking engine handles fine-grained quality separation.

Sort
  • Primary:   Perf 6M DESC    (best available RS proxy)
  • Secondary: Perf 3M DESC
  • Tertiary:  Perf 1M DESC
"""

from __future__ import annotations

from datetime import date

import pandas as pd
import structlog
from tradingview_screener import col

from app.clients.tradingview_client import TradingViewClient
from app.services.validation import validate_batch

log = structlog.get_logger(__name__)

SCREENER_NAME = "momentum"
RESULT_LIMIT = 200

COLUMNS = [
    "name",
    "open",
    "close",
    "high",
    "low",
    "volume",
    "relative_volume_10d_calc",
    "average_volume_10d_calc",
    "market_cap_basic",
    "RSI",
    "sector",
    "change",
    "gap",
    "price_52_week_high",
    "price_52_week_low",
    # Stage 2 moving averages (all confirmed against live TV API)
    "SMA20",        # ≈ 21-day SMA (closest standard period in TV)
    "SMA50",
    "SMA150",
    "SMA200",
    "SMA200[1]",    # previous day's SMA200 — for "SMA200 trending up"
    # Performance / relative strength
    "Perf.W",       # 1-week price performance (short-term RS)
    "Perf.1M",      # 1-month — best near-term continuation predictor
    "Perf.3M",
    "Perf.6M",
    # Volatility
    "ATR",          # 14-day Average True Range — extension & consolidation filters
    # Fundamentals — needed by LLM prompt renderer for momentum quality scoring.
    "earnings_per_share_basic_ttm",       # EPS TTM
    "earnings_per_share_diluted_yoy_growth_fq",  # EPS growth YoY (last quarter)
    "total_revenue_ttm",                  # revenue TTM ($)
    "total_revenue_yoy_growth_ttm",       # revenue growth YoY TTM (%)
    "return_on_equity_fq",                # ROE (most recent quarter)
    "gross_margin",                       # gross margin %
    "operating_margin",                   # operating margin %
    "net_margin",                         # net profit margin %
    "earnings_release_next_date",         # upcoming earnings date
    "float_shares_outstanding",           # share-structure context
    "close[1]",                           # prior session close — gap_pct for Go engine
]


def run(client: TradingViewClient) -> pd.DataFrame:
    """
    Execute the momentum screener and return results as a DataFrame.

    Three-stage pipeline:
      1. TradingView API ``where()`` — server-side filters for exchange,
         single-column, and column-to-column comparisons.
      2. Pandas post-filters — derived conditions requiring column arithmetic
         (dollar volume, ATR-adjusted extension, consolidation).
      3. Multi-column sort.

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
            # Universe — restrict to major exchanges server-side.
            col("exchange").isin(["NASDAQ", "NYSE", "AMEX"]),
            col("market_cap_basic") >= 500_000_000,          # ≥ $500 M
            col("close") >= 10,                              # ≥ $10
            col("average_volume_10d_calc") >= 300_000,       # ≥ 300 k avg vol
            # Stage 2 MA structure
            col("close") > col("SMA20"),                     # above 20-day SMA
            col("close") > col("SMA50"),                     # above 50-day SMA
            # SMA50 > SMA150 and SMA150 > SMA200 moved to pandas post-filter
            # (SMA150 may not be available from TV API — proxy applied first)
            col("SMA200") > col("SMA200[1]"),                # SMA200 trending up
            # Relative strength (multi-timeframe confirmation)
            col("Perf.1M") >= 5,                             # ≥ 5 % in 1 month
            col("Perf.3M") >= 15,                            # ≥ 15 % in 3 months
            col("Perf.6M") >= 20,                            # ≥ 20 % in 6 months
        )
        # Primary sort at API level keeps the top 200 by 6M RS before
        # post-filters narrow the set further.
        .order_by("Perf.6M", ascending=False)
        .limit(RESULT_LIMIT)
    )

    try:
        df = client.execute_query(query)
    except Exception:
        return pd.DataFrame()

    # Early-return guard: if df is empty or missing required columns, return empty
    required = {"ticker", "close", "volume", "SMA200", "SMA150", "SMA20", "SMA50",
                "high", "low", "SMA200[1]", "ATR"}
    if df.empty or not required.issubset(df.columns):
        return pd.DataFrame()

    pre_filter_rows = len(df)

    # SMA150 field availability check — verify the TV API returned it.
    if "SMA150" not in df.columns or df["SMA150"].isna().all():
        log.error(
            "screener.field_missing",
            screener=SCREENER_NAME,
            field="SMA150",
            reason="SMA150 not returned by TradingView API — using SMA200×1.05 proxy",
        )
        df["SMA150"] = df["SMA200"] * 1.05

    # ── Stage 2: pandas post-filters ─────────────────────────────────────────

    # Stage 2 MA structure: SMA50 > SMA150 AND SMA150 > SMA200 (Weinstein).
    # Applied here (not server-side) so the SMA150 proxy runs first.
    # Rows with missing SMA150/SMA200 are kept (benefit of doubt).
    df = df[
        df["SMA150"].isna() | df["SMA200"].isna() |
        (
            (df["SMA50"] > df["SMA150"]) &
            (df["SMA150"] > df["SMA200"])
        )
    ]

    # Dollar volume ≥ $10 M  (close × avg_volume_10d)
    df = df[df["close"] * df["average_volume_10d_calc"] >= 10_000_000]

    # Price location — lower bound: close ≥ 75 % of 52-week high.
    df = df[df["close"] >= 0.75 * df["price_52_week_high"]]

    # ATR-adjusted extension filter — a stock is over-extended only when
    # close exceeds the 52-week high by more than 2 × ATR.  Rows where
    # ATR is missing/zero fall back to 105 % hard cap.
    atr = df["ATR"].fillna(0)
    extension_limit = df["price_52_week_high"] + 2 * atr
    fallback = 1.05 * df["price_52_week_high"]
    upper_bound = extension_limit.where(atr > 0, fallback)
    df = df[df["close"] <= upper_bound]

    # Consolidation / base-tightness filter: today's range ≤ 1.5 × ATR.
    # Identifies stocks with range contraction (tight bases) — the hallmark
    # of constructive setups about to break out.  Extended names with
    # blow-off wide-range bars are filtered.  Rows where ATR is missing are
    # kept (benefit of doubt).
    atr_post = df["ATR"]
    daily_range_post = df["high"] - df["low"]
    df = df[atr_post.isna() | (daily_range_post <= 1.5 * atr_post)]

    # ── Stage 3: final sort ───────────────────────────────────────────────────
    # Primary: Perf.6M DESC  |  Secondary: Perf.3M DESC  |  Tertiary: Perf.1M DESC
    df = df.sort_values(
        by=["Perf.6M", "Perf.3M", "Perf.1M"],
        ascending=[False, False, False],
    )

    # ── Stage 4: schema validation ────────────────────────────────────────────
    # Extract raw rows, validate against the momentum schema, and return only
    # valid rows.  Rejects batches with header mismatches.
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
        screener="momentum",
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
