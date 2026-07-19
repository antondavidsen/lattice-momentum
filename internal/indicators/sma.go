// Package indicators provides pure-function technical-analysis calculations
// used by the Market Regime Engine and related services.
//
// All functions are stateless: they accept slices of primitives or model types
// and return computed values without side effects. This makes them trivially
// testable and safe to call from concurrent goroutines.
package indicators

// ComputeSMA returns the Simple Moving Average of the last `period` values in
// values.
//
// Returns 0 when:
//   - period <= 0
//   - len(values) < period
func ComputeSMA(values []float64, period int) float64 {
	if period <= 0 || len(values) < period {
		return 0
	}
	start := len(values) - period
	var sum float64
	for _, v := range values[start:] {
		sum += v
	}
	return sum / float64(period)
}
