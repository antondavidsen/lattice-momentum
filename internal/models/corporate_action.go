package models

import "time"

// CorporateAction represents a split, reverse-split, dividend, or spin-off event.
// Used by trade_outcome_service.go to adjust return calculations.
type CorporateAction struct {
	ID          int64     `db:"id"`
	Ticker      string    `db:"ticker"`
	ExDate      time.Time `db:"ex_date"`
	ActionType  string    `db:"action_type"` // split, reverse_split, dividend, spinoff
	Ratio       float64   `db:"ratio"`       // 4.0 for 4:1 split, 0.1 for 1:10 reverse split
	DividendAmt float64   `db:"dividend_amt"`
	Source      string    `db:"source"` // "polygon"
	CreatedAt   time.Time `db:"created_at"`
}
