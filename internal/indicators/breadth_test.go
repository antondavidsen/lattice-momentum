package indicators_test

import (
	"math"
	"testing"
	"time"

	"ai-stock-service/internal/indicators"
	"ai-stock-service/internal/models"
)

// floatEqual returns true when a and b are within a small epsilon of each other.
// Used for percentage comparisons where IEEE-754 rounding can differ by 1 ULP.
func floatEqual(a, b float64) bool {
	const eps = 1e-9
	return math.Abs(a-b) <= eps
}

// makeCandleSeq builds a slice of daily candles from a list of closing prices.
// Dates start on 2024-01-01 and increment by one calendar day per element.
func makeCandleSeq(closes []float64) []models.CandleDaily {
	out := make([]models.CandleDaily, len(closes))
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = models.CandleDaily{
			Date:   base.AddDate(0, 0, i),
			Close:  c,
			Volume: 1_000_000,
		}
	}
	return out
}

func TestCalculateBreadth(t *testing.T) {
	tests := []struct {
		name         string
		stockCandles map[string][]models.CandleDaily
		period       int
		want         float64
	}{
		{
			name:         "empty map returns 0",
			stockCandles: map[string][]models.CandleDaily{},
			period:       50,
			want:         0,
		},
		{
			name: "period zero returns 0",
			stockCandles: map[string][]models.CandleDaily{
				"AAPL": makeCandleSeq([]float64{100, 101, 102}),
			},
			period: 0,
			want:   0,
		},
		{
			name: "period negative returns 0",
			stockCandles: map[string][]models.CandleDaily{
				"AAPL": makeCandleSeq([]float64{100, 101, 102}),
			},
			period: -1,
			want:   0,
		},
		{
			name: "all stocks above their SMA returns 100",
			stockCandles: map[string][]models.CandleDaily{
				// SMA(3) of [1,2,3] = 2.0;  latest close = 3 > 2 → above
				"AAPL": makeCandleSeq([]float64{1, 2, 3}),
				// SMA(3) of [10,20,30] = 20.0; latest close = 30 > 20 → above
				"MSFT": makeCandleSeq([]float64{10, 20, 30}),
			},
			period: 3,
			want:   100.0,
		},
		{
			name: "no stocks above their SMA returns 0",
			stockCandles: map[string][]models.CandleDaily{
				// SMA(3) of [3,2,1] = 2.0;  latest close = 1 < 2 → below
				"AAPL": makeCandleSeq([]float64{3, 2, 1}),
				// SMA(3) of [30,20,10] = 20.0; latest close = 10 < 20 → below
				"MSFT": makeCandleSeq([]float64{30, 20, 10}),
			},
			period: 3,
			want:   0.0,
		},
		{
			name: "half above SMA returns 50",
			stockCandles: map[string][]models.CandleDaily{
				// above: SMA(3)=2, latest=3
				"AAPL": makeCandleSeq([]float64{1, 2, 3}),
				// below: SMA(3)=2, latest=1
				"MSFT": makeCandleSeq([]float64{3, 2, 1}),
			},
			period: 3,
			want:   50.0,
		},
		{
			name: "ticker with fewer candles than period is excluded from result",
			stockCandles: map[string][]models.CandleDaily{
				// included — 3 candles ≥ period 3; SMA(3)=2, latest=3 → above
				"AAPL": makeCandleSeq([]float64{1, 2, 3}),
				// excluded — only 1 candle, period = 3
				"TINY": makeCandleSeq([]float64{999}),
			},
			period: 3,
			want:   100.0, // only AAPL counted → 1/1 = 100%
		},
		{
			name: "all tickers excluded due to insufficient history returns 0",
			stockCandles: map[string][]models.CandleDaily{
				"T1": makeCandleSeq([]float64{100}),
				"T2": makeCandleSeq([]float64{200}),
			},
			period: 50,
			want:   0.0,
		},
		{
			name: "exactly at SMA — not above — counts as below",
			stockCandles: map[string][]models.CandleDaily{
				// SMA(3) of [1,2,3] = 2.0; but we force latest == SMA
				"FLAT": makeCandleSeq([]float64{1, 3, 2}), // SMA(3)=2, latest=2 → not above
			},
			period: 3,
			want:   0.0,
		},
		{
			name: "three stocks 2 above 1 below — ~66.67%",
			stockCandles: map[string][]models.CandleDaily{
				"A": makeCandleSeq([]float64{1, 2, 3}), // above
				"B": makeCandleSeq([]float64{1, 2, 3}), // above
				"C": makeCandleSeq([]float64{3, 2, 1}), // below
			},
			period: 3,
			want:   200.0 / 3.0, // 2/3 * 100 ≈ 66.667
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := indicators.CalculateBreadth(tc.stockCandles, tc.period)
			if !floatEqual(got, tc.want) { //nolint:gocritic
				t.Errorf("CalculateBreadth: got %v, want %v", got, tc.want)
			}
		})
	}
}
