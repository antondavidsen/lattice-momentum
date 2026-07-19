package sector_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services/sector"
)

// ── mock repo ─────────────────────────────────────────────────────────────────

type mockSectorData struct {
	indices map[string][]models.CandleDaily
	err     error
}

func (m *mockSectorData) GetIndexHistory(_ context.Context, ticker string, _ int) ([]models.CandleDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.indices[ticker], nil
}

// ── candle builders ───────────────────────────────────────────────────────────

func buildCandles(n int, c float64) []models.CandleDaily {
	out := make([]models.CandleDaily, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = models.CandleDaily{
			Date:   base.AddDate(0, 0, i),
			Close:  c,
			Volume: 1_000_000,
		}
	}
	return out
}

func buildCandlesLinear(n int, start, end float64) []models.CandleDaily {
	out := make([]models.CandleDaily, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	step := (end - start) / float64(n-1)
	for i := range out {
		out[i] = models.CandleDaily{
			Date:   base.AddDate(0, 0, i),
			Close:  start + float64(i)*step,
			Volume: 1_000_000,
		}
	}
	return out
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// buildMockIndices builds a mock with SPY + all SectorETFs using the same candles.
func buildMockIndices(candles []models.CandleDaily) map[string][]models.CandleDaily {
	m := map[string][]models.CandleDaily{"SPY": candles}
	for _, etf := range models.SectorETFs {
		m[etf] = candles
	}
	return m
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestBuildSectorScores_AllFlat(t *testing.T) {
	// All ETFs + SPY flat at 100 → perf_1m = perf_3m = rs_vs_spy_3m = 0.
	flat := buildCandles(250, 100)
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: buildMockIndices(flat)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != len(models.SectorETFs) {
		t.Fatalf("got %d scores, want %d", len(scores), len(models.SectorETFs))
	}

	for _, s := range scores {
		if s.Perf1M != 0 {
			t.Errorf("%s: Perf1M = %v, want 0", s.ETF, s.Perf1M)
		}
		if s.Perf3M != 0 {
			t.Errorf("%s: Perf3M = %v, want 0", s.ETF, s.Perf3M)
		}
		if s.RSvsSPY3M != 0 {
			t.Errorf("%s: RSvsSPY3M = %v, want 0", s.ETF, s.RSvsSPY3M)
		}
	}
}

func TestBuildSectorScores_RisingAboveSMA(t *testing.T) {
	// Rising series: latest close above SMA-50 and SMA-200.
	rising := buildCandlesLinear(250, 100, 500)
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: buildMockIndices(rising)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range scores {
		if !s.AboveSMA50 {
			t.Errorf("%s: AboveSMA50 expected true for rising series", s.ETF)
		}
		if !s.AboveSMA200 {
			t.Errorf("%s: AboveSMA200 expected true for rising series", s.ETF)
		}
		if s.TrendScore != 1.0 {
			t.Errorf("%s: TrendScore = %v, want 1.0", s.ETF, s.TrendScore)
		}
	}
}

func TestBuildSectorScores_FallingBelowSMA(t *testing.T) {
	// Falling series: latest close below SMA-50 and SMA-200.
	falling := buildCandlesLinear(250, 500, 100)
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: buildMockIndices(falling)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range scores {
		if s.AboveSMA50 {
			t.Errorf("%s: AboveSMA50 expected false for falling series", s.ETF)
		}
		if s.AboveSMA200 {
			t.Errorf("%s: AboveSMA200 expected false for falling series", s.ETF)
		}
		if s.TrendScore != 0.0 {
			t.Errorf("%s: TrendScore = %v, want 0.0", s.ETF, s.TrendScore)
		}
	}
}

func TestBuildSectorScores_LabelAssignment(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.95, models.SectorLabelLeading},
		{0.80, models.SectorLabelLeading},
		{0.70, models.SectorLabelStrong},
		{0.60, models.SectorLabelStrong},
		{0.50, models.SectorLabelNeutral},
		{0.40, models.SectorLabelNeutral},
		{0.30, models.SectorLabelWeak},
		{0.20, models.SectorLabelWeak},
		{0.10, models.SectorLabelLagging},
		{0.00, models.SectorLabelLagging},
	}
	for _, tc := range tests {
		got := models.SectorScoreLabel(tc.score)
		if got != tc.want {
			t.Errorf("SectorScoreLabel(%.2f) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestBuildSectorScores_Ranking(t *testing.T) {
	// Build a mock where XLK is strongly rising and all others are flat.
	// XLK should be ranked highest.
	flat := buildCandles(250, 100)
	rising := buildCandlesLinear(250, 100, 300)

	indices := map[string][]models.CandleDaily{"SPY": flat}
	for _, etf := range models.SectorETFs {
		if etf == "XLK" {
			indices[etf] = rising
		} else {
			indices[etf] = flat
		}
	}

	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: indices},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// scores are sorted by score descending — first should be XLK.
	if scores[0].ETF != "XLK" {
		t.Errorf("expected XLK to be top-ranked, got %s (score=%.4f)", scores[0].ETF, scores[0].Score)
	}
	if scores[0].Score <= scores[len(scores)-1].Score {
		t.Error("top score should be > bottom score")
	}
}

func TestBuildSectorScores_ScoresClamped(t *testing.T) {
	// Verify all scores are in [0, 1].
	flat := buildCandles(250, 100)
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: buildMockIndices(flat)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range scores {
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("%s: Score = %v, want [0, 1]", s.ETF, s.Score)
		}
	}
}

func TestBuildSectorScores_InsufficientSPYHistory(t *testing.T) {
	short := buildCandles(50, 100)
	indices := map[string][]models.CandleDaily{"SPY": short}
	for _, etf := range models.SectorETFs {
		indices[etf] = buildCandles(250, 100)
	}

	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: indices},
		quietLogger(),
	)

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for insufficient SPY history, got nil")
	}
}

func TestBuildSectorScores_InsufficientETFHistory(t *testing.T) {
	indices := map[string][]models.CandleDaily{"SPY": buildCandles(250, 100)}
	for _, etf := range models.SectorETFs {
		if etf == "XLE" {
			indices[etf] = buildCandles(50, 100) // too short
		} else {
			indices[etf] = buildCandles(250, 100)
		}
	}

	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: indices},
		quietLogger(),
	)

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for insufficient XLE history, got nil")
	}
}

func TestBuildSectorScores_PropagatesRepoError(t *testing.T) {
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{err: fmt.Errorf("database unavailable")},
		quietLogger(),
	)

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error when repo fails, got nil")
	}
}

func TestBuildSectorScores_DatePreserved(t *testing.T) {
	flat := buildCandles(250, 100)
	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: buildMockIndices(flat)},
		quietLogger(),
	)

	want := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	scores, err := svc.BuildSectorScores(context.Background(), want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range scores {
		if !s.Date.Equal(want) {
			t.Errorf("%s: Date = %v, want %v", s.ETF, s.Date, want)
		}
	}
}

func TestBuildSectorScores_TieAwareRanking(t *testing.T) {
	// Two ETFs with identical perf_3m values should receive identical
	// percentile ranks (tie-aware ranking via indicators.PercentileRanks).
	//
	// Build a mock where XLF and XLI have identical rising series,
	// XLK is strongly rising (should be top), and all others are flat.
	flat := buildCandles(250, 100)
	rising := buildCandlesLinear(250, 100, 300)
	tiedRising := buildCandlesLinear(250, 100, 200) // same for both tied ETFs

	indices := map[string][]models.CandleDaily{"SPY": flat}
	for _, etf := range models.SectorETFs {
		switch etf {
		case "XLK":
			indices[etf] = rising
		case "XLF", "XLI":
			indices[etf] = tiedRising
		default:
			indices[etf] = flat
		}
	}

	svc := sector.NewMomentumServiceFromSource(
		&mockSectorData{indices: indices},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find XLF and XLI scores
	var xlfScore, xliScore float64
	for _, s := range scores {
		if s.ETF == "XLF" {
			xlfScore = s.Score
		}
		if s.ETF == "XLI" {
			xliScore = s.Score
		}
	}

	// They should have identical scores due to tie-aware ranking
	if xlfScore != xliScore {
		t.Errorf("Tied ETFs should have identical scores: XLF=%.6f, XLI=%.6f", xlfScore, xliScore)
	}
}
