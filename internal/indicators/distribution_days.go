package indicators

import "ai-stock-service/internal/models"

// CountDistributionDays counts distribution days within the last `lookback`
// candles of the provided slice.
//
// IBD definition — a distribution day occurs when:
//   - The index closes LOWER than the previous session's close, AND
//   - Volume is HIGHER than the previous session's volume.
//
// The standard lookback used in IBD analysis is 20 trading sessions.
//
// The candles slice must be sorted ascending by date (oldest → newest).
// Returns 0 when lookback <= 0 or fewer than 2 candles are provided.
func CountDistributionDays(candles []models.CandleDaily, lookback int) int {
	if lookback <= 0 || len(candles) < 2 {
		return 0
	}

	// We want to examine the last `lookback` candles as distribution-day
	// candidates. Each candidate requires comparison to its predecessor, so
	// the window starts one element earlier to always supply a "previous" day.
	start := len(candles) - lookback - 1
	if start < 0 {
		start = 0
	}
	window := candles[start:]

	count := 0
	for i := 1; i < len(window); i++ {
		prev, curr := window[i-1], window[i]
		if curr.Close < prev.Close && curr.Volume > prev.Volume {
			count++
		}
	}
	return count
}
