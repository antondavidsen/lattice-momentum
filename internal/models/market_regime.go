package models

import "time"

// Legacy regime labels — kept for backward compatibility with existing code
// that reads from the old market_regime table (migration 007).
//

// MarketRegimeDaily stores the nightly classifier output for one trading
// session.  It mirrors the market_regime_daily table (migrations 019 + 020)
// exactly.
//
// Regime values are restricted by a CHECK constraint in the database; use
// internal/services/regime.RegimeLabel to produce valid values.
type MarketRegimeDaily struct {
	Date       time.Time `db:"date"`
	Regime     string    `db:"regime"`     // strong_bull | bull | neutral | correction | bear
	Confidence *float64  `db:"confidence"` // 0–100: smoothed bull strength × 100
	Notes      *string   `db:"notes"`

	// v2 smoothing columns (migration 020); NULL for rows written by the v1
	// classifier before the migration was applied.
	RawBullStrength      *float64 `db:"raw_bull_strength"`
	SmoothedBullStrength *float64 `db:"smoothed_bull_strength"`

	// BreadthDivergenceSignal is the R-11 breadth divergence flag (migration 048).
	// 1.0 = SPY up but NYSE A/D line flat/falling (distribution warning).
	// 0.0 = no divergence detected. NULL = not yet computed.
	BreadthDivergenceSignal *float64 `db:"breadth_divergence_signal"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// MarketRegime stores the nightly classifier output for one trading session.
type MarketRegime struct {
	Date                time.Time `db:"date"`
	Regime              string    `db:"regime"`
	GateLevel           string    `db:"gate_level"`
	SPYClose            *float64  `db:"spy_close"`
	SPYSMA50            *float64  `db:"spy_sma50"`
	SPYSMA200           *float64  `db:"spy_sma200"`
	SPYAboveSMA50       *bool     `db:"spy_above_sma50"`
	SPYAboveSMA200      *bool     `db:"spy_above_sma200"`
	QQQClose            *float64  `db:"qqq_close"`
	QQQSMA50            *float64  `db:"qqq_sma50"`
	QQQSMA200           *float64  `db:"qqq_sma200"`
	QQQAboveSMA50       *bool     `db:"qqq_above_sma50"`
	QQQAboveSMA200      *bool     `db:"qqq_above_sma200"`
	AdvanceDeclineRatio *float64  `db:"advance_decline_ratio"`
	Notes               *string   `db:"notes"`
	CreatedAt           time.Time `db:"created_at"`
}
