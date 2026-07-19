package indicators_test

import (
	"fmt"
	"testing"
	"time"

	"ai-stock-service/internal/indicators"
	"ai-stock-service/internal/models"
)

// makeCandle is a minimal CandleDaily builder for distribution-day tests.
func makeCandle(date string, c float64, volume int64) models.CandleDaily {
	t, _ := time.Parse("2006-01-02", date)
	return models.CandleDaily{
		Date:   t,
		Close:  c,
		Volume: volume,
	}
}

func TestCountDistributionDays(t *testing.T) {
	tests := []struct {
		name     string
		candles  []models.CandleDaily
		lookback int
		want     int
	}{
		{
			name:     "nil candles returns 0",
			candles:  nil,
			lookback: 20,
			want:     0,
		},
		{
			name:     "single candle returns 0",
			candles:  []models.CandleDaily{makeCandle("2024-01-01", 100, 1_000_000)},
			lookback: 20,
			want:     0,
		},
		{
			name: "lookback zero returns 0",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 99, 1_100_000),
			},
			lookback: 0,
			want:     0,
		},
		{
			name: "no distribution days — all up sessions",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 101, 1_100_000),
				makeCandle("2024-01-03", 102, 1_200_000),
			},
			lookback: 20,
			want:     0,
		},
		{
			name: "no distribution days — close down but volume also lower",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 99, 900_000), // close↓ volume↓ → NOT a dist day
			},
			lookback: 20,
			want:     0,
		},
		{
			name: "one distribution day",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 99, 1_100_000),  // close↓ volume↑ → distribution
				makeCandle("2024-01-03", 101, 1_200_000), // close↑ → not distribution
			},
			lookback: 20,
			want:     1,
		},
		{
			name: "multiple distribution days",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 99, 1_100_000),  // dist
				makeCandle("2024-01-03", 101, 1_200_000), // not dist (up)
				makeCandle("2024-01-04", 100, 1_300_000), // dist
				makeCandle("2024-01-05", 98, 1_400_000),  // dist
			},
			lookback: 20,
			want:     3,
		},
		{
			name: "close equal to previous — not a distribution day",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 100, 1_100_000), // flat close → not dist
			},
			lookback: 20,
			want:     0,
		},
		{
			name: "lookback window excludes distribution day older than 20 sessions",
			candles: func() []models.CandleDaily {
				// 22 candles total.
				// candles[0] and candles[1] sit outside the 20-session window.
				// The distribution day is at candles[1] — it must NOT be counted.
				// candles[2..21] are all up-sessions.
				out := []models.CandleDaily{
					makeCandle("2024-01-01", 100, 1_000_000),
					makeCandle("2024-01-02", 99, 1_100_000), // dist day — outside window
				}
				c := 100.0
				vol := int64(1_000_000)
				for i := 0; i < 20; i++ {
					c += 1.0
					vol += 100_000
					date := fmt.Sprintf("2024-01-%02d", 3+i)
					out = append(out, makeCandle(date, c, vol))
				}
				return out
			}(),
			lookback: 20,
			want:     0,
		},
		{
			name: "fewer candles than lookback counts what is available",
			candles: []models.CandleDaily{
				makeCandle("2024-01-01", 100, 1_000_000),
				makeCandle("2024-01-02", 99, 1_100_000), // dist
				makeCandle("2024-01-03", 98, 1_200_000), // dist
			},
			lookback: 20, // only 3 candles available — 2 pairs checked
			want:     2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := indicators.CountDistributionDays(tc.candles, tc.lookback)
			if got != tc.want {
				t.Errorf("CountDistributionDays: got %d, want %d", got, tc.want)
			}
		})
	}
}
