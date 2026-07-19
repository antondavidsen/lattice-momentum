package models

import "time"

// SectorLeadershipDaily mirrors the sector_leadership_daily table (migration 047).
// Stores per-ticker, per-date leadership scores within their sector.
type SectorLeadershipDaily struct {
	ID              int64     `db:"id"`
	Ticker          string    `db:"ticker"`
	Date            time.Time `db:"date"`
	SectorETF       string    `db:"sector_etf"`
	LeadershipScore float64   `db:"leadership_score"`
	TickerReturn5d  *float64  `db:"ticker_return_5d"`
	SectorReturn5d  *float64  `db:"sector_return_5d"`
	IsLeader        bool      `db:"is_leader"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}
