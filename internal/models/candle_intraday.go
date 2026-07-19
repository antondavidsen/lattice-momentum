package models

import "time"

// CandleIntraday is one OHLCV row in the candles_intraday table.
// It represents a 1-minute or 5-minute bar for a given ticker.
type CandleIntraday struct {
	ID       int64     `db:"id"`
	Ticker   string    `db:"ticker"`
	Date     time.Time `db:"date"`
	TS       time.Time `db:"ts"`
	Interval string    `db:"interval"` // "1min" | "5min"
	Open     float64   `db:"open"`
	High     float64   `db:"high"`
	Low      float64   `db:"low"`
	Close    float64   `db:"close"`
	Volume   int64     `db:"volume"`
	VWAP     float64   `db:"vwap"`
	Provider string    `db:"provider"` // "polygon"
}
