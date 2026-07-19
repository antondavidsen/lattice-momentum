package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MarketSnapshotRequest is the JSON payload sent by tv-collector.
// Each screener slice contains raw rows returned by TradingView
// (one map per ticker, keys = column names).
type MarketSnapshotRequest struct {
	Date           string           `json:"date"`            // "YYYY-MM-DD"
	Momentum       []map[string]any `json:"momentum"`        // momentum screener rows
	EpisodicPivots []map[string]any `json:"episodic_pivots"` // EP screener rows
	MarketLeaders  []map[string]any `json:"market_leaders"`  // market-leaders rows
	// Market index OHLC for the day (e.g. SPY/SPX), optional.
	Open float64 `json:"open"`
	High float64 `json:"high"`
	Low  float64 `json:"low"`
}

// MarketSnapshot is the DB representation of a received snapshot.
type MarketSnapshot struct {
	ID             uuid.UUID       `db:"id"`
	SnapshotDate   time.Time       `db:"snapshot_date"`
	Momentum       json.RawMessage `db:"momentum"`
	EpisodicPivots json.RawMessage `db:"episodic_pivots"`
	MarketLeaders  json.RawMessage `db:"market_leaders"`
	RowCounts      json.RawMessage `db:"row_counts"`
	// Market index OHLC for the day.
	Open       float64   `db:"open"`
	High       float64   `db:"high"`
	Low        float64   `db:"low"`
	ReceivedAt time.Time `db:"received_at"`
}

// SnapshotRowCounts holds the count of rows per screener for the summary response.
type SnapshotRowCounts struct {
	Momentum       int `json:"momentum"`
	EpisodicPivots int `json:"episodic_pivots"`
	MarketLeaders  int `json:"market_leaders"`
}
