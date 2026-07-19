package outcomes

import "time"

// RegimeSegment maps a regime_label to its regime_bucket.
// Used for regime-conditioned sizing and performance stratification.
type RegimeSegment string

// RegimeSegment constants.
const (
	RegimeRiskOn  RegimeSegment = "risk_on"
	RegimeRiskOff RegimeSegment = "risk_off"
	RegimeUnknown RegimeSegment = "unknown"
)

// SegmentRegime maps a regime label string to a RegimeSegment.
// strong_bull and bull → risk_on
// neutral, correction, bear → risk_off
// anything else → unknown
func SegmentRegime(regimeLabel string) RegimeSegment {
	switch regimeLabel {
	case "strong_bull", "bull":
		return RegimeRiskOn
	case "neutral", "correction", "bear":
		return RegimeRiskOff
	default:
		return RegimeUnknown
	}
}

// StressWindow defines a named historical period for multi-regime analysis.
type StressWindow struct {
	Name      string
	StartDate time.Time
	EndDate   time.Time
	Character string
}

// DefaultStressWindows returns the standard stress windows for multi-regime analysis.
// These cover bear, recovery, bull, and correction periods from available history.
func DefaultStressWindows() []StressWindow {
	return []StressWindow{
		{
			Name:      "2022 bear",
			StartDate: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2022, 12, 31, 0, 0, 0, 0, time.UTC),
			Character: "Sustained bear, momentum crash, multiple regime transitions",
		},
		{
			Name:      "2022Q4 recovery",
			StartDate: time.Date(2022, 10, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2022, 12, 31, 0, 0, 0, 0, time.UTC),
			Character: "Bear-to-neutral transition",
		},
		{
			Name:      "2023 bull",
			StartDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			Character: "Sustained bull (similar to current sample)",
		},
		{
			Name:      "2024 correction",
			StartDate: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC),
			Character: "Short correction (~10%) within bull",
		},
		{
			Name:      "2025 bear",
			StartDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			Character: "Check candle history for any correction/bear periods",
		},
	}
}

// BreakevenBps computes the breakeven cost in basis points given a gross return.
// breakeven_bps = gross_return * 10000 rounded to nearest whole bps.
// Returns nil if grossReturn is nil.
func BreakevenBps(grossReturn *float64) *float64 {
	if grossReturn == nil {
		return nil
	}
	be := *grossReturn * 10000
	// round to nearest integer
	rounded := float64(int(be + 0.5))
	return &rounded
}
