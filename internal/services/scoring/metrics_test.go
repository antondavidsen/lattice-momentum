package scoring

// White-box tests for computeRawMetrics.
// Lives in package `scoring` (not `scoring_test`) to access unexported types:
//   sectorHistory, rawSectorMetrics, computeRawMetrics, extractCloses, perfReturn.

import (
	"math"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

// ── Synthetic candle builders ─────────────────────────────────────────────────

// buildTrendSeries generates deterministic candles with compound daily returns.
//
//	buildTrendSeries(250, 100, 0.2)  → steady 0.2% daily uptrend over 250 days
//	buildTrendSeries(250, 100, -0.2) → steady 0.2% daily downtrend
//	buildTrendSeries(250, 100, 0.0)  → flat at 100
//
// The series starts at `start` and each subsequent close is multiplied by
// (1 + dailyPct/100). Dates begin at 2024-01-01 and advance by 1 day.
func buildTrendSeries(days int, start, dailyPct float64) []models.CandleDaily {
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

// buildFlatSeries generates `days` candles all at the same close price.
func buildFlatSeries(days int, c float64) []models.CandleDaily {
	return buildTrendSeries(days, c, 0.0)
}

// ── TestComputeRawMetrics ─────────────────────────────────────────────────────

func TestComputeRawMetrics_StrongVsWeakSector(t *testing.T) {
	// XLK: strong uptrend → high 3M return, above SMA50 & SMA200, trend=1.0
	// XLU: downtrend      → low returns, below SMA50 & SMA200, trend=0.0
	// SPY: medium flat     → used as benchmark for RS computation

	xlkCandles := buildTrendSeries(250, 100, 0.20)  // ~65% gain over 250 days
	xluCandles := buildTrendSeries(250, 100, -0.15) // ~31% loss over 250 days
	spyCandles := buildFlatSeries(250, 150)         // flat at 150

	sectors := []sectorHistory{
		{etf: "XLK", candles: xlkCandles},
		{etf: "XLU", candles: xluCandles},
	}

	metrics := computeRawMetrics(sectors, spyCandles)

	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	xlk := metrics[0]
	xlu := metrics[1]

	// ── Performance assertions (verify math, not exact numbers) ───────────
	if xlk.perf3m <= xlu.perf3m {
		t.Errorf("XLK perf3m (%v) should be > XLU perf3m (%v)", xlk.perf3m, xlu.perf3m)
	}
	if xlk.perf1m <= xlu.perf1m {
		t.Errorf("XLK perf1m (%v) should be > XLU perf1m (%v)", xlk.perf1m, xlu.perf1m)
	}

	// ── Relative strength vs SPY ──────────────────────────────────────────
	if xlk.rs3m <= xlu.rs3m {
		t.Errorf("XLK rs3m (%v) should be > XLU rs3m (%v)", xlk.rs3m, xlu.rs3m)
	}
	// XLK is strongly rising vs flat SPY → positive RS
	if xlk.rs3m <= 0 {
		t.Errorf("XLK rs3m = %v, expected > 0 (outperforming flat SPY)", xlk.rs3m)
	}
	// XLU is falling vs flat SPY → negative RS
	if xlu.rs3m >= 0 {
		t.Errorf("XLU rs3m = %v, expected < 0 (underperforming flat SPY)", xlu.rs3m)
	}

	// ── Trend position ────────────────────────────────────────────────────
	if xlk.trend <= xlu.trend {
		t.Errorf("XLK trend (%v) should be > XLU trend (%v)", xlk.trend, xlu.trend)
	}

	// ── SMA flags ─────────────────────────────────────────────────────────
	// XLK uptrend: latest close should be above both SMAs.
	if !xlk.above50 {
		t.Error("XLK: expected above50 = true (strong uptrend)")
	}
	if !xlk.above200 {
		t.Error("XLK: expected above200 = true (strong uptrend)")
	}
	// XLU downtrend: latest close should be below both SMAs.
	if xlu.above50 {
		t.Error("XLU: expected above50 = false (downtrend)")
	}
	if xlu.above200 {
		t.Error("XLU: expected above200 = false (downtrend)")
	}
}

func TestComputeRawMetrics_AllFlat(t *testing.T) {
	// When every sector and SPY are flat at the same price:
	// perf_1m = perf_3m = 0, rs_3m = 0, above50 = false (close == SMA, not >).
	flat := buildFlatSeries(250, 100)
	sectors := []sectorHistory{
		{etf: "XLK", candles: flat},
		{etf: "XLF", candles: flat},
	}

	metrics := computeRawMetrics(sectors, flat)

	for _, m := range metrics {
		if m.perf1m != 0 {
			t.Errorf("%s: perf1m = %v, want 0", m.etf, m.perf1m)
		}
		if m.perf3m != 0 {
			t.Errorf("%s: perf3m = %v, want 0", m.etf, m.perf3m)
		}
		if m.rs3m != 0 {
			t.Errorf("%s: rs3m = %v, want 0", m.etf, m.rs3m)
		}
		// Flat: close == SMA, so ">" is false.
		if m.above50 {
			t.Errorf("%s: above50 should be false when close == SMA", m.etf)
		}
		if m.above200 {
			t.Errorf("%s: above200 should be false when close == SMA", m.etf)
		}
		if m.trend != 0.0 {
			t.Errorf("%s: trend = %v, want 0.0", m.etf, m.trend)
		}
	}
}

func TestComputeRawMetrics_RelativeStrength(t *testing.T) {
	// RS = sector_perf_3m - spy_perf_3m.
	// If sector and SPY rise at same rate → RS ≈ 0.
	// If sector rises faster → RS > 0.

	samePace := buildTrendSeries(250, 100, 0.10)
	fasterPace := buildTrendSeries(250, 100, 0.30)

	sectors := []sectorHistory{
		{etf: "SAME", candles: samePace},
		{etf: "FAST", candles: fasterPace},
	}

	metrics := computeRawMetrics(sectors, samePace) // SPY = same pace

	same := metrics[0]
	fast := metrics[1]

	// SAME sector matches SPY exactly → RS ≈ 0.
	if math.Abs(same.rs3m) > 1e-9 {
		t.Errorf("SAME rs3m = %v, want ≈ 0 (matching SPY)", same.rs3m)
	}

	// FAST sector outperforms SPY → RS > 0.
	if fast.rs3m <= 0 {
		t.Errorf("FAST rs3m = %v, expected > 0", fast.rs3m)
	}
}

// ── TestPerfReturn (unit test for the helper function) ────────────────────────

func TestPerfReturn_Basic(t *testing.T) {
	tests := []struct {
		name   string
		closes []float64
		period int
		want   float64
	}{
		{
			name:   "100 → 110 over 1 period = +10%",
			closes: []float64{100, 110},
			period: 1,
			want:   0.10,
		},
		{
			name:   "100 → 80 over 1 period = -20%",
			closes: []float64{100, 80},
			period: 1,
			want:   -0.20,
		},
		{
			name:   "insufficient data → 0",
			closes: []float64{100},
			period: 5,
			want:   0.0,
		},
		{
			name:   "zero reference → 0 (no division by zero)",
			closes: []float64{0, 100},
			period: 1,
			want:   0.0,
		},
		{
			name:   "flat → 0",
			closes: []float64{100, 100, 100},
			period: 2,
			want:   0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := perfReturn(tc.closes, tc.period)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("perfReturn = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── TestExtractCloses ─────────────────────────────────────────────────────────

func TestExtractCloses(t *testing.T) {
	candles := []models.CandleDaily{
		{Close: 100.0},
		{Close: 105.5},
		{Close: 99.0},
	}
	got := extractCloses(candles)
	want := []float64{100.0, 105.5, 99.0}

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("closes[%d] = %v, want %v", i, got[i], v)
		}
	}
}

// ── Trend score logic ─────────────────────────────────────────────────────────

func TestComputeRawMetrics_TrendScoreValues(t *testing.T) {
	// Construct 3 sectors that exercise each trend branch:
	//   above50 && above200 → 1.0    (strong uptrend)
	//   above50 only        → 0.6    (early recovery: above short MA but not long)
	//   neither             → 0.0    (downtrend)

	// Strong uptrend: 250 bars, rising from 100→400. Latest well above both SMAs.
	strongUp := buildTrendSeries(250, 100, 0.55)

	// Early recovery: build a V-shape — fall then rise. Latest above SMA50 but below SMA200.
	// 200 bars falling from 200→80, then 50 bars rising from 80→100.
	earlyRecovery := make([]models.CandleDaily, 250)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		earlyRecovery[i] = models.CandleDaily{
			Date:  base.AddDate(0, 0, i),
			Close: 200 - float64(i)*0.6, // 200 → 80
		}
	}
	for i := 200; i < 250; i++ {
		earlyRecovery[i] = models.CandleDaily{
			Date:  base.AddDate(0, 0, i),
			Close: 80 + float64(i-200)*0.6, // 80 → 110
		}
	}

	// Downtrend: steady decline.
	downtrend := buildTrendSeries(250, 200, -0.20)

	spy := buildFlatSeries(250, 150) // irrelevant for trend test

	sectors := []sectorHistory{
		{etf: "STRONG", candles: strongUp},
		{etf: "RECOV", candles: earlyRecovery},
		{etf: "DOWN", candles: downtrend},
	}
	metrics := computeRawMetrics(sectors, spy)

	// STRONG: above both SMAs → trend 1.0
	if metrics[0].trend != 1.0 {
		t.Errorf("STRONG trend = %v, want 1.0 (above50=%v, above200=%v)",
			metrics[0].trend, metrics[0].above50, metrics[0].above200)
	}

	// RECOV: above SMA50 but below SMA200 → trend 0.6
	if metrics[1].trend != 0.6 {
		t.Errorf("RECOV trend = %v, want 0.6 (above50=%v, above200=%v)",
			metrics[1].trend, metrics[1].above50, metrics[1].above200)
	}

	// DOWN: below both → trend 0.0
	if metrics[2].trend != 0.0 {
		t.Errorf("DOWN trend = %v, want 0.0 (above50=%v, above200=%v)",
			metrics[2].trend, metrics[2].above50, metrics[2].above200)
	}
}
