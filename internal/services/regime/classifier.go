package regime

import (
	"log/slog"

	"ai-stock-service/internal/models"
)

// NoHistory is the sentinel value for prevSmoothed that signals "no previous
// classification exists".  When ClassifyRegime receives this value it seeds
// the smoothed bull strength with the raw value, effectively disabling EMA on
// the first run.
const NoHistory = -1.0

// smoothingAlpha is the EMA decay factor applied to bull_strength.
//
//	α = 0.5  →  effective half-life ≈ 1 session
//	           (new data gets 50 %, yesterday gets 50 %)
//
// This suppresses single-session whipsaws in choppy tape while still reacting
// to genuine trend changes within a few sessions.
const smoothingAlpha = 0.5

// velocityThreshold is the minimum single-session drop in raw bull strength
// that triggers the smoothing bypass. A drop > 0.25 (3+ regime points) in
// one session is severe enough to warrant immediate regime downgrade.
const velocityThreshold = -0.25

// maxScore is the sum of the maximum points achievable across all scoring
// categories.  It is used to normalise raw score → bull_strength in [0, 1].
//
//	SMA distance  5 pts   (was 6 — trimmed 1 pt for R-02)
//	Golden cross  1 pt
//	Dist days     1.5 pt (was 2 — trimmed 0.5 pt for R-02)
//	Breadth       1.5 pt (was 2 — trimmed 0.5 pt for R-02)
//	RS ratios     1 pt
//	VIX           1 pt   (new — R-02)
//	TICK          0.5 pt (new — R-02)
//	Breadth vel   0.5 pt (new — R-02)
//	─────────────────
//	Total        12 pts
const maxScore = 12.0

// Drawdown thresholds for the hard label cap.
// During significant drawdowns the classifier cannot output a label more
// bullish than the threshold allows, regardless of short-term SMA signals.
const (
	drawdownCorrectionCap = -20.0 // SPY ≥ 20 % below 52-week high → max CORRECTION
	drawdownBearCap       = -30.0 // SPY ≥ 30 % below 52-week high → max BEAR
)

// ClassifierResult is the output of ClassifyRegime.
type ClassifierResult struct {
	// Label is one of the five canonical regime states.
	// It is derived from SmoothedBullStrength after the drawdown cap is applied.
	Label Label

	// RiskScore is 1 − SmoothedBullStrength, giving a value in [0, 1] where:
	//   0.0 → minimum risk  (STRONG_BULL)
	//   1.0 → maximum risk  (BEAR)
	RiskScore float64

	// RawBullStrength is today's unsmoothed normalised score (0–1).
	RawBullStrength float64

	// SmoothedBullStrength is the EMA-smoothed score used for label
	// determination.  It equals RawBullStrength on the first ever run
	// (prevSmoothed == NoHistory).
	SmoothedBullStrength float64
}

// ClassifyRegime runs the v2 rule-based scoring model against the pre-computed
// market signals in inputs, applies EMA smoothing against the previous session's
// smoothed score, enforces a trailing-drawdown label cap, and returns the regime
// label together with both raw and smoothed bull-strength values.
//
// prevSmoothed is the SmoothedBullStrength stored from the previous session.
// Pass NoHistory (= -1) on the very first run or whenever no prior row exists.
//
// ── Scoring model (12 pts max) ───────────────────────────────────────────────
//
//	SMA distance — SPY (max 3 pts, was 4 — trimmed 1 pt for R-02)
//	  spy_pct_from_sma50  > +5 %  → +1.5  (strongly above)
//	  spy_pct_from_sma50  0–+5 %  → +0.75 (just above)
//	  spy_pct_from_sma200 > +5 %  → +1.5
//	  spy_pct_from_sma200 0–+5 %  → +0.75
//
//	SMA distance — QQQ (max 2 pts)
//	  qqq_pct_from_sma50  > +5 %  → +1
//	  qqq_pct_from_sma50  0–+5 %  → +0.5
//	  qqq_pct_from_sma200 > +5 %  → +1
//	  qqq_pct_from_sma200 0–+5 %  → +0.5
//
//	Golden / death cross (max 1 pt)
//	  spy_sma50_above_sma200       → +0.5
//	  qqq_sma50_above_sma200       → +0.5
//
//	Distribution-day pressure (max 1.5 pts, was 2 — trimmed 0.5 pt for R-02)
//	  dist_days ≤ 2  → +1.5   (clean tape)
//	  dist_days ≤ 4  → +0.75  (mild pressure)
//	  dist_days ≤ 6  → +0.375 (elevated pressure)
//	  dist_days > 6  → +0     (heavy distribution)
//
//	Market breadth (max 1.5 pts, was 2 — trimmed 0.5 pt for R-02)
//	  breadth_50  ≥ 60 % → +0.75 ;  ≥ 50 % → +0.375
//	  breadth_200 ≥ 55 % → +0.75 ;  ≥ 40 % → +0.375
//
//	RS ratios (max 1 pt)
//	  qqq_vs_spy_rs > 1.05 → +0.5  (growth leadership confirmed)
//	  iwm_vs_spy_rs > 0.95 → +0.5  (small caps not lagging)
//
//	VIX (max 1 pt — new R-02)
//	  vix_level < 15  → +1.0
//	  15–20           → +0.75
//	  20–25           → +0.50
//	  25–30           → +0.25
//	  >= 30           → +0.0
//	  Spike override: vix_roc_pct > +20 % → subtract 0.25 (min 0)
//
//	NYSE $TICK (max 0.5 pt — new R-02)
//	  tick_min_daily > −600  → +0.5
//	  −600 to −900           → +0.25
//	  < −900                 → +0.0
//
//	Breadth velocity (max 0.5 pt — new R-02)
//	  breadth_velocity_5d > +5 pp  → +0.5
//	  +1 to +5 pp                  → +0.35
//	  −1 to +1 pp                  → +0.25
//	  −5 to −1 pp                  → +0.1
//	  < −5 pp                      → +0.0
//
// ── Regime thresholds (smoothed_bull_strength = smoothed_score / 12) ─────────
//
//	≥ 0.80 → STRONG_BULL
//	≥ 0.60 → BULL
//	≥ 0.40 → NEUTRAL
//	≥ 0.20 → CORRECTION
//	< 0.20 → BEAR
//
// ── Drawdown cap (applied after smoothing) ───────────────────────────────────
//
//	spy_drawdown_pct < −20 % → cap label at CORRECTION
//	spy_drawdown_pct < −30 % → cap label at BEAR
func ClassifyRegime(inputs *models.MarketInputsDaily, prevSmoothed float64) ClassifierResult {

	score := 0.0

	// ── SMA distance — SPY (max 3 pts, was 4 — trimmed 1 pt for R-02) ────────
	score += smaDistanceScore(inputs.SpyPctFromSMA50, 1.5)
	score += smaDistanceScore(inputs.SpyPctFromSMA200, 1.5)

	// ── SMA distance — QQQ (max 2 pts) ────────────────────────────────────────
	score += smaDistanceScore(inputs.QqqPctFromSMA50, 1.0)
	score += smaDistanceScore(inputs.QqqPctFromSMA200, 1.0)

	// ── Golden / death cross (max 1 pt) ───────────────────────────────────────
	if inputs.SpySMA50AboveSMA200 {
		score += 0.5
	}
	if inputs.QqqSMA50AboveSMA200 {
		score += 0.5
	}

	// ── Distribution-day pressure (max 1.5 pts, was 2 — trimmed 0.5 pt) ──────
	switch {
	case inputs.DistributionDays <= 2:
		score += 1.5
	case inputs.DistributionDays <= 4:
		score += 0.75
	case inputs.DistributionDays <= 6:
		score += 0.375
		// > 6: no points — heavy distribution
	}

	// ── Market breadth (max 1.5 pts, was 2 — trimmed 0.5 pt) ──────────────────
	switch {
	case inputs.BreadthAbove50 >= 60.0:
		score += 0.75
	case inputs.BreadthAbove50 >= 50.0:
		score += 0.375
	}
	switch {
	case inputs.BreadthAbove200 >= 55.0:
		score += 0.75
	case inputs.BreadthAbove200 >= 40.0:
		score += 0.375
	}

	// ── RS ratios: QQQ leadership + IWM health (max 1 pt) ────────────────────
	if inputs.QQQvsSPYRS > 1.05 {
		score += 0.5 // growth stocks leading
	}
	if inputs.IWMvsSPYRS > 0.95 {
		score += 0.5 // small caps not lagging
	}

	// ── R-02: VIX (max 1 pt) ─────────────────────────────────────────────────
	score += ScoreVIX(inputs.VIXLevel, inputs.VIXROCpct)

	// ── R-02: NYSE $TICK (max 0.5 pt) ────────────────────────────────────────
	score += ScoreTick(inputs.TickMinDaily, slog.Default())

	// ── R-02: Breadth velocity (max 0.5 pt) ──────────────────────────────────
	score += ScoreBreadthVelocity(inputs.BreadthVelocity5d)

	// ── Normalise ─────────────────────────────────────────────────────────────

	rawBullStrength := score / maxScore

	// ── EMA smoothing with velocity bypass ────────────────────────────────────
	smoothed := rawBullStrength
	if prevSmoothed >= 0 {
		// Velocity check: if raw strength drops sharply in a single session,
		// bypass smoothing to react immediately (crash detection fast-path).
		delta := rawBullStrength - prevSmoothed
		if delta < velocityThreshold {
			// Severe deterioration: use raw value directly to avoid lag.
			smoothed = rawBullStrength
		} else {
			smoothed = smoothingAlpha*rawBullStrength + (1-smoothingAlpha)*prevSmoothed
		}
	}

	// ── Label from smoothed bull strength ─────────────────────────────────────
	label := labelFromStrength(smoothed)

	// ── Trailing drawdown cap ─────────────────────────────────────────────────
	// A significant SPY drawdown from its 52-week high prevents the classifier
	// from outputting an optimistically bullish label during bear-market rallies.
	if inputs.SpyDrawdownPct < drawdownBearCap {
		label = mostBearish(label, RegimeBear)
	} else if inputs.SpyDrawdownPct < drawdownCorrectionCap {
		label = mostBearish(label, RegimeCorrection)
	}

	return ClassifierResult{
		Label:                label,
		RiskScore:            1.0 - smoothed,
		RawBullStrength:      rawBullStrength,
		SmoothedBullStrength: smoothed,
	}
}

// ── private helpers ───────────────────────────────────────────────────────────

// smaDistanceScore converts a percentage-distance-from-SMA value into a
// partial score scaled by maxPts.
//
//	pct > +5 %  → full maxPts
//	pct 0–+5 %  → half maxPts
//	pct < 0 %   → 0   (below SMA contributes nothing)
func smaDistanceScore(pct, maxPts float64) float64 {
	switch {
	case pct > 5.0:
		return maxPts
	case pct >= 0:
		return maxPts * 0.5
	default:
		return 0
	}
}

// labelFromStrength maps a normalised bull strength value to a RegimeLabel
// using the fixed threshold ladder.
func labelFromStrength(s float64) Label {
	switch {
	case s >= 0.80:
		return RegimeStrongBull
	case s >= 0.60:
		return RegimeBull
	case s >= 0.40:
		return RegimeNeutral
	case s >= 0.20:
		return RegimeCorrection
	default:
		return RegimeBear
	}
}

// regimeRank returns an ordinal rank where higher = more bullish.
func regimeRank(l Label) int {
	switch l {
	case RegimeStrongBull:
		return 4
	case RegimeBull:
		return 3
	case RegimeNeutral:
		return 2
	case RegimeCorrection:
		return 1
	case RegimeBear:
		return 0
	default: // RegimeBear
		return 0
	}
}

// mostBearish returns whichever of a or b is the more bearish (lower rank).
// Used to enforce the drawdown cap without replacing a label that is already
// more conservative than the cap threshold.
func mostBearish(a, b Label) Label {
	if regimeRank(a) <= regimeRank(b) {
		return a
	}
	return b
}

// ── R-02 pure scoring functions ───────────────────────────────────────────────

// ScoreVIX returns 0.0–1.0 based on VIX level and 1-day ROC.
// Lower VIX = more bullish (low fear).  A spike override subtracts 0.25
// when VIX surges > +20 % in a single session (minimum 0).
//
// Thresholds:
//
//	VIX < 15  → 1.0
//	15–20     → 0.75
//	20–25     → 0.50
//	25–30     → 0.25
//	>= 30     → 0.0
//
// Spike override: vixROC > +20 % → subtract 0.25 (min 0).
//
// When vixLevel or vixROC is nil (data unavailable), returns 0.5 (neutral).
// This prevents missing data from being scored as either bullish (1.0) or
// bearish (0.0), similar to ScoreTick's nil handling.
func ScoreVIX(vixLevel, vixROC *float64) float64 {
	if vixLevel == nil || vixROC == nil {
		return 0.5 // neutral when VIX data unavailable
	}
	var s float64
	switch {
	case *vixLevel < 15:
		s = 1.0
	case *vixLevel < 20:
		s = 0.75
	case *vixLevel < 25:
		s = 0.50
	case *vixLevel < 30:
		s = 0.25
	default:
		s = 0.0
	}
	// Spike override: sharp VIX surge in one session is bearish.
	if *vixROC > 20.0 {
		s -= 0.25
		if s < 0 {
			s = 0
		}
	}
	return s
}

// ScoreTick returns 0.0–0.5 based on the session low of NYSE $TICK.
// Higher (less negative) TICK = more bullish (broad buying).
//
// Thresholds:
//
//	tickMin > −600  → 0.5
//	−600 to −900    → 0.25
//	< −900          → 0.0
//
// When tickMin is nil (data source unavailable), returns 0.25 (neutral)
// and logs a warning. This prevents missing data from being scored as
// either bullish (0.5) or bearish (0.0).
//
// TODO: Wire IB TWS API or Norgate Data as a real $TICK source.
func ScoreTick(tickMin *float64, log *slog.Logger) float64 {
	if tickMin == nil {
		log.Warn("tick_min_daily unavailable, using neutral score (0.25)")
		return 0.25
	}
	switch {
	case *tickMin > -600:
		return 0.5
	case *tickMin >= -900:
		return 0.25
	default:
		return 0.0
	}
}

// ScoreBreadthVelocity returns 0.0–0.5 based on the 5-session percentage-point
// change in breadth_above_50.  Positive velocity = breadth improving.
//
// Thresholds:
//
//	velocity > +5 pp  → 0.5
//	+1 to +5 pp       → 0.35
//	−1 to +1 pp       → 0.25
//	−5 to −1 pp       → 0.1
//	< −5 pp           → 0.0
func ScoreBreadthVelocity(velocity float64) float64 {
	switch {
	case velocity > 5.0:
		return 0.5
	case velocity > 1.0:
		return 0.35
	case velocity >= -1.0:
		return 0.25
	case velocity > -5.0:
		return 0.1
	default:
		return 0.0
	}
}
