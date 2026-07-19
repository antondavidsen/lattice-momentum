// Package regime houses the Market Regime Engine's classification logic.
//
// This file defines the canonical set of regime label constants used by the
// classifier (Ticket 3.1) and by every downstream consumer (API, alerts, etc.).
//
// Labels are lowercase snake_case to match the CHECK constraint in the
// market_regime_daily table (migration 019).  Both must stay in sync — if you
// add a label here you must also extend the CHECK constraint.
package regime

// Label is a named string type for the five market regime states.
// Using a named type rather than plain string constants catches misuse at
// compile time and makes function signatures self-documenting.
type Label string

// The five regime labels produced by the classifier.
const (
	// RegimeStrongBull — broad-market uptrend, high breadth, indices above SMAs,
	// low distribution pressure.  Strongest risk-on state.
	RegimeStrongBull Label = "strong_bull"

	// RegimeBull — healthy uptrend with minor deterioration relative to
	// strong_bull (e.g., breadth starting to narrow or distribution days
	// beginning to accumulate, but primary trend still intact).
	RegimeBull Label = "bull"

	// RegimeNeutral — mixed signals: indices near SMAs, moderate breadth,
	// inconclusive rotation.  Neither clear risk-on nor risk-off.
	RegimeNeutral Label = "neutral"

	// RegimeCorrection — measurable weakness: indices below SMA-50 but above
	// SMA-200, elevated distribution days, breadth declining.
	// Risk reduction warranted; avoid new long positions.
	RegimeCorrection Label = "correction"

	// RegimeBear — primary downtrend: indices below both SMA-50 and SMA-200,
	// high distribution pressure, low breadth.  Maximum defensive posture.
	RegimeBear Label = "bear"
)

// Valid returns true when l is one of the five recognised regime labels.
// Use this to validate classifier output before persisting to the database.
func (l Label) Valid() bool {
	switch l {
	case RegimeStrongBull, RegimeBull, RegimeNeutral, RegimeCorrection, RegimeBear:
		return true
	}
	return false
}

// String returns the string representation of the label (satisfies fmt.Stringer).
func (l Label) String() string {
	return string(l)
}
