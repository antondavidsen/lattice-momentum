package models

import "time"

// TickerPriority controls the order in which tickers are ingested by the
// nightly candle worker pool.  The values mirror the ticker_priority Postgres
// ENUM added in migration 021.
type TickerPriority string

const (
	// TickerPriorityCritical marks tickers (benchmark indices + sector ETFs)
	// whose candles must be fresh before the regime pipeline can run.
	TickerPriorityCritical TickerPriority = "CRITICAL"

	// TickerPriorityNormal marks every other universe ticker.
	TickerPriorityNormal TickerPriority = "NORMAL"
)

// Ticker holds exchange-level metadata sourced from TradingView CSV exports.
type Ticker struct {
	Ticker    string         `db:"ticker"`
	Name      string         `db:"name"`
	Sector    string         `db:"sector"`
	Industry  string         `db:"industry"`
	Exchange  string         `db:"exchange"`
	Country   string         `db:"country"`
	Priority  TickerPriority `db:"priority"`
	CreatedAt time.Time      `db:"created_at"`
	UpdatedAt time.Time      `db:"updated_at"`
}
