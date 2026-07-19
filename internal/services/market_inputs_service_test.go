package services_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services"
)

// ── mock repo ─────────────────────────────────────────────────────────────────

// mockMarketData implements the unexported marketDataSource interface by
// returning fixed candle slices supplied at construction time.
// It satisfies the interface because NewMarketInputsService accepts
// *repository.MarketDataRepo, but we wire our mock through
// newMarketInputsServiceForTest (see below).
type mockMarketData struct {
	indices map[string][]models.CandleDaily
	stocks  map[string][]models.CandleDaily
	err     error // if non-nil, all calls return this error
}

func (m *mockMarketData) GetIndexHistory(_ context.Context, ticker string, _ int) ([]models.CandleDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.indices[ticker], nil
}

func (m *mockMarketData) GetAllStocksHistory(_ context.Context, _ int) (map[string][]models.CandleDaily, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.stocks, nil
}

// newMarketInputsServiceForTest wires the mock through the internal interface
// using the exported constructor. We do this by embedding the mock in a thin
// wrapper that satisfies *repository.MarketDataRepo's interface — but since
// MarketInputsService stores the interface (not the concrete type) we can
// bypass the constructor and build the struct directly via a test helper.
//
// Because the marketDataSource field is unexported, we expose a package-level
// helper that accepts an interface value. The helper lives in the services
// package so it can access the unexported field.
func newTestService(mock *mockMarketData) *services.MarketInputsService {
	return services.NewMarketInputsServiceFromSource(mock, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

// ── candle builders ───────────────────────────────────────────────────────────

// buildCandles creates `n` candles with the given close price.
// Each candle increments by one day starting from 2024-01-01.
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

// buildCandlesLinear creates `n` candles with linearly increasing close prices
// from `start` to `end` (inclusive), sorted ascending by date.
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

// ── tests ─────────────────────────────────────────────────────────────────────

func TestBuildMarketInputs_SMAAbove(t *testing.T) {
	// SPY and QQQ linearly rising over 250 sessions: close starts well below
	// SMA range and ends higher → latest close > SMA-50 > SMA-200.
	rising := buildCandlesLinear(250, 100, 500) // 100 → 500

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": rising,
			"QQQ": rising,
			"IWM": rising,
		},
		stocks: map[string][]models.CandleDaily{},
	})

	inputs, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !inputs.SpyAbove50 {
		t.Error("SpyAbove50: expected true for rising SPY")
	}
	if !inputs.SpyAbove200 {
		t.Error("SpyAbove200: expected true for rising SPY")
	}
	if !inputs.QqqAbove50 {
		t.Error("QqqAbove50: expected true for rising QQQ")
	}
	if !inputs.QqqAbove200 {
		t.Error("QqqAbove200: expected true for rising QQQ")
	}
}

func TestBuildMarketInputs_SMABelow(t *testing.T) {
	// Falling series: latest close will be the minimum → below all SMAs.
	falling := buildCandlesLinear(250, 500, 100) // 500 → 100

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": falling,
			"QQQ": falling,
			"IWM": falling,
		},
		stocks: map[string][]models.CandleDaily{},
	})

	inputs, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inputs.SpyAbove50 {
		t.Error("SpyAbove50: expected false for falling SPY")
	}
	if inputs.SpyAbove200 {
		t.Error("SpyAbove200: expected false for falling SPY")
	}
}

func TestBuildMarketInputs_DistributionDays(t *testing.T) {
	// Build a 250-candle slice where the last 3 candles are distribution days.
	candles := buildCandles(250, 400) // flat baseline

	// Overwrite the final 3 pairs with (down close, higher volume).
	for _, i := range []int{247, 248, 249} {
		candles[i].Close = candles[i-1].Close - 1   // close down
		candles[i].Volume = candles[i-1].Volume + 1 // volume up
	}

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": candles,
			"QQQ": buildCandles(250, 350),
			"IWM": buildCandles(250, 180),
		},
		stocks: map[string][]models.CandleDaily{},
	})

	inputs, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inputs.DistributionDays != 3 {
		t.Errorf("DistributionDays: got %d, want 3", inputs.DistributionDays)
	}
}

func TestBuildMarketInputs_RelativeStrength(t *testing.T) {
	// SPY flat at 400, QQQ flat at 400, IWM flat at 200.
	// Expected RS: QQQ/SPY = 1.0, IWM/SPY = 0.5.
	spy := buildCandles(250, 400)
	qqq := buildCandles(250, 400)
	iwm := buildCandles(250, 200)

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": spy,
			"QQQ": qqq,
			"IWM": iwm,
		},
		stocks: map[string][]models.CandleDaily{},
	})

	inputs, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inputs.QqqvsspyRs != 1.0 {
		t.Errorf("QQQvsSPY_RS: got %v, want 1.0", inputs.QqqvsspyRs)
	}
	if inputs.IwmvsspyRs != 0.5 {
		t.Errorf("IWMvsSPY_RS: got %v, want 0.5", inputs.IwmvsspyRs)
	}
}

func TestBuildMarketInputs_Breadth(t *testing.T) {
	// 4 stocks: 3 above their flat SMA (rising), 1 below (falling).
	// Expected BreadthAbove50 = BreadthAbove200 = 75.0%.
	rising := buildCandlesLinear(250, 100, 300)
	falling := buildCandlesLinear(250, 300, 100)

	flat := buildCandles(250, 400)

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": flat,
			"QQQ": flat,
			"IWM": flat,
		},
		stocks: map[string][]models.CandleDaily{
			"AAPL": rising,
			"MSFT": rising,
			"NVDA": rising,
			"META": falling,
		},
	})

	inputs, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if inputs.BreadthAbove50 != 75.0 {
		t.Errorf("BreadthAbove50: got %v, want 75.0", inputs.BreadthAbove50)
	}
	if inputs.BreadthAbove200 != 75.0 {
		t.Errorf("BreadthAbove200: got %v, want 75.0", inputs.BreadthAbove200)
	}
}

func TestBuildMarketInputs_DateIsPreserved(t *testing.T) {
	flat := buildCandles(250, 400)
	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": flat, "QQQ": flat, "IWM": flat,
		},
		stocks: map[string][]models.CandleDaily{},
	})

	want := time.Date(2025, 3, 14, 0, 0, 0, 0, time.UTC)
	inputs, err := svc.BuildMarketInputs(context.Background(), want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inputs.Date.Equal(want) {
		t.Errorf("Date: got %v, want %v", inputs.Date, want)
	}
}

func TestBuildMarketInputs_PropagatesRepoError(t *testing.T) {
	svc := newTestService(&mockMarketData{
		err: fmt.Errorf("database unavailable"),
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error when repo fails, got nil")
	}
}

// ── GAP-7: Minimum-candle guard tests ─────────────────────────────────────────

func TestBuildMarketInputs_EmptySPYHistory(t *testing.T) {
	// SPY returns 0 bars — should produce a hard error, not zero-value signals.
	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": {},
			"QQQ": buildCandles(250, 350),
			"IWM": buildCandles(250, 180),
		},
		stocks: map[string][]models.CandleDaily{},
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for empty SPY history, got nil")
	}
}

func TestBuildMarketInputs_InsufficientSPYHistory(t *testing.T) {
	// SPY has only 50 candles — SMA-200 would silently return 0 without the guard.
	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": buildCandles(50, 400),
			"QQQ": buildCandles(250, 350),
			"IWM": buildCandles(250, 180),
		},
		stocks: map[string][]models.CandleDaily{},
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for < 200 SPY candles, got nil")
	}
}

func TestBuildMarketInputs_InsufficientQQQHistory(t *testing.T) {
	// QQQ has 199 candles — one short of the minimum.
	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": buildCandles(250, 400),
			"QQQ": buildCandles(199, 350),
			"IWM": buildCandles(250, 180),
		},
		stocks: map[string][]models.CandleDaily{},
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for < 200 QQQ candles, got nil")
	}
}

func TestBuildMarketInputs_InsufficientIWMHistory(t *testing.T) {
	// IWM has 100 candles — should error.
	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": buildCandles(250, 400),
			"QQQ": buildCandles(250, 350),
			"IWM": buildCandles(100, 180),
		},
		stocks: map[string][]models.CandleDaily{},
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error for < 200 IWM candles, got nil")
	}
}

func TestBuildMarketInputs_ExactlyMinHistorySucceeds(t *testing.T) {
	// Exactly 200 candles for every index — guard must pass and computation succeed.
	flat := buildCandles(200, 400)

	svc := newTestService(&mockMarketData{
		indices: map[string][]models.CandleDaily{
			"SPY": flat,
			"QQQ": flat,
			"IWM": flat,
		},
		stocks: map[string][]models.CandleDaily{},
	})

	_, err := svc.BuildMarketInputs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("expected no error with exactly 200 candles, got: %v", err)
	}
}
