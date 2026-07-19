package ranking_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services/ranking"
)

// ── mocks ─────────────────────────────────────────────────────────────────────

type mockSnapshotSource struct {
	snaps []models.TradingViewSnapshotDaily
	err   error
}

func (m *mockSnapshotSource) ListByDate(_ context.Context, _ time.Time) ([]models.TradingViewSnapshotDaily, error) {
	return m.snaps, m.err
}

type mockRegimeSource struct {
	regime *models.MarketRegimeDaily
	err    error
}

func (m *mockRegimeSource) GetMarketRegimeDaily(_ context.Context, _ time.Time) (*models.MarketRegimeDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.regime, nil
}

type mockSectorScoreSource struct {
	scores []models.SectorScoreDaily
	err    error
}

func (m *mockSectorScoreSource) GetSectorScores(_ context.Context, _ time.Time) ([]models.SectorScoreDaily, error) {
	return m.scores, m.err
}

// mockNarrativeVelocitySource returns a fixed narrative velocity score per ticker.
type mockNarrativeVelocitySource struct {
	scores map[string]float64
	err    error
}

func (m *mockNarrativeVelocitySource) GetByTickerDate(_ context.Context, ticker string, _ time.Time) (*models.NarrativeVelocityDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	score, ok := m.scores[ticker]
	if !ok {
		return nil, nil //nolint:nilnil // mock ticker not found; return nil without error
	}
	return &models.NarrativeVelocityDaily{
		Ticker:            ticker,
		NarrativeVelocity: score,
	}, nil
}

// mockMomentumWeightsProvider returns default Momentum weights.
type mockMomentumWeightsProvider struct{}

func (m *mockMomentumWeightsProvider) GetActiveWeights(_ context.Context) (ranking.MomentumWeights, error) {
	return ranking.MomentumWeights{
		BreakoutStrength:        0.30,
		RelativeStrength:        0.25,
		VolumeExpansion:         0.10,
		VolumePriceConfirmation: 0.15,
		TrendConsistency:        0.20,
		RegimeMult:              1.0,
		SectorMult:              1.0,
	}, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ── helpers ───────────────────────────────────────────────────────────────────

func makeMomentumSnapshot(ticker string, c, relVol, perf3m, perf6m, dist52w, rsi float64) models.TradingViewSnapshotDaily {
	closeV := c
	relVolV := relVol
	perf3mV := perf3m
	perf6mV := perf6m
	dist52wV := dist52w
	rsiV := rsi
	sector := "Technology"
	volume := int64(1000000)
	avgVol := int64(200000)

	return models.TradingViewSnapshotDaily{
		Ticker:          ticker,
		ScreenerSource:  models.ScreenerMomentum,
		Close:           &closeV,
		RelativeVolume:  &relVolV,
		Perf3M:          &perf3mV,
		Perf6M:          &perf6mV,
		Distance52wHigh: &dist52wV,
		RSI14:           &rsiV,
		Sector:          &sector,
		Volume:          &volume,
		AvgVolume10d:    &avgVol, // ensures $10M+ at typical prices ($50+)
		RawJSON:         json.RawMessage(`{}`),
	}
}

func bullRegime() *models.MarketRegimeDaily {
	return &models.MarketRegimeDaily{Regime: "bull"}
}

func leadingSector() []models.SectorScoreDaily {
	return []models.SectorScoreDaily{
		{ETF: "XLK", Label: "LEADING"},
	}
}

// ── Momentum Engine tests ─────────────────────────────────────────────────────

func TestMomentumEngine_HappyPath(t *testing.T) {
	date := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)

	snaps := &mockSnapshotSource{snaps: []models.TradingViewSnapshotDaily{
		makeMomentumSnapshot("AAPL", 200, 2.0, 0.25, 0.30, -5, 65),
		makeMomentumSnapshot("MSFT", 400, 1.8, 0.20, 0.25, -8, 60),
		makeMomentumSnapshot("NFLX", 500, 3.0, 0.35, 0.40, -3, 70),
		makeMomentumSnapshot("SMCI", 80, 4.0, 0.40, 0.50, -2, 68),
		makeMomentumSnapshot("PLTR", 30, 2.5, 0.30, 0.35, -10, 55),
		makeMomentumSnapshot("JUNK", 150, 1.5, 0.10, 0.15, -20, 45),
	}}

	engine := ranking.NewMomentumEngine(snaps, &mockRegimeSource{regime: bullRegime()}, &mockSectorScoreSource{scores: leadingSector()}, &mockMomentumWeightsProvider{}, quietLogger())

	results, err := engine.Compute(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) > 10 {
		t.Errorf("expected at most 10 results, got %d", len(results))
	}

	// Verify sorted descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted descending at index %d", i)
		}
	}
}

func TestMomentumEngine_NarrativeVelocityBonus(t *testing.T) {
	date := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)

	// Two identical momentum snapshots; AAPL has narrative velocity data, MSFT does not.
	snaps := &mockSnapshotSource{snaps: []models.TradingViewSnapshotDaily{
		makeMomentumSnapshot("AAPL", 200, 2.0, 0.25, 0.30, -5, 65),
		makeMomentumSnapshot("MSFT", 200, 2.0, 0.25, 0.30, -5, 65),
	}}

	narrativeSrc := &mockNarrativeVelocitySource{
		scores: map[string]float64{"AAPL": 0.8},
	}

	engine := ranking.NewMomentumEngine(snaps, &mockRegimeSource{regime: bullRegime()}, &mockSectorScoreSource{}, &mockMomentumWeightsProvider{}, quietLogger())
	engine.WithNarrativeVelocity(narrativeSrc)

	results, err := engine.Compute(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// AAPL should rank first due to narrative velocity bonus on breakout strength.
	if results[0].Ticker != "AAPL" {
		t.Errorf("expected AAPL first (with narrative velocity bonus), got %s", results[0].Ticker)
	}

	// Verify narrative_velocity in Reason map.
	if got := results[0].Reason["narrative_velocity"]; got != 0.8 {
		t.Errorf("expected AAPL narrative_velocity=0.8, got %.2f", got)
	}
	if got := results[1].Reason["narrative_velocity"]; got != 0.0 {
		t.Errorf("expected MSFT narrative_velocity=0.0 (no data), got %.2f", got)
	}

	// AAPL score must be strictly higher than MSFT (identical except narrative bonus).
	if results[0].Score <= results[1].Score {
		t.Errorf("expected AAPL score (%.2f) > MSFT score (%.2f) due to narrative velocity bonus",
			results[0].Score, results[1].Score)
	}
}
