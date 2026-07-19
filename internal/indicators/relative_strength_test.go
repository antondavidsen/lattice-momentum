package indicators_test

import (
	"testing"

	"ai-stock-service/internal/indicators"
)

func TestComputeRelativeStrength(t *testing.T) {
	tests := []struct {
		name    string
		seriesA []float64
		seriesB []float64
		want    []float64
	}{
		{
			name:    "both empty returns empty slice",
			seriesA: []float64{},
			seriesB: []float64{},
			want:    []float64{},
		},
		{
			name:    "equal length computes element-wise ratio",
			seriesA: []float64{200, 300, 400},
			seriesB: []float64{100, 150, 200},
			want:    []float64{2.0, 2.0, 2.0},
		},
		{
			name:    "seriesA shorter — output truncated to seriesA length",
			seriesA: []float64{10, 20},
			seriesB: []float64{5, 10, 15},
			want:    []float64{2.0, 2.0},
		},
		{
			name:    "seriesB shorter — output truncated to seriesB length",
			seriesA: []float64{10, 20, 30},
			seriesB: []float64{5, 10},
			want:    []float64{2.0, 2.0},
		},
		{
			name:    "zero in seriesB yields 0 for that element",
			seriesA: []float64{10, 20, 30},
			seriesB: []float64{5, 0, 10},
			want:    []float64{2.0, 0.0, 3.0},
		},
		{
			name:    "QQQ vs SPY — realistic price ratio",
			seriesA: []float64{350, 360, 370}, // QQQ
			seriesB: []float64{400, 410, 420}, // SPY
			want: []float64{
				350.0 / 400.0,
				360.0 / 410.0,
				370.0 / 420.0,
			},
		},
		{
			name:    "IWM vs SPY — small-cap underperformance",
			seriesA: []float64{180, 178, 175}, // IWM declining
			seriesB: []float64{450, 455, 460}, // SPY rising
			want: []float64{
				180.0 / 450.0,
				178.0 / 455.0,
				175.0 / 460.0,
			},
		},
		{
			name:    "values of 1 produce ratio 1",
			seriesA: []float64{1, 1, 1},
			seriesB: []float64{1, 1, 1},
			want:    []float64{1.0, 1.0, 1.0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := indicators.ComputeRelativeStrength(tc.seriesA, tc.seriesB)

			if len(got) != len(tc.want) {
				t.Fatalf("ComputeRelativeStrength len(got)=%d, len(want)=%d",
					len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
