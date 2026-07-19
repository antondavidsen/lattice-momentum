package models

import "time"

// TradeOutcomeDaily tracks forward performance of a ranked trade signal.
// One row per (entry_date, list_type, ticker) in the trade_outcomes_daily table.
//
// Entry model (Ticket 6.2):
//
//	Signal published after market close on day T → entry_date = T.
//	Entry price = T+1 Open (next trading day's opening price).
//	Tracking window = T+1 through T+20 (20 trading days).
//	MRU / MDD use highs/lows of the entire tracking window (including T+1).
//	Fixed-horizon returns use the close of the Nth tracking day vs entry.
type TradeOutcomeDaily struct {
	EntryDate time.Time `db:"entry_date"`
	ListType  ListType  `db:"list_type"`
	Ticker    string    `db:"ticker"`
	Rank      int       `db:"rank"`

	// EntryPrice is the T+1 Open — the opening price on the first trading
	// day after the signal is published.
	EntryPrice float64 `db:"entry_price"`

	// Forward returns — nil until the required tracking days have elapsed.
	// Measured as (close of day N − entry) / entry.
	Return1D  *float64 `db:"return_1d"`
	Return2D  *float64 `db:"return_2d"`
	Return3D  *float64 `db:"return_3d"`
	Return4D  *float64 `db:"return_4d"`
	Return5D  *float64 `db:"return_5d"`
	Return10D *float64 `db:"return_10d"`
	Return20D *float64 `db:"return_20d"`

	// Continuous risk / opportunity metrics over the tracking window.
	// MRU = (highest high T+1..T+20 − entry) / entry
	// MDD = (lowest low   T+1..T+20 − entry) / entry
	MaxRunup20D    *float64 `db:"max_runup_20d"`
	MaxDrawdown20D *float64 `db:"max_drawdown_20d"`

	// EvaluatedDays records how many tracking days (T+1 onward) have been observed.
	EvaluatedDays int `db:"evaluated_days"`

	// IsDuplicateSignal is true when this signal's ticker also appeared in
	// another pipeline within 3 trading days while the earlier signal was
	// still open (evaluated_days < 20).
	IsDuplicateSignal bool `db:"is_duplicate_signal"`

	// TradingDaysSincePrior is the number of trading days between this
	// signal's entry_date and the prior open signal for the same ticker.
	// Only meaningful when IsDuplicateSignal is true.
	TradingDaysSincePrior int `db:"trading_days_since_prior"`

	// CorporateActionCount tracks the number of corporate actions found in the
	// measurement window for this outcome. 0 means no actions were found.
	// Used to flag rows that needed adjustment.
	CorporateActionCount int `db:"corporate_action_count"`

	// ── STORY-R04 fields ─────────────────────────────────────────────────────

	// IsPrimaryObservation is true if this row is the canonical observation for
	// (ticker, list_type) within the rolling 20-trading-day window.  At most one
	// row per (ticker, list_type) can have this set to true at any time.
	IsPrimaryObservation bool `db:"is_primary_observation"`

	// CrossListDuplicate is true when the same ticker appears on multiple lists
	// for the same entry date and this row is NOT the highest-conviction list
	// (priority: leaders > momentum > ep).
	CrossListDuplicate bool `db:"cross_list_duplicate"`

	// ClusterID groups rows whose 20d outcome windows overlap by ≥5 trading days.
	// Used for cluster-robust standard errors in R05.
	ClusterID *int64 `db:"cluster_id"`

	// ── STORY-R06 fields ─────────────────────────────────────────────────────

	// RegimeLabel is the market regime on entry_date, joined from market_regime_daily.
	RegimeLabel *string `db:"regime_label"`

	// SlippageTier is the assigned slippage tier (leaders/momentum/ep_tier1/ep_tier2/default).
	SlippageTier *string `db:"slippage_tier"`

	// ADVCapApplied is true when position size was capped at 2% ADV.
	ADVCapApplied bool `db:"adv_cap_applied"`

	// ADVCapPct is the capped position size percentage when ADV cap was applied.
	ADVCapPct *float64 `db:"adv_cap_pct"`

	// NetReturn5D is the gross return_5d minus slippage costs.
	NetReturn5D *float64 `db:"net_return_5d"`

	// NetReturn10D is the gross return_10d minus slippage costs.
	NetReturn10D *float64 `db:"net_return_10d"`

	// NetReturn20D is the gross return_20d minus slippage costs.
	NetReturn20D *float64 `db:"net_return_20d"`

	// ExitType is the path-aware exit type: 'stop', 'target1', 'target2', 'hold'.
	ExitType *string `db:"exit_type"`

	// StopSlippageBps is the extra slippage bps applied for stop exits.
	StopSlippageBps *float64 `db:"stop_slippage_bps"`

	// RegimeBucket is a generated column: 'risk_on' when regime_label IN (strong_bull, bull),
	// 'risk_off' when regime_label IN (neutral, correction, bear), else NULL.
	RegimeBucket *string `db:"regime_bucket"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// TickerSnapshot holds price and ADV for a ticker on a specific date.
// Used by the slippage model and NetReturnJob.
type TickerSnapshot struct {
	Price float64
	ADV   float64
}
