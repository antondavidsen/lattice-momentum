package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ScreenerSource identifies which TradingView screener produced a snapshot row.
type ScreenerSource string

// Valid screener sources are "ep", "momentum", and "market_leaders".
const (
	ScreenerEP            ScreenerSource = "ep"
	ScreenerMomentum      ScreenerSource = "momentum"
	ScreenerMarketLeaders ScreenerSource = "market_leaders"
)

// TradingViewSnapshotDaily is a per-screener daily snapshot of TradingView data.
// One row per (ticker, snapshot_date, screener_source) — upserted after each
// collection run.
type TradingViewSnapshotDaily struct {
	ID             uuid.UUID      `db:"id"`
	Ticker         string         `db:"ticker"` // symbol only, e.g. "NVDA"
	SnapshotDate   time.Time      `db:"snapshot_date"`
	ScreenerSource ScreenerSource `db:"screener_source"` // "ep" | "momentum" | "market_leaders"

	// ── Price / Liquidity ─────────────────────────────────────────────────
	Open            *float64 `db:"open"`
	High            *float64 `db:"high"`
	Low             *float64 `db:"low"`
	Close           *float64 `db:"close"`
	Volume          *int64   `db:"volume"`
	RelativeVolume  *float64 `db:"relative_volume"`
	PriceXVolume10d *float64 `db:"price_x_volume_10d"` // close × avg_volume_10d
	MarketCap       *float64 `db:"market_cap"`
	AvgVolume10d    *int64   `db:"avg_volume_10d"` // 10-day average volume

	// ── Technical ─────────────────────────────────────────────────────────
	RSI14           *float64 `db:"rsi_14"`
	Perf3M          *float64 `db:"perf_3m"`
	Perf6M          *float64 `db:"perf_6m"`
	Distance52wHigh *float64 `db:"distance_52w_high"` // ((close/high)-1)×100; 0 = at high, negative = below
	Price52wHigh    *float64 `db:"price_52w_high"`
	Price52wLow     *float64 `db:"price_52w_low"`
	GapPct          *float64 `db:"gap_pct"`    // opening gap % vs prior close
	ChangePct       *float64 `db:"change_pct"` // intraday % change

	// ── Moving averages ───────────────────────────────────────────────────
	SMA20  *float64 `db:"sma_20"`
	SMA50  *float64 `db:"sma_50"`
	SMA150 *float64 `db:"sma_150"`
	SMA200 *float64 `db:"sma_200"`

	// ── Volatility ────────────────────────────────────────────────────────
	ATR14 *float64 `db:"atr_14"` // 14-day Average True Range

	// ── Share structure ───────────────────────────────────────────────────
	FloatShares *int64 `db:"float_shares"` // float shares outstanding

	// ── Fundamentals ──────────────────────────────────────────────────────
	EPSTTM           *float64   `db:"eps_ttm"`
	EPSGrowthYOY     *float64   `db:"eps_growth_yoy"`
	RevenueTTM       *float64   `db:"revenue_ttm"`
	RevenueGrowthYOY *float64   `db:"revenue_growth_yoy"`
	ROE              *float64   `db:"roe"`
	GrossMargin      *float64   `db:"gross_margin"`
	NetMargin        *float64   `db:"net_margin"`
	OperatingMargin  *float64   `db:"operating_margin"`
	EarningsDate     *time.Time `db:"earnings_date"`

	// ── Metadata ──────────────────────────────────────────────────────────
	Sector   *string `db:"sector"`
	Exchange *string `db:"exchange"`

	RawJSON   json.RawMessage `db:"raw_json"` // NOT NULL
	CreatedAt time.Time       `db:"created_at"`
}
