package regime_test

import (
	"log/slog"
	"testing"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/services/regime"
)

// makeInputs is a test helper that builds a fully populated MarketInputsDaily.
// All v2 fields default to values consistent with the described scenario so
// individual tests only need to override what they care about.
// R-02 fields (VIX, TICK, breadth velocity) default to neutral (0) so they
// do not affect existing test expectations.
func makeInputs(
	spyPct50, spyPct200, qqqPct50, qqqPct200 float64, // % from SMA (positive = above)
	spyGolden, qqqGolden bool,
	distDays int,
	breadth50, breadth200 float64,
	qqqRS, iwmRS float64,
	drawdownPct float64,
) models.MarketInputsDaily {
	return models.MarketInputsDaily{
		Date: time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		// boolean flags (derived from pct fields for consistency)
		SpyAbove50:  spyPct50 >= 0,
		SpyAbove200: spyPct200 >= 0,
		QqqAbove50:  qqqPct50 >= 0,
		QqqAbove200: qqqPct200 >= 0,
		// v2 continuous
		SpyPctFromSMA50:     spyPct50,
		SpyPctFromSMA200:    spyPct200,
		QqqPctFromSMA50:     qqqPct50,
		QqqPctFromSMA200:    qqqPct200,
		SpySMA50AboveSMA200: spyGolden,
		QqqSMA50AboveSMA200: qqqGolden,
		DistributionDays:    distDays,
		BreadthAbove50:      breadth50,
		BreadthAbove200:     breadth200,
		QQQvsSPYRS:          qqqRS,
		IWMvsSPYRS:          iwmRS,
		SpyDrawdownPct:      drawdownPct,
		// R-02: default to nil (data unavailable) — existing tests unaffected
		VIXLevel:          nil,
		VIXROCpct:         nil,
		TickMinDaily:      nil, // nil → ScoreTick returns neutral 0.25
		BreadthVelocity5d: 0,
	}
}

// perfectBull returns a textbook STRONG_BULL input set.
func perfectBull() models.MarketInputsDaily {
	return makeInputs(
		8.0, 12.0, 7.0, 9.0, // strongly above all SMAs
		true, true, // golden cross on both
		1,          // clean tape
		72.0, 62.0, // broad participation
		1.08, 0.98, // QQQ leading, IWM healthy
		-2.0, // only 2 % below 52w high (near all-time highs)
	)
}

// perfectBear returns a textbook BEAR input set.
func perfectBear() models.MarketInputsDaily {
	return makeInputs(
		-8.0, -14.0, -7.0, -12.0, // below all SMAs
		false, false, // death cross
		9,          // heavy distribution
		28.0, 16.0, // very poor breadth
		0.94, 0.88, // QQQ lagging, IWM lagging
		-32.0, // 32 % below 52w high
	)
}

// ── Label tests ───────────────────────────────────────────────────────────────

func TestClassifyRegime_StrongBull(t *testing.T) {
	inp := perfectBull()
	result := regime.ClassifyRegime(&inp, regime.NoHistory)

	if result.Label != regime.RegimeStrongBull {
		t.Errorf("label: got %s, want %s", result.Label, regime.RegimeStrongBull)
	}
	if !result.Label.Valid() {
		t.Errorf("label %q failed Valid()", result.Label)
	}
	if result.RiskScore >= 0.20 {
		t.Errorf("RiskScore: got %.4f, want < 0.20 for strong_bull", result.RiskScore)
	}
}

func TestClassifyRegime_Bear(t *testing.T) {
	inp := perfectBear()
	result := regime.ClassifyRegime(&inp, regime.NoHistory)

	if result.Label != regime.RegimeBear {
		t.Errorf("label: got %s, want %s", result.Label, regime.RegimeBear)
	}
	if result.RiskScore <= 0.80 {
		t.Errorf("RiskScore: got %.4f, want > 0.80 for bear", result.RiskScore)
	}
}

func TestClassifyRegime_Neutral(t *testing.T) {
	// SPY above both SMAs (SMA50 just above, SMA200 strongly above), QQQ above
	// both SMAs, no golden cross, moderate dist days, mediocre breadth, IWM
	// barely healthy.
	// Score (R-02 revised): SPY50(+4%→+0.75) + SPY200(+6%→+1.5)
	//        + QQQ50(+4%→+0.5) + QQQ200(+2%→+0.5) + dist3(+0.75)
	//        + breadth50≥50%(+0.375) + breadth200≥40%(+0.375) + IWM_RS>0.95(+0.5)
	//        = 5.25/12 = 0.438 → NEUTRAL
	inputs := makeInputs(
		4.0, 6.0, 4.0, 2.0,
		false, false,
		3,
		53.0, 42.0,
		1.01, 0.96,
		-5.0,
	)

	result := regime.ClassifyRegime(&inputs, regime.NoHistory)

	if result.Label != regime.RegimeNeutral {
		t.Errorf("label: got %s, want %s (raw=%.4f)", result.Label, regime.RegimeNeutral, result.RawBullStrength)
	}
}

// ── RiskScore / strength range ────────────────────────────────────────────────

func TestClassifyRegime_RiskScoreInRange(t *testing.T) {
	cases := []models.MarketInputsDaily{
		perfectBull(),
		perfectBear(),
		makeInputs(2.0, 2.0, 1.0, 1.0, true, false, 4, 52.0, 42.0, 1.02, 0.96, -8.0),
	}
	for _, inp := range cases {
		r := regime.ClassifyRegime(&inp, regime.NoHistory)
		if r.RiskScore < 0.0 || r.RiskScore > 1.0 {
			t.Errorf("RiskScore out of [0,1]: %.6f", r.RiskScore)
		}
		if !r.Label.Valid() {
			t.Errorf("invalid label %q", r.Label)
		}
		if r.RawBullStrength < 0.0 || r.RawBullStrength > 1.0 {
			t.Errorf("RawBullStrength out of [0,1]: %.6f", r.RawBullStrength)
		}
	}
}

func TestClassifyRegime_LabelConsistentWithRiskScore(t *testing.T) {
	// Scenarios are ordered from most bullish to most bearish.
	// Each successive scenario must produce a strictly higher RiskScore.
	scenarios := []models.MarketInputsDaily{
		// strong_bull: everything green, score ≈ 10/12
		makeInputs(8.0, 10.0, 6.0, 8.0, true, true, 1, 70.0, 58.0, 1.08, 0.97, -1.0),
		// bull: most signals green, score ≈ 7.5/12
		makeInputs(6.0, 7.0, 4.0, 5.0, true, true, 3, 58.0, 46.0, 1.06, 0.96, -5.0),
		// neutral: SPY above both SMAs, QQQ above both, no golden cross, score ≈ 5.25/12
		makeInputs(4.0, 6.0, 4.0, 2.0, false, false, 3, 53.0, 42.0, 1.01, 0.96, -5.0),
		// correction: SPY just above SMAs, QQQ barely above SMA50, moderate dist, poor breadth, score ≈ 2.75/12
		makeInputs(2.0, 2.0, 1.0, -2.0, false, false, 4, 44.0, 38.0, 0.99, 0.93, -12.0),
		// bear: all below SMAs, heavy distribution, drawdown -25% (cap at CORRECTION but score=0 → BEAR wins)
		makeInputs(-8.0, -14.0, -7.0, -12.0, false, false, 9, 28.0, 16.0, 0.94, 0.88, -25.0),
	}

	var prevRisk float64 = -1
	for i, sc := range scenarios {
		r := regime.ClassifyRegime(&sc, regime.NoHistory)
		if r.RiskScore <= prevRisk {
			t.Errorf("scenario %d: RiskScore %.4f should be > previous %.4f (label=%s)",
				i, r.RiskScore, prevRisk, r.Label)
		}
		prevRisk = r.RiskScore
	}
}

// ── Drawdown cap ──────────────────────────────────────────────────────────────

func TestClassifyRegime_DrawdownCapAtCorrection(t *testing.T) {
	// All SMA signals are bullish, but SPY is 22 % below its 52w high.
	// The drawdown cap must prevent STRONG_BULL / BULL, ceiling at CORRECTION.
	inputs := makeInputs(8.0, 10.0, 7.0, 9.0, true, true, 1, 70.0, 60.0, 1.08, 0.97, -22.0)
	result := regime.ClassifyRegime(&inputs, regime.NoHistory)

	if result.Label != regime.RegimeCorrection {
		t.Errorf("drawdown -22%%: got %s, want correction", result.Label)
	}
}

func TestClassifyRegime_DrawdownCapAtBear(t *testing.T) {
	// Bull signals, but SPY 32 % down from 52w high.
	inputs := makeInputs(8.0, 10.0, 7.0, 9.0, true, true, 1, 70.0, 60.0, 1.08, 0.97, -32.0)
	result := regime.ClassifyRegime(&inputs, regime.NoHistory)

	if result.Label != regime.RegimeBear {
		t.Errorf("drawdown -32%%: got %s, want bear", result.Label)
	}
}

func TestClassifyRegime_DrawdownCapDoesNotElevateBear(t *testing.T) {
	// If signals already classify as BEAR, the cap must not change the label.
	inp := perfectBear()
	result := regime.ClassifyRegime(&inp, regime.NoHistory)
	if result.Label != regime.RegimeBear {
		t.Errorf("bear inputs + deep drawdown: got %s, want bear", result.Label)
	}
}

// ── EMA smoothing ─────────────────────────────────────────────────────────────

func TestClassifyRegime_FirstRunNoSmoothing(t *testing.T) {
	// With NoHistory the smoothed value must equal the raw value.
	inp := perfectBull()
	result := regime.ClassifyRegime(&inp, regime.NoHistory)

	if result.RawBullStrength != result.SmoothedBullStrength {
		t.Errorf("first run: raw %.4f != smoothed %.4f", result.RawBullStrength, result.SmoothedBullStrength)
	}
}

func TestClassifyRegime_SmoothingBlendsPrevAndRaw(t *testing.T) {
	// prevSmoothed = 0.7 (previously bull), today's raw ≈ 0.52 (neutral).
	// Delta = 0.52 - 0.7 = -0.18, which is above velocity threshold (-0.25),
	// so normal EMA applies: smoothed = 0.5*0.52 + 0.5*0.7 = 0.61 → BULL.
	inputs := makeInputs(
		4.0, 5.0, 1.0, 2.0, true, true,
		4, 52.0, 42.0,
		1.0, 0.96,
		-4.0,
	)

	result := regime.ClassifyRegime(&inputs, 0.7)

	if result.Label != regime.RegimeBull {
		t.Errorf("smoothed label: got %s, want bull (raw=%.4f, smoothed=%.4f)", result.Label, result.RawBullStrength, result.SmoothedBullStrength)
	}
	// smoothed must be between raw and prev.
	if result.SmoothedBullStrength <= result.RawBullStrength || result.SmoothedBullStrength >= 0.7 {
		t.Errorf("smoothed %.4f should be between raw %.4f and prev 0.7",
			result.SmoothedBullStrength, result.RawBullStrength)
	}
}

// ── RS ratio scoring ──────────────────────────────────────────────────────────

func TestClassifyRegime_RSRatiosContributeToScore(t *testing.T) {
	// Two identical input sets differing only in RS ratios.
	// The one with strong QQQ + healthy IWM must score higher.
	base := makeInputs(4.0, 4.0, 3.0, 3.0, true, true, 2, 60.0, 50.0, 1.0, 1.0, -3.0)
	withRS := base
	withRS.QQQvsSPYRS = 1.08 // > 1.05 → +0.5 pt
	withRS.IWMvsSPYRS = 0.97 // > 0.95 → +0.5 pt

	rBase := regime.ClassifyRegime(&base, regime.NoHistory)
	rRS := regime.ClassifyRegime(&withRS, regime.NoHistory)

	if rRS.RawBullStrength <= rBase.RawBullStrength {
		t.Errorf("RS bonus not applied: withRS=%.4f base=%.4f", rRS.RawBullStrength, rBase.RawBullStrength)
	}
}

// ── Golden cross scoring ──────────────────────────────────────────────────────

func TestClassifyRegime_GoldenCrossAddsPoints(t *testing.T) {
	noX := makeInputs(4.0, 4.0, 3.0, 3.0, false, false, 2, 60.0, 50.0, 1.0, 0.96, -3.0)
	withX := noX
	withX.SpySMA50AboveSMA200 = true
	withX.QqqSMA50AboveSMA200 = true

	rNoX := regime.ClassifyRegime(&noX, regime.NoHistory)
	rX := regime.ClassifyRegime(&withX, regime.NoHistory)

	if rX.RawBullStrength <= rNoX.RawBullStrength {
		t.Errorf("golden cross bonus not applied: withCross=%.4f noCross=%.4f",
			rX.RawBullStrength, rNoX.RawBullStrength)
	}
}

// ── Velocity bypass ───────────────────────────────────────────────────────────

func TestClassifyRegime_VelocityBypass(t *testing.T) {
	// Previous session was strong bull (smoothed = 0.90).
	// Today's signals collapsed to bear-like levels (raw ≈ 0.0).
	// Without velocity bypass: smoothed = 0.5*0.0 + 0.5*0.90 = 0.45 → "neutral"
	// With velocity bypass: smoothed = 0.0 → "bear"
	inputs := perfectBear()
	inputs.SpyDrawdownPct = -5.0 // don't trigger drawdown cap
	result := regime.ClassifyRegime(&inputs, 0.90)

	if result.SmoothedBullStrength > 0.40 {
		t.Errorf("velocity bypass failed: smoothed=%.2f, want ≤ 0.40", result.SmoothedBullStrength)
	}
	if result.Label != regime.RegimeCorrection && result.Label != regime.RegimeBear {
		t.Errorf("label: got %s, want correction or bear", result.Label)
	}
}

func TestClassifyRegime_VelocityBypassNotTriggeredOnSmallDrop(t *testing.T) {
	// Previous session smoothed = 0.70, today raw ≈ 0.52.
	// Delta = -0.18, which is above -0.25 threshold.
	// Normal EMA should apply.
	inputs := makeInputs(
		4.0, 5.0, 1.0, 2.0, true, true,
		4, 52.0, 42.0,
		1.0, 0.96,
		-4.0,
	)

	result := regime.ClassifyRegime(&inputs, 0.70)

	// Smoothed should be blended between raw and prev (not raw alone).
	if result.SmoothedBullStrength == result.RawBullStrength {
		t.Errorf("velocity bypass incorrectly triggered: smoothed=raw=%.4f", result.SmoothedBullStrength)
	}
}

// ── R-02 pure scoring functions ───────────────────────────────────────────────

func TestVIXScore(t *testing.T) {
	// Use named sub-tests with explicit pointer construction so we cover
	// nil-level, nil-ROC, and normal cases without fragile test-name matching.
	t.Run("nil level (no data)", func(t *testing.T) {
		got := regime.ScoreVIX(nil, ptr(0))
		if got != 0.5 {
			t.Errorf("want 0.5 (neutral), got %.2f", got)
		}
	})
	t.Run("nil ROC (partial data)", func(t *testing.T) {
		got := regime.ScoreVIX(ptr(22.0), nil)
		if got != 0.5 {
			t.Errorf("want 0.5 (neutral), got %.2f", got)
		}
	})
	t.Run("below 15", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(12.0), ptr(0)); got != 1.0 {
			t.Errorf("want 1.0, got %.2f", got)
		}
	})
	t.Run("exactly 15", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(15.0), ptr(0)); got != 0.75 {
			t.Errorf("want 0.75, got %.2f", got)
		}
	})
	t.Run("mid 15-20", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(17.5), ptr(0)); got != 0.75 {
			t.Errorf("want 0.75, got %.2f", got)
		}
	})
	t.Run("exactly 20", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(20.0), ptr(0)); got != 0.50 {
			t.Errorf("want 0.50, got %.2f", got)
		}
	})
	t.Run("mid 20-25", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(22.0), ptr(0)); got != 0.50 {
			t.Errorf("want 0.50, got %.2f", got)
		}
	})
	t.Run("exactly 25", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(25.0), ptr(0)); got != 0.25 {
			t.Errorf("want 0.25, got %.2f", got)
		}
	})
	t.Run("mid 25-30", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(27.0), ptr(0)); got != 0.25 {
			t.Errorf("want 0.25, got %.2f", got)
		}
	})
	t.Run("exactly 30", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(30.0), ptr(0)); got != 0.0 {
			t.Errorf("want 0.0, got %.2f", got)
		}
	})
	t.Run("above 30", func(t *testing.T) {
		if got := regime.ScoreVIX(ptr(35.0), ptr(0)); got != 0.0 {
			t.Errorf("want 0.0, got %.2f", got)
		}
	})
	t.Run("spike override: ROC > 20%", func(t *testing.T) {
		got := regime.ScoreVIX(ptr(18.0), ptr(25.0))
		if got != 0.50 { // 0.75 - 0.25
			t.Errorf("want 0.50, got %.2f", got)
		}
	})
	t.Run("spike override: ROC exactly 20%", func(t *testing.T) {
		got := regime.ScoreVIX(ptr(18.0), ptr(20.0))
		if got != 0.75 { // not > 20, no override
			t.Errorf("want 0.75, got %.2f", got)
		}
	})
	t.Run("spike override: min 0", func(t *testing.T) {
		got := regime.ScoreVIX(ptr(30.0), ptr(30.0))
		if got != 0.0 { // 0.0 - 0.25 clamped to 0
			t.Errorf("want 0.0, got %.2f", got)
		}
	})
	t.Run("spike override: low VIX + spike", func(t *testing.T) {
		got := regime.ScoreVIX(ptr(12.0), ptr(22.0))
		if got != 0.75 { // 1.0 - 0.25
			t.Errorf("want 0.75, got %.2f", got)
		}
	})
}

func TestTickScore(t *testing.T) {
	tests := []struct {
		name string
		tick *float64
		want float64
	}{
		{"above -600", ptr(-400), 0.5},
		{"exactly -600", ptr(-600), 0.25},
		{"mid range", ptr(-750), 0.25},
		{"exactly -900", ptr(-900), 0.25}, // −600 to −900 inclusive → 0.25
		{"below -900", ptr(-1200), 0.0},
		{"positive TICK", ptr(200), 0.5},
		{"nil (no data)", nil, 0.25}, // nil → neutral fallback
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := regime.ScoreTick(tt.tick, slog.Default())
			if got != tt.want {
				t.Errorf("ScoreTick(%v) = %.2f, want %.2f", tt.tick, got, tt.want)
			}
		})
	}
}

// ptr returns a pointer to v for use in test structs.
func ptr(v float64) *float64 { return &v }

// ── VIX spike override test (Prompt 2, step 5) ────────────────────────────────

func TestClassifyRegime_VIXSpikeReducesScore(t *testing.T) {
	// Base: VIX=22 (score 0.50), ROC=25% (>20% spike → -0.25 penalty)
	// Expected VIX score: 0.50 - 0.25 = 0.25
	inputs := makeInputs(
		4.0, 5.0, 3.0, 3.0, true, true,
		2, 60.0, 50.0, 1.02, 0.96, -3.0,
	)
	vixLvl := 22.0
	vixRoc := 25.0
	inputs.VIXLevel = &vixLvl
	inputs.VIXROCpct = &vixRoc

	result := regime.ClassifyRegime(&inputs, regime.NoHistory)

	// The VIX score should be 0.25 (0.50 base - 0.25 spike penalty).
	// We verify indirectly: the raw bull strength should be lower than
	// an identical input set without the VIX spike.
	noSpike := inputs
	zeroRoc := 0.0
	noSpike.VIXROCpct = &zeroRoc
	rNoSpike := regime.ClassifyRegime(&noSpike, regime.NoHistory)

	if result.RawBullStrength >= rNoSpike.RawBullStrength {
		t.Errorf("VIX spike should reduce score: withSpike=%.4f noSpike=%.4f",
			result.RawBullStrength, rNoSpike.RawBullStrength)
	}
}

func TestBreadthVelocityScore(t *testing.T) {
	tests := []struct {
		name     string
		velocity float64
		want     float64
	}{
		{"above +5 pp", 7.0, 0.5},
		{"exactly +5 pp", 5.0, 0.35}, // not > 5, falls into +1 to +5
		{"mid +1 to +5", 3.0, 0.35},
		{"exactly +1 pp", 1.0, 0.25}, // not > 1, falls into -1 to +1
		{"near zero", 0.0, 0.25},
		{"exactly -1 pp", -1.0, 0.25}, // not < -1, still in -1 to +1
		{"mid -5 to -1", -3.0, 0.1},
		{"exactly -5 pp", -5.0, 0.0}, // not > -5, falls into < -5
		{"below -5 pp", -8.0, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := regime.ScoreBreadthVelocity(tt.velocity)
			if got != tt.want {
				t.Errorf("ScoreBreadthVelocity(%.1f) = %.2f, want %.2f", tt.velocity, got, tt.want)
			}
		})
	}
}
