package models

import "time"

// Sector momentum labels assigned by the scoring engine.
const (
	SectorLabelLeading = "LEADING"
	SectorLabelStrong  = "STRONG"
	SectorLabelNeutral = "NEUTRAL"
	SectorLabelWeak    = "WEAK"
	SectorLabelLagging = "LAGGING"
)

// SectorScoreDaily is one row in the sector_scores_daily table.
// It stores the output of the Sector Momentum Scoring job for a single ETF on
// a given trading day.
type SectorScoreDaily struct {
	Date time.Time `db:"date"`
	ETF  string    `db:"etf"`

	// Raw performance metrics
	Perf1M    float64 `db:"perf_1m"`      // 21-session return
	Perf3M    float64 `db:"perf_3m"`      // 63-session return
	RSvsSPY3M float64 `db:"rs_vs_spy_3m"` // perf_3m - spy_perf_3m

	// Trend position
	AboveSMA50  bool `db:"above_sma50"`
	AboveSMA200 bool `db:"above_sma200"`

	// Computed scores
	TrendScore float64 `db:"trend_score"` // 0.0 | 0.6 | 1.0
	Score      float64 `db:"score"`       // weighted composite [0,1]
	Label      string  `db:"label"`       // LEADING | STRONG | NEUTRAL | WEAK | LAGGING

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// SectorScoreLabel returns the human-readable label for a composite score.
func SectorScoreLabel(score float64) string {
	switch {
	case score >= 0.80:
		return SectorLabelLeading
	case score >= 0.60:
		return SectorLabelStrong
	case score >= 0.40:
		return SectorLabelNeutral
	case score >= 0.20:
		return SectorLabelWeak
	default:
		return SectorLabelLagging
	}
}
