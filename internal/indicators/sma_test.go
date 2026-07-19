package indicators_test

import (
	"testing"

	"ai-stock-service/internal/indicators"
)

func TestComputeSMA(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		period int
		want   float64
	}{
		{
			name:   "nil slice returns 0",
			values: nil,
			period: 5,
			want:   0,
		},
		{
			name:   "empty slice returns 0",
			values: []float64{},
			period: 5,
			want:   0,
		},
		{
			name:   "period zero returns 0",
			values: []float64{1, 2, 3},
			period: 0,
			want:   0,
		},
		{
			name:   "period negative returns 0",
			values: []float64{1, 2, 3},
			period: -1,
			want:   0,
		},
		{
			name:   "fewer values than period returns 0",
			values: []float64{1, 2, 3},
			period: 5,
			want:   0,
		},
		{
			name:   "exactly period values returns mean",
			values: []float64{2, 4, 6, 8, 10},
			period: 5,
			want:   6, // (2+4+6+8+10)/5
		},
		{
			name:   "more values than period uses last N",
			values: []float64{100, 1, 2, 3, 4, 5},
			period: 5,
			want:   3, // mean of [1,2,3,4,5]
		},
		{
			name:   "period 1 returns last value",
			values: []float64{10, 20, 30},
			period: 1,
			want:   30,
		},
		{
			name: "SMA-50 over 1..50 equals 25.5",
			values: func() []float64 {
				v := make([]float64, 50)
				for i := range v {
					v[i] = float64(i + 1) // 1, 2, …, 50
				}
				return v
			}(),
			period: 50,
			want:   25.5,
		},
		{
			name:   "SMA-3 picks last 3 elements only",
			values: []float64{1000, 1000, 1000, 1000, 3, 6, 9},
			period: 3,
			want:   6, // mean of [3,6,9]
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := indicators.ComputeSMA(tc.values, tc.period)
			if got != tc.want {
				t.Errorf("ComputeSMA(%v, %d) = %v; want %v",
					tc.values, tc.period, got, tc.want)
			}
		})
	}
}
