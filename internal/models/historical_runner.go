package models

import (
	"time"

	"github.com/google/uuid"
	pgvector "github.com/pgvector/pgvector-go"
)

// ── Catalyst categories ───────────────────────────────────────────────────────
const (
	CatalystEarningsBeat = "earnings_beat"
	CatalystFDABiotech   = "fda_biotech"
	CatalystMNA          = "mna"
	CatalystContract     = "contract"
	CatalystUpgrade      = "upgrade"
	CatalystPartnership  = "partnership"
	CatalystShortSqueeze = "short_squeeze"
	CatalystSympathy     = "sympathy"
	CatalystTechnical    = "technical"
	CatalystUnknown      = "unknown"
)

// CatalystScores maps catalyst categories to their base score (0–10).
var CatalystScores = map[string]int{
	CatalystEarningsBeat: 10,
	CatalystFDABiotech:   10,
	CatalystMNA:          9,
	CatalystContract:     8,
	CatalystShortSqueeze: 7,
	CatalystUpgrade:      6,
	CatalystPartnership:  6,
	CatalystSympathy:     5,
	CatalystTechnical:    3,
	CatalystUnknown:      0,
}

// CatalystTypeEncoding returns a normalised ordinal for feature vectors.
func CatalystTypeEncoding(category string) float64 {
	switch category {
	case CatalystEarningsBeat:
		return 1.0
	case CatalystFDABiotech:
		return 0.9
	case CatalystMNA:
		return 0.8
	case CatalystContract:
		return 0.7
	case CatalystShortSqueeze:
		return 0.6
	case CatalystUpgrade, CatalystPartnership:
		return 0.5
	case CatalystSympathy:
		return 0.4
	case CatalystTechnical:
		return 0.2
	default:
		return 0.0
	}
}

// ── HistoricalRunner ──────────────────────────────────────────────────────────

// HistoricalRunner represents one row in the historical_runners table.
type HistoricalRunner struct {
	ID uuid.UUID `db:"id"`

	Ticker string    `db:"ticker"`
	Date   time.Time `db:"date"`

	// Catalyst
	CatalystCategory   string   `db:"catalyst_category"`
	CatalystHeadline   *string  `db:"catalyst_headline"`
	CatalystScore      int      `db:"catalyst_score"`
	CatalystConfidence *float64 `db:"catalyst_confidence"`

	// Price action
	PrevClose    float64 `db:"prev_close"`
	OpenPrice    float64 `db:"open_price"`
	HighPrice    float64 `db:"high_price"`
	LowPrice     float64 `db:"low_price"`
	ClosePrice   float64 `db:"close_price"`
	Volume       int64   `db:"volume"`
	AvgVolume20D int64   `db:"avg_volume_20d"`

	// Computed signal-day metrics
	GapPct           float64  `db:"gap_pct"`
	RelVolume        float64  `db:"rel_volume"`
	IntradayReturn   float64  `db:"intraday_return"`
	IntradayRangePct float64  `db:"intraday_range_pct"`
	MaxIntradayRunup float64  `db:"max_intraday_runup"`
	CloseVsRange     *float64 `db:"close_vs_range"`

	// Supply dynamics
	FloatShares *int64   `db:"float_shares"`
	MarketCap   *float64 `db:"market_cap"`
	Sector      *string  `db:"sector"`

	// Forward performance
	Day2Open    *float64 `db:"day2_open"`
	Day2Close   *float64 `db:"day2_close"`
	Day2Return  *float64 `db:"day2_return"`
	HeldGainsD2 *bool    `db:"held_gains_d2"`
	Day5Return  *float64 `db:"day5_return"`

	// Metadata
	Source              string           `db:"source"`
	HasSurvivorshipFlag bool             `db:"has_survivorship_flag"`
	FeatureVector       *pgvector.Vector `db:"feature_vector"`
	IsUnremarkable      bool             `db:"is_unremarkable"`
	CreatedAt           time.Time        `db:"created_at"`
	UpdatedAt           time.Time        `db:"updated_at"`
}
