package indicators

import "ai-stock-service/internal/models"

// CalculateBreadth returns the percentage (0–100) of stocks whose latest
// closing price is above the SMA(period).
//
// Typical usage:
//   - period = 50  → "% of stocks above 50-day SMA"
//   - period = 200 → "% of stocks above 200-day SMA"
//
// stockCandles must be a map of ticker → daily candles sorted ascending by
// date (oldest → newest). Any ticker whose history contains fewer than
// `period` candles is silently excluded from both the numerator and
// denominator so that thin histories do not skew the result.
//
// Returns 0 when stockCandles is empty, period <= 0, or every ticker is
// excluded due to insufficient history.
func CalculateBreadth(
	stockCandles map[string][]models.CandleDaily,
	period int,
) float64 {
	if len(stockCandles) == 0 || period <= 0 {
		return 0
	}

	total, above := 0, 0

	for _, candles := range stockCandles {
		if len(candles) < period {
			continue // not enough history to compute a meaningful SMA
		}

		closes := make([]float64, len(candles))
		for i := range candles {
			closes[i] = candles[i].Close
		}

		sma := ComputeSMA(closes, period)
		if closes[len(closes)-1] > sma {
			above++
		}
		total++
	}

	if total == 0 {
		return 0
	}
	return float64(above) / float64(total) * 100.0
}
