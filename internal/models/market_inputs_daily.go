package models

import "time"

// MarketInputsDaily is the database entity that stores one row of pre-computed
// regime input signals per trading session.
//
// It mirrors the market_inputs_daily table exactly and is intentionally kept
// separate from services.MarketInputs so that the persistence layer never
// imports the services package.
type MarketInputsDaily struct {
	Date time.Time `db:"date"`

	// Benchmark index position relative to moving averages (v1 boolean flags,
	// kept for backward compatibility; v2 classifier uses the continuous
	// percentage-distance fields below).
	SpyAbove50  bool `db:"spy_above_50"`
	SpyAbove200 bool `db:"spy_above_200"`
	QqqAbove50  bool `db:"qqq_above_50"`
	QqqAbove200 bool `db:"qqq_above_200"`

	// IBD-style distribution-day count for SPY (20-session rolling window).
	DistributionDays int `db:"distribution_days"`

	// Market breadth: percentage (0–100) of tracked stocks above their SMA.
	BreadthAbove50  float64 `db:"breadth_above_50"`
	BreadthAbove200 float64 `db:"breadth_above_200"`

	// Latest-session relative-strength ratios (dimensionless price ratio).
	//   > 1.0  →  numerator outperforming SPY
	//   < 1.0  →  numerator underperforming SPY
	QQQvsSPYRS float64 `db:"qqq_vs_spy_rs"`
	IWMvsSPYRS float64 `db:"iwm_vs_spy_rs"`

	// ── v2 signals (migration 020) ────────────────────────────────────────────

	// Continuous SMA-distance signals: percentage distance of the latest close
	// from the given SMA.  Positive = above SMA, negative = below.
	// Example: +3.5 means the close is 3.5 % above the SMA.
	SpyPctFromSMA50  float64 `db:"spy_pct_from_sma50"`
	SpyPctFromSMA200 float64 `db:"spy_pct_from_sma200"`
	QqqPctFromSMA50  float64 `db:"qqq_pct_from_sma50"`
	QqqPctFromSMA200 float64 `db:"qqq_pct_from_sma200"`

	// Golden / death cross: TRUE when SMA-50 is above SMA-200.
	SpySMA50AboveSMA200 bool `db:"spy_sma50_above_sma200"`
	QqqSMA50AboveSMA200 bool `db:"qqq_sma50_above_sma200"`

	// SpyDrawdownPct is SPY's trailing drawdown from the 252-session high,
	// expressed as a negative percentage (e.g. −15.0 = 15 % below the high).
	// Used by the classifier to cap regime labels during bear-market rallies.
	SpyDrawdownPct float64 `db:"spy_drawdown_pct"`

	// ── R-02 regime signal enrichment (migration 043) ──────────────────────────

	// VIXLevel is the daily close of the VIX volatility index.
	// Higher values indicate elevated fear / hedging demand.
	// nil when VIX data is unavailable (data source missing).
	VIXLevel *float64 `db:"vix_level"`

	// VIXROCpct is the 1-day percentage change in VIX close.
	// Positive = volatility spiking higher (bearish signal).
	// nil when can't compute ROC (fewer than 2 VIX candles available).
	VIXROCpct *float64 `db:"vix_roc_pct"`

	// TickMinDaily is the session low of the NYSE $TICK index.
	// Values below −600 indicate broad selling pressure.
	// Nil when the data source (IB TWS / Norgate) is unavailable.
	TickMinDaily *float64 `db:"tick_min_daily"`

	// BreadthVelocity5d is the 5-session change in breadth_above_50
	// (percentage points). Positive = breadth improving; negative = deteriorating.
	BreadthVelocity5d float64 `db:"breadth_velocity_5d"`

	// CreatedAt is the timestamp of the first insert and must never be
	// overwritten on subsequent upserts (see migration 019 notes).
	CreatedAt time.Time `db:"created_at"`

	// UpdatedAt is set to NOW() on every upsert so monitoring tooling and
	// the classifier can tell when a row was last recomputed.
	UpdatedAt time.Time `db:"updated_at"`
}
