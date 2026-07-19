package ranking

import (
	"math"
	"testing"

	"ai-stock-service/internal/models"
)

func TestComputeTrendConsistency_HighRSINotPenalised(t *testing.T) {
	tests := []struct {
		name    string
		rsi     float64
		dist52w float64
		wantMin float64
	}{
		{"RSI 75 near high", 75, -2.0, 0.80},
		{"RSI 82 near high", 82, -1.0, 0.90},
		{"RSI 90 near high", 90, -1.0, 0.65},
		{"RSI 45 far from high", 45, -20.0, 0.10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := models.TradingViewSnapshotDaily{
				RSI14:           new(tt.rsi),
				Distance52wHigh: new(tt.dist52w),
			}
			got := computeTrendConsistency(&snap)
			if got < tt.wantMin {
				t.Errorf("got %.3f, want >= %.3f", got, tt.wantMin)
			}
		})
	}
}

func TestComputeVolumePriceConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		relVol    float64
		changePct float64
		gapPct    float64
		wantMin   float64
		wantMax   float64
	}{
		{"3x volume, +4% change, gap up", 3.0, 4.0, 1.5, 0.45, 1.0},
		{"3x volume, -4% change, gap down", 3.0, -4.0, -2.0, 0.0, 0.05},
		{"1x volume, +1% change", 1.0, 1.0, 0.5, 0.05, 0.20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := models.TradingViewSnapshotDaily{
				RelativeVolume: &tt.relVol,
				ChangePct:      &tt.changePct,
				GapPct:         &tt.gapPct,
			}
			got := computeVolumePriceConfirmation(&snap)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("got %.3f, want [%.3f, %.3f]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestComputeRelativeStrength_PiecewiseMapping(t *testing.T) {
	tests := []struct {
		perf3m, perf6m float64
		wantApprox     float64
		tolerance      float64
	}{
		{0, 0, 0.20, 0.02},     // zero performance → 0.2
		{30, 30, 0.60, 0.02},   // +30% → 0.6
		{60, 60, 0.80, 0.05},   // +60% → ~0.8
		{-20, -20, 0.00, 0.02}, // -20% → 0.0
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			snap := models.TradingViewSnapshotDaily{
				Perf3M: &tt.perf3m,
				Perf6M: &tt.perf6m,
			}
			got := computeRelativeStrength(&snap)
			if math.Abs(got-tt.wantApprox) > tt.tolerance {
				t.Errorf("perf3m=%.0f perf6m=%.0f: got %.3f, want ≈%.3f (±%.2f)",
					tt.perf3m, tt.perf6m, got, tt.wantApprox, tt.tolerance)
			}
		})
	}
}
