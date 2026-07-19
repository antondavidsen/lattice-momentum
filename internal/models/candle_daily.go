package models

import "time"

// CandleDaily is one OHLCV row in the unified candles_daily table.
// It covers individual equities, benchmark indices (SPY, QQQ), and sector ETFs (XLK…).
// All symbols referenced here must exist in the tickers table first.
type CandleDaily struct {
	ID            int64     `db:"id"`
	Ticker        string    `db:"ticker"`
	Date          time.Time `db:"date"`
	Open          float64   `db:"open"`
	High          float64   `db:"high"`
	Low           float64   `db:"low"`
	Close         float64   `db:"close"`
	AdjustedClose *float64  `db:"adjusted_close"` // nil when provider does not supply it
	Volume        int64     `db:"volume"`
	Provider      string    `db:"provider"` // "polygon" | "twelvedata"
	CreatedAt     time.Time `db:"created_at"`
}
