"""
app/screeners/stage1_filter.py
────────────────────────────────
Stage 1 hard elimination rules for screener candidates.

Eliminates deterministic low-quality setups in Python before they reach
the LLM (Stage 2).  Rules are applied in order; each ticker gets at most
one hard elimination reason.

Rules
─────
A — Extension Rule        (Momentum + Leaders only)
B — Fundamental Quality   (Leaders only)
C — Exhaustion Risk       (EP only, flag-only — no elimination)
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

import structlog

log = structlog.get_logger(__name__)

# ── Extension rule thresholds ──────────────────────────────────────────────────
# Rule A: Climax top detection — wide-range expansion bar ABOVE the 52W high.
# Catches blow-off tops ONLY. Does NOT fire on stocks in tight bases near highs
# (which have range contraction, not expansion) — the most common valid setups.
# The old SMA50-extension rule incorrectly eliminated valid pre-breakout bases.
EXT_RANGE_MULT_HARD = 3.0   # today's range > N×ATR = wide expansion bar → hard eliminate
EXT_RANGE_MULT_SOFT = 2.0   # today's range > N×ATR = wide range warning → soft flag
EXT_PERF3M_MIN = 50.0       # perf_3m must also exceed this for rule to trigger

# ── Fundamental quality rule thresholds (Leaders only) ─────────────────────────
# Rule B: Financial engineering flag — EPS growth far outpacing revenue growth
# without margin expansion. Indicates buybacks/tax effects, not genuine earnings.
# Soft flag only — the LLM Step 3 (Earnings Quality) handles the nuance.
FUND_EPS_REV_RATIO = 3.0    # EPS growth > N × revenue growth = financial engineering
FUND_REV_GROWTH_MAX = 5.0   # only flag when revenue growth is also very low (<5%)

# ── Exhaustion risk rule thresholds (EP only) ──────────────────────────────────
EXHAUST_GAP_MIN = 30.0    # gap_pct must exceed this
EXHAUST_PERF1M_MIN = 50.0  # perf_1m must also exceed this


# ── Data classes ───────────────────────────────────────────────────────────────


@dataclass
class EliminationRecord:
    """Record of a single ticker elimination or flag."""
    ticker: str
    screener: str           # "momentum" | "leaders" | "ep"
    rule: str               # "extension_hard" | "fundamental_quality" | etc.
    tier: str               # "hard_eliminate" | "soft_penalty"
    metrics: dict
    reason: str
    date: str               # ISO date YYYY-MM-DD


@dataclass
class FilterResult:
    """Result of applying stage 1 filter to a batch of tickers."""
    passed: list[dict] = field(default_factory=list)
    eliminated: list[EliminationRecord] = field(default_factory=list)
    flagged: list[dict] = field(default_factory=list)
    summary: dict = field(default_factory=dict)


# ── Internal helpers ───────────────────────────────────────────────────────────


def _compute_gap_pct(ticker: dict) -> float | None:
    """
    Compute gap percentage from today's open and prior close.

    gap_pct = (open_today - prev_close) / prev_close * 100

    Field names in raw TV data: "open" (today's open), "close[1]" (prior close).
    Returns None when either value is missing or prev_close is zero.
    """
    open_today = ticker.get("open")
    prev_close = ticker.get("close[1]")

    if open_today is None or prev_close is None or prev_close == 0:
        return None

    return (open_today - prev_close) / prev_close * 100


def _ensure_risk_flags(ticker: dict) -> list:
    """Return the risk_flags list, initialising to [] if absent."""
    if "risk_flags" not in ticker or ticker["risk_flags"] is None:
        ticker["risk_flags"] = []
    return ticker["risk_flags"]


# ── Core filtering function ────────────────────────────────────────────────────


def apply_stage1_filter(
    tickers: list[dict],
    screener: str,
    run_date: str,
) -> FilterResult:
    """
    Apply stage 1 hard elimination rules to a list of ticker dicts.

    Rules are applied in order.  Stop at first hard elimination per ticker.
    Soft penalties (flags) are applied independently — a ticker can have
    both a hard elimination reason AND flags, but only the first hard
    elimination reason is recorded.

    Parameters
    ----------
    tickers : list[dict]
        List of ticker dicts (validated, cleaned rows from the screener).
    screener : str
        One of "momentum", "leaders", "ep".
    run_date : str
        ISO date string YYYY-MM-DD for the run.

    Returns
    -------
    FilterResult
        Split into passed, eliminated, flagged, and summary.
    """
    result = FilterResult()

    for ticker in tickers:
        ticker_symbol = ticker.get("ticker", ticker.get("name", "unknown"))

        # ── Rule A — Climax Top Detection (Momentum + Leaders ONLY) ───────────
        # Fires when today is a wide-range expansion bar ABOVE the 52W high with
        # a large prior move. These are late-stage blow-off tops, not bases.
        # Does NOT fire on tight-base setups near highs (range contraction there).
        if screener in ("momentum", "leaders"):
            high_today = ticker.get("high")
            low_today = ticker.get("low")
            atr = ticker.get("ATR")
            price_52w_high = ticker.get("price_52_week_high")
            close = ticker.get("close")
            perf_3m = ticker.get("Perf.3M")

            if (
                high_today is not None and low_today is not None
                and atr is not None and atr > 0
                and price_52w_high is not None and price_52w_high > 0
                and close is not None
            ):
                today_range = high_today - low_today
                range_mult = today_range / atr

                if (
                    range_mult >= EXT_RANGE_MULT_HARD
                    and close > price_52w_high          # ABOVE 52W high — not in base
                    and perf_3m is not None
                    and perf_3m > EXT_PERF3M_MIN
                ):
                    # Hard eliminate — climax move, not a constructive setup
                    result.eliminated.append(EliminationRecord(
                        ticker=ticker_symbol,
                        screener=screener,
                        rule="climax_top",
                        tier="hard_eliminate",
                        metrics={
                            "today_range": round(today_range, 2),
                            "atr": round(atr, 2),
                            "range_mult": round(range_mult, 2),
                            "close": close,
                            "price_52w_high": price_52w_high,
                            "perf_3m": perf_3m,
                        },
                        reason=(
                            f"Climax top: range {today_range:.2f} = {range_mult:.1f}×ATR, "
                            f"close {close:.2f} above 52W high {price_52w_high:.2f}, "
                            f"perf_3m {perf_3m:.1f}%"
                        ),
                        date=run_date,
                    ))
                    log.warning(
                        "stage1_hard_eliminate",
                        ticker=ticker_symbol,
                        rule="climax_top",
                        range_mult=round(range_mult, 2),
                        perf_3m=perf_3m,
                        screener=screener,
                        date=run_date,
                    )
                    continue  # stop at first hard elimination

                elif (
                    range_mult >= EXT_RANGE_MULT_SOFT
                    and perf_3m is not None
                    and perf_3m > 40.0
                ):
                    # Soft flag: wide range but not necessarily above 52W high
                    flags = _ensure_risk_flags(ticker)
                    flags.append("WIDE_RANGE_WARNING")
                    ticker["range_mult_atr"] = round(range_mult, 2)
                    result.flagged.append(ticker)
                    log.info(
                        "stage1_soft_penalty",
                        ticker=ticker_symbol,
                        flag="WIDE_RANGE_WARNING",
                        range_mult=round(range_mult, 2),
                        perf_3m=perf_3m,
                        screener=screener,
                    )
                    continue

        # ── Rule B — Financial Engineering Flag (Leaders ONLY) ─────────────
        # Flags tickers where EPS growth far exceeds revenue growth with no
        # margin story — indicates buybacks/tax effects, not real earnings.
        # Soft flag only — LLM Step 3 (Earnings Quality) handles the nuance.
        if screener == "leaders":
            eps_growth = ticker.get("earnings_per_share_diluted_yoy_growth_fq")
            rev_growth = ticker.get("total_revenue_yoy_growth_ttm")

            if (
                eps_growth is not None
                and rev_growth is not None
                and rev_growth > 0   # avoid division issues; negative rev is already a problem
            ):
                if (
                    eps_growth > FUND_EPS_REV_RATIO * rev_growth
                    and rev_growth < FUND_REV_GROWTH_MAX
                ):
                    flags = _ensure_risk_flags(ticker)
                    flags.append("FINANCIAL_ENGINEERING_RISK")
                    result.flagged.append(ticker)
                    log.info(
                        "stage1_soft_penalty",
                        ticker=ticker_symbol,
                        flag="FINANCIAL_ENGINEERING_RISK",
                        eps_growth=eps_growth,
                        rev_growth=rev_growth,
                        ratio=round(eps_growth / rev_growth, 1),
                        screener=screener,
                    )
                    continue

        # ── Rule C — Exhaustion Risk (EP ONLY, flag-only) ──────────────────
        if screener == "ep":
            gap_pct = _compute_gap_pct(ticker)
            perf_1m = ticker.get("Perf.1M")

            if gap_pct is not None and perf_1m is not None:
                if gap_pct > EXHAUST_GAP_MIN and perf_1m > EXHAUST_PERF1M_MIN:
                    flags = _ensure_risk_flags(ticker)
                    flags.append("EXHAUSTION_RISK")
                    ticker["gap_pct"] = round(gap_pct, 2)
                    result.flagged.append(ticker)

                    log.info(
                        "stage1_soft_penalty",
                        ticker=ticker_symbol,
                        flag="EXHAUSTION_RISK",
                        gap_pct=round(gap_pct, 2),
                        perf_1m=perf_1m,
                        screener=screener,
                    )
                    continue

        # ── Passed all rules — no elimination, no flag ─────────────────────
        _ensure_risk_flags(ticker)  # ensure field exists even if empty
        result.passed.append(ticker)

    # ── Compute summary ────────────────────────────────────────────────────
    by_rule: dict[str, int] = {}
    by_flag: dict[str, int] = {}
    for rec in result.eliminated:
        by_rule[rec.rule] = by_rule.get(rec.rule, 0) + 1
    for ticker in result.flagged:
        for flag in ticker.get("risk_flags", []):
            by_flag[flag] = by_flag.get(flag, 0) + 1

    result.summary = {
        "evaluated": len(tickers),
        "hard_eliminated": len(result.eliminated),
        "soft_flagged": len(result.flagged),
        "passed": len(result.passed),
        "by_rule": by_rule,
        "by_flag": by_flag,
    }

    return result
