package models

import "time"

// NarrativeVelocityDaily stores per-ticker narrative velocity scores for a given date.
// Computed from ticker_enrichments news data: measures how fast news coverage is
// accelerating relative to the ticker's 30-day baseline.
type NarrativeVelocityDaily struct {
	ID                 int64     `db:"id" json:"-"`
	Ticker             string    `db:"ticker" json:"ticker"`
	Date               time.Time `db:"date" json:"date"`
	NewsFreshnessScore float64   `db:"news_freshness_score" json:"news_freshness_score"`
	CoverageAccelScore float64   `db:"coverage_accel_score" json:"coverage_accel_score"`
	NarrativeVelocity  float64   `db:"narrative_velocity" json:"narrative_velocity"`
	Headlines24h       int       `db:"headlines_24h" json:"headlines_24h"`
	BaselineDailyRate  float64   `db:"baseline_daily_rate" json:"baseline_daily_rate"`
	UniqueSources24h   int       `db:"unique_sources_24h" json:"unique_sources_24h"`
	AvgSources30d      float64   `db:"avg_sources_30d" json:"avg_sources_30d"`
	CreatedAt          time.Time `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time `db:"updated_at" json:"updated_at"`
}
