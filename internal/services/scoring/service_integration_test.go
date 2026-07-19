package scoring_test

// Integration-level tests for ScoringService.BuildSectorScores.
//
// These tests exercise the full compute pipeline end-to-end:
//   mock data source → loadSectorHistories → computeRawMetrics →
//   buildFinalScores → returned snapshot.
//
// They use deterministic synthetic candles so results are reproducible.
// No external APIs or live database connections are required.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services/scoring"
)

// ── mock data source ──────────────────────────────────────────────────────────

type mockScoringData struct {
	indices map[string][]models.CandleDaily
	err     error
}

func (m *mockScoringData) GetIndexHistory(_ context.Context, ticker string, _ int) ([]models.CandleDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.indices[ticker], nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// buildTrendCandles generates deterministic candles with compound daily returns.
//
//	buildTrendCandles(250, 100, 0.20)  → 0.2% daily uptrend
//	buildTrendCandles(250, 100, -0.15) → 0.15% daily downtrend
//	buildTrendCandles(250, 100, 0.0)   → flat at 100
func buildTrendCandles(days int, start, dailyPct float64) []models.CandleDaily {
	out := make([]models.CandleDaily, days)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	price := start
	for i := range out {
		out[i] = models.CandleDaily{
			Date:   base.AddDate(0, 0, i),
			Close:  price,
			Volume: 1_000_000,
		}
		price *= (1 + dailyPct/100)
	}
	return out
}

// buildFlatCandles generates `n` candles all at the same close price.
func buildFlatCandles(n int, c float64) []models.CandleDaily {
	return buildTrendCandles(n, c, 0.0)
}

// buildAllIndices builds a mock with SPY + all scoring.SectorETFs using the
// specified candle data for each.
func buildAllIndices(spy []models.CandleDaily, etfCandles map[string][]models.CandleDaily) map[string][]models.CandleDaily {
	m := map[string][]models.CandleDaily{"SPY": spy}
	for _, etf := range models.SectorETFs {
		if c, ok := etfCandles[etf]; ok {
			m[etf] = c
		}
	}
	return m
}

// buildUniformIndices builds a mock where SPY + all SectorETFs share the same candles.
func buildUniformIndices(candles []models.CandleDaily) map[string][]models.CandleDaily {
	m := map[string][]models.CandleDaily{"SPY": candles}
	for _, etf := range models.SectorETFs {
		m[etf] = candles
	}
	return m
}

// ── TestScoringService_BuildSectorScores_FullPipeline ─────────────────────────
// Insert 250 days of synthetic candles for SPY + all SectorETFs.
// Call BuildSectorScores and verify the snapshot.

func TestScoringService_BuildSectorScores_FullPipeline(t *testing.T) {
	// Create a scenario with clear differentiation:
	//   XLK: strong uptrend (0.20% daily)  → should be leader
	//   XLE: moderate uptrend (0.10% daily) → middle
	//   XLU: downtrend (-0.15% daily)       → should be laggard
	//   all others: flat at 150
	flat := buildFlatCandles(250, 150)
	etfCandles := make(map[string][]models.CandleDaily)
	for _, etf := range models.SectorETFs {
		switch etf {
		case "XLK":
			etfCandles[etf] = buildTrendCandles(250, 100, 0.20) // strong up
		case "XLE":
			etfCandles[etf] = buildTrendCandles(250, 100, 0.10) // moderate up
		case "XLU":
			etfCandles[etf] = buildTrendCandles(250, 150, -0.15) // down
		default:
			etfCandles[etf] = flat
		}
	}

	indices := buildAllIndices(flat, etfCandles)
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{indices: indices},
		quietLogger(),
	)

	date := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	scores, err := svc.BuildSectorScores(context.Background(), date)
	if err != nil {
		t.Fatalf("BuildSectorScores: unexpected error: %v", err)
	}

	// ── row count == len(SectorETFs) ──────────────────────────────────────
	if len(scores) != len(models.SectorETFs) {
		t.Fatalf("row count = %d, want %d", len(scores), len(models.SectorETFs))
	}

	// ── scores are non-zero (at least one sector must differ from others) ─
	hasNonZero := false
	for _, s := range scores {
		if s.Score > 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("all scores are 0 — expected at least one non-zero score")
	}

	// ── labels not empty ──────────────────────────────────────────────────
	validLabels := map[string]bool{
		models.SectorLabelLeading: true,
		models.SectorLabelStrong:  true,
		models.SectorLabelNeutral: true,
		models.SectorLabelWeak:    true,
		models.SectorLabelLagging: true,
	}
	for _, s := range scores {
		if s.Label == "" {
			t.Errorf("%s: label is empty", s.ETF)
		}
		if !validLabels[s.Label] {
			t.Errorf("%s: label %q is not a valid sector label", s.ETF, s.Label)
		}
	}

	// ── date == requested date ────────────────────────────────────────────
	for _, s := range scores {
		if !s.Date.Equal(date) {
			t.Errorf("%s: Date = %v, want %v", s.ETF, s.Date, date)
		}
	}

	// ── all scores in [0, 1] ──────────────────────────────────────────────
	for _, s := range scores {
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("%s: Score = %v, out of [0, 1]", s.ETF, s.Score)
		}
	}

	// ── ranking correctness: XLK on top, XLU at bottom ────────────────────
	if scores[0].ETF != "XLK" {
		t.Errorf("expected leader = XLK, got %s (score=%.4f)", scores[0].ETF, scores[0].Score)
	}
	if scores[len(scores)-1].ETF != "XLU" {
		t.Errorf("expected laggard = XLU, got %s (score=%.4f)",
			scores[len(scores)-1].ETF, scores[len(scores)-1].Score)
	}

	// ── sorted descending ─────────────────────────────────────────────────
	for i := 0; i < len(scores)-1; i++ {
		if scores[i].Score < scores[i+1].Score {
			t.Errorf("scores not sorted descending: [%d]=%s (%.4f) < [%d]=%s (%.4f)",
				i, scores[i].ETF, scores[i].Score,
				i+1, scores[i+1].ETF, scores[i+1].Score)
		}
	}

	// ── XLK metrics sanity: positive perf, above SMAs, LEADING label ──────
	xlk := scores[0]
	if xlk.Perf1M <= 0 {
		t.Errorf("XLK Perf1M = %v, expected > 0", xlk.Perf1M)
	}
	if xlk.Perf3M <= 0 {
		t.Errorf("XLK Perf3M = %v, expected > 0", xlk.Perf3M)
	}
	if xlk.RSvsSPY3M <= 0 {
		t.Errorf("XLK RSvsSPY3M = %v, expected > 0 (outperforming flat SPY)", xlk.RSvsSPY3M)
	}
	if !xlk.AboveSMA50 {
		t.Error("XLK: expected AboveSMA50 = true")
	}
	if !xlk.AboveSMA200 {
		t.Error("XLK: expected AboveSMA200 = true")
	}
	if xlk.Label != models.SectorLabelLeading {
		t.Errorf("XLK label = %s, want LEADING", xlk.Label)
	}

	// ── XLU metrics sanity: negative perf, below SMAs, LAGGING label ──────
	xlu := scores[len(scores)-1]
	if xlu.Perf1M >= 0 {
		t.Errorf("XLU Perf1M = %v, expected < 0", xlu.Perf1M)
	}
	if xlu.AboveSMA50 {
		t.Error("XLU: expected AboveSMA50 = false")
	}
	if xlu.Label != models.SectorLabelLagging {
		t.Errorf("XLU label = %s, want LAGGING", xlu.Label)
	}
}

// ── TestScoringService_AllFlat ────────────────────────────────────────────────
// When all sectors and SPY are flat, perf and RS should be zero.

func TestScoringService_AllFlat(t *testing.T) {
	flat := buildFlatCandles(250, 100)
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{indices: buildUniformIndices(flat)},
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

// ── TestScoringService_RisingAboveSMA ─────────────────────────────────────────

func TestScoringService_RisingAboveSMA(t *testing.T) {
	// Steady uptrend → latest close above both SMA-50 and SMA-200.
	rising := buildTrendCandles(250, 100, 0.30)
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{indices: buildUniformIndices(rising)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range scores {
		if !s.AboveSMA50 {
			t.Errorf("%s: AboveSMA50 expected true for uptrend", s.ETF)
		}
		if !s.AboveSMA200 {
			t.Errorf("%s: AboveSMA200 expected true for uptrend", s.ETF)
		}
		if s.TrendScore != 1.0 {
			t.Errorf("%s: TrendScore = %v, want 1.0", s.ETF, s.TrendScore)
		}
	}
}

// ── TestScoringService_FallingBelowSMA ────────────────────────────────────────

func TestScoringService_FallingBelowSMA(t *testing.T) {
	// Steady downtrend → latest close below both SMA-50 and SMA-200.
	falling := buildTrendCandles(250, 200, -0.20)
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{indices: buildUniformIndices(falling)},
		quietLogger(),
	)

	scores, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range scores {
		if s.AboveSMA50 {
			t.Errorf("%s: AboveSMA50 expected false for downtrend", s.ETF)
		}
		if s.AboveSMA200 {
			t.Errorf("%s: AboveSMA200 expected false for downtrend", s.ETF)
		}
		if s.TrendScore != 0.0 {
			t.Errorf("%s: TrendScore = %v, want 0.0", s.ETF, s.TrendScore)
		}
	}
}

// ── TestScoringService_StableRankingWithRepeat ────────────────────────────────
// Run the same inputs twice and verify scores are identical (determinism).

func TestScoringService_StableRankingWithRepeat(t *testing.T) {
	flat := buildFlatCandles(250, 150)
	etfCandles := make(map[string][]models.CandleDaily)
	for _, etf := range models.SectorETFs {
		if etf == "XLK" {
			etfCandles[etf] = buildTrendCandles(250, 100, 0.15)
		} else {
			etfCandles[etf] = flat
		}
	}
	indices := buildAllIndices(flat, etfCandles)

	date := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)

	// Run 1.
	svc1 := scoring.NewScoringServiceFromSource(&mockScoringData{indices: indices}, quietLogger())
	scores1, err := svc1.BuildSectorScores(context.Background(), date)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}

	// Run 2.
	svc2 := scoring.NewScoringServiceFromSource(&mockScoringData{indices: indices}, quietLogger())
	scores2, err := svc2.BuildSectorScores(context.Background(), date)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	// Scores must be identical.
	if len(scores1) != len(scores2) {
		t.Fatalf("length mismatch: %d vs %d", len(scores1), len(scores2))
	}
	for i := range scores1 {
		if scores1[i].ETF != scores2[i].ETF {
			t.Errorf("[%d]: ETF %s vs %s", i, scores1[i].ETF, scores2[i].ETF)
		}
		if scores1[i].Score != scores2[i].Score {
			t.Errorf("[%d] %s: Score %.6f vs %.6f", i, scores1[i].ETF, scores1[i].Score, scores2[i].Score)
		}
		if scores1[i].Label != scores2[i].Label {
			t.Errorf("[%d] %s: Label %s vs %s", i, scores1[i].ETF, scores1[i].Label, scores2[i].Label)
		}
	}
}

// ── Error propagation ─────────────────────────────────────────────────────────

func TestScoringService_InsufficientSPYHistory(t *testing.T) {
	short := buildFlatCandles(50, 100) // only 50 bars, need ≥ 200
	indices := map[string][]models.CandleDaily{"SPY": short}
	for _, etf := range models.SectorETFs {
		indices[etf] = buildFlatCandles(250, 100)
	}

	svc := scoring.NewScoringServiceFromSource(&mockScoringData{indices: indices}, quietLogger())

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for insufficient SPY history, got nil")
	}
}

func TestScoringService_InsufficientETFHistory(t *testing.T) {
	indices := map[string][]models.CandleDaily{"SPY": buildFlatCandles(250, 100)}
	for _, etf := range models.SectorETFs {
		if etf == "XLE" {
			indices[etf] = buildFlatCandles(50, 100) // too short
		} else {
			indices[etf] = buildFlatCandles(250, 100)
		}
	}

	svc := scoring.NewScoringServiceFromSource(&mockScoringData{indices: indices}, quietLogger())

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for insufficient XLE history, got nil")
	}
}

func TestScoringService_PropagatesRepoError(t *testing.T) {
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{err: fmt.Errorf("database unavailable")},
		quietLogger(),
	)

	_, err := svc.BuildSectorScores(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error when repo fails, got nil")
	}
}

func TestScoringService_DatePreserved(t *testing.T) {
	flat := buildFlatCandles(250, 100)
	svc := scoring.NewScoringServiceFromSource(
		&mockScoringData{indices: buildUniformIndices(flat)},
		quietLogger(),
	)

	want := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
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
