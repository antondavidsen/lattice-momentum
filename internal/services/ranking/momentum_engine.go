package ranking

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
)

// ── MomentumWeights ───────────────────────────────────────────────────────────

// MomentumWeights holds the 5 component weights + 2 multipliers for the
// Momentum engine.  Loaded from the momentum_score_weights DB table at startup;
// falls back to defaults if the DB is unavailable.
type MomentumWeights struct {
	BreakoutStrength        float64
	RelativeStrength        float64
	VolumeExpansion         float64
	VolumePriceConfirmation float64
	TrendConsistency        float64
	RegimeMult              float64
	SectorMult              float64
}

// defaultMomentumWeights are the hardcoded defaults (version 0 seed values).
var defaultMomentumWeights = MomentumWeights{
	BreakoutStrength:        0.30,
	RelativeStrength:        0.25,
	VolumeExpansion:         0.10,
	VolumePriceConfirmation: 0.15,
	TrendConsistency:        0.20,
	RegimeMult:              1.0,
	SectorMult:              1.0,
}

// MomentumWeightsProvider is the interface for loading Momentum weights from DB.
type MomentumWeightsProvider interface {
	GetActiveWeights(ctx context.Context) (MomentumWeights, error)
}

// ── Momentum Engine ───────────────────────────────────────────────────────────

// MomentumEngine scores tickers for the pure technical breakout leaders list.
//
// Scoring formula:
//
//	MOMENTUM_SCORE =
//	  w_breakout_strength × breakout_strength
//	+ w_relative_strength × relative_strength
//	+ w_volume_expansion × volume_expansion
//	+ w_volume_price_confirmation × volume_price_confirmation
//	+ w_trend_consistency × trend_consistency
//
// All weights are DB-driven with monthly refit (see NightlyWeightRefitJob).
// Falls back to defaultMomentumWeights if DB is unavailable.
//
// Primary sources: momentum screener (required), market_leaders screener
// (secondary boost).
type MomentumEngine struct {
	snapshots         SnapshotSource
	regime            RegimeSource
	sectorScores      SectorScoreSource
	leadership        LeadershipSource        // R-11: sector leadership bonus
	narrativeVelocity NarrativeVelocitySource // optional — nil means no narrative bonus
	candleRepo        CandleReader            // pennant detection
	weights           MomentumWeights
	log               *slog.Logger
}

// NewMomentumEngine constructs a MomentumEngine.
// Weights are loaded from the DB at startup; falls back to defaults on error.
func NewMomentumEngine(
	snapshots SnapshotSource,
	regime RegimeSource,
	sectorScores SectorScoreSource,
	weightsProvider MomentumWeightsProvider,
	log *slog.Logger,
) *MomentumEngine {
	w := loadMomentumWeights(context.Background(), weightsProvider, log)
	return &MomentumEngine{
		snapshots:    snapshots,
		regime:       regime,
		sectorScores: sectorScores,
		weights:      w,
		log:          log,
	}
}

// loadMomentumWeights attempts to load active weights from the DB.
// On any error, logs a warning and returns defaults.
func loadMomentumWeights(ctx context.Context, provider MomentumWeightsProvider, log *slog.Logger) MomentumWeights {
	if provider == nil {
		log.Warn("MomentumEngine: no weights provider, using defaults")
		return defaultMomentumWeights
	}
	w, err := provider.GetActiveWeights(ctx)

	if err != nil {
		log.Warn("MomentumEngine: failed to load DB weights, using defaults", "error", err)
		return defaultMomentumWeights
	}
	log.Info("MomentumEngine: loaded DB weights",
		"breakout_strength", w.BreakoutStrength, "relative_strength", w.RelativeStrength, "volume_expansion", w.VolumeExpansion,
		"volume_price_confirmation", w.VolumePriceConfirmation, "trend_consistency", w.TrendConsistency,
		"regime_mult", w.RegimeMult, "sector_mult", w.SectorMult)
	return w
}

// WithLeadership attaches a LeadershipSource for the R-11 sector leadership
// bonus and returns the engine for fluent chaining.
func (e *MomentumEngine) WithLeadership(src LeadershipSource) *MomentumEngine {
	e.leadership = src
	return e
}

// WithNarrativeVelocity attaches a NarrativeVelocitySource for the narrative
// velocity multiplier bonus and returns the engine for fluent chaining.
// Optional — nil means no narrative bonus (defaults to 1.0).
func (e *MomentumEngine) WithNarrativeVelocity(src NarrativeVelocitySource) *MomentumEngine {
	e.narrativeVelocity = src
	return e
}

// WithCandleRepo attaches a CandleReader for pennant detection.
// Optional — nil means no pennant bonus (defaults to no boost).
func (e *MomentumEngine) WithCandleRepo(src CandleReader) *MomentumEngine {
	e.candleRepo = src
	return e
}

// Compute implements RankingEngine.
func (e *MomentumEngine) Compute(ctx context.Context, date time.Time) ([]RankedTicker, error) {
	tag := date.Format("2006-01-02")

	allSnaps, err := e.snapshots.ListByDate(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("MomentumEngine [%s]: load snapshots: %w", tag, err)
	}

	// Primary: momentum screener; secondary: market_leaders for boost.
	momSnaps := filterBySource(allSnaps, models.ScreenerMomentum)
	leadersSet := buildTickerSet(filterBySource(allSnaps, models.ScreenerMarketLeaders))

	if len(momSnaps) == 0 {
		e.log.Warn("MomentumEngine: no momentum snapshots found", "date", tag)
		return nil, nil
	}

	regimeMult, err := e.loadRegimeMultiplier(ctx, date, tag)
	if err != nil {
		return nil, err
	}

	sectorLabels, err := e.loadSectorLabels(ctx, date, tag)
	if err != nil {
		return nil, err
	}

	// Load leadership map (R-11: non-fatal — defaults to empty if missing).
	leadersMap, _ := e.loadLeadersMap(ctx, date)

	var candidates []RankedTicker
	for i := range momSnaps {
		snap := &momSnaps[i]
		// Filters: price > $5.
		price := safeDeref(snap.Close, 0)
		if price <= 5.0 {
			continue
		}
		if avgDollarVolume(snap) < 10e6 {
			continue // minimum $10M avg daily dollar volume for momentum
		}

		breakoutStr := computeBreakoutStrength(snap)
		relStr := computeRelativeStrength(snap)
		volExp := computeVolumeExpansion(snap)
		volConfirm := computeVolumePriceConfirmation(snap)
		trendCons := computeTrendConsistency(snap)

		// Narrative velocity multiplier: max +10% on breakout strength.
		narrativeScore := e.loadNarrativeScore(ctx, snap.Ticker, date)
		narrativeBonus := 1.0 + (0.10 * narrativeScore)
		breakoutStr *= narrativeBonus

		raw := e.weights.BreakoutStrength*breakoutStr +
			e.weights.RelativeStrength*relStr +
			e.weights.VolumeExpansion*volExp +
			e.weights.VolumePriceConfirmation*volConfirm +
			e.weights.TrendConsistency*trendCons

		// Extension penalty: demote stocks where the breakout already happened
		// and the low-risk pivot is gone. A stock at 52W high with RSI > 75
		// and strong recent perf is a LATE ENTRY — the chart setup is consumed.
		extPenalty := computeExtensionPenalty(snap)
		raw *= extPenalty

		// Secondary boost: if also on market_leaders screener, +5%.
		if _, inLeaders := leadersSet[snap.Ticker]; inLeaders {
			raw *= 1.05
		}
		raw = normalizeZeroOne(raw)

		sectorLabel := lookupSectorLabel(safeDerefStr(snap.Sector, ""), sectorLabels)
		score := scaleScore(raw, regimeMult, sectorMultiplier(sectorLabel))

		// R-11: sector leadership bonus — 5% boost for sector leaders.
		isLeader := leadersMap[snap.Ticker]
		leadershipBonus := 1.0
		if isLeader {
			leadershipBonus = 1.05
			score *= leadershipBonus
		}

		candidates = append(candidates, RankedTicker{
			Ticker: snap.Ticker,
			Score:  score,
			Reason: map[string]float64{
				"breakout_strength":         breakoutStr,
				"relative_strength":         relStr,
				"volume_expansion":          volExp,
				"volume_price_confirmation": volConfirm,
				"trend_consistency":         trendCons,
				"extension_penalty":         extPenalty,
				"narrative_velocity":        narrativeScore,
				"raw_composite":             raw,
				"leaders_boost":             boolToFloat(leadersSet[snap.Ticker]),
				"regime_mult":               regimeMult,
				"sector_mult":               sectorMultiplier(sectorLabel),
				"leadership_bonus":          leadershipBonus,
			},
		})
	}

	sortDescending(candidates)
	e.logTopN("MomentumEngine", tag, candidates, 10)

	return topN(candidates, 10), nil
}

// ── Momentum component scorers ────────────────────────────────────────────────

// computeBreakoutStrength measures how close the price is to the 52-week high.
// At the high → 1.0, far below → 0.0.
func computeBreakoutStrength(snap *models.TradingViewSnapshotDaily) float64 {
	dist := safeDeref(snap.Distance52wHigh, -100)
	// distance_52w_high: 0 = at high, negative = below.
	// Map [-30, 0] to [0, 1]: near the high = strong breakout.
	return normalizeMinMax(dist, -30, 0)
}

// computeRelativeStrength uses 3-month and 6-month performance with
// non-linear mapping that gives more resolution to the high-RS tail.
func computeRelativeStrength(snap *models.TradingViewSnapshotDaily) float64 {
	perf3m := safeDeref(snap.Perf3M, 0)
	perf6m := safeDeref(snap.Perf6M, 0)

	// Weight recent performance more heavily.
	blended := 0.65*perf3m + 0.35*perf6m

	// Two-segment normalisation:
	//   [-20%, 0%]    → [0.0, 0.2]   (laggards get compressed)
	//   [0%, +30%]    → [0.2, 0.6]   (average performers)
	//   [+30%, +100%] → [0.6, 1.0]   (leaders get full resolution)
	switch {
	case blended <= 0:
		return normalizeMinMax(blended, -20, 0) * 0.2
	case blended <= 30:
		return 0.2 + normalizeMinMax(blended, 0, 30)*0.4
	default:
		return 0.6 + normalizeMinMax(blended, 30, 100)*0.4
	}
}

// computeVolumeExpansion normalises relative volume for momentum context.
func computeVolumeExpansion(snap *models.TradingViewSnapshotDaily) float64 {
	relVol := safeDeref(snap.RelativeVolume, 1.0)
	// Map [0.5, 5.0] to [0, 1].
	return normalizeMinMax(relVol, 0.5, 5.0)
}

// computeVolumePriceConfirmation checks if volume expansion confirms
// the price trend. High volume on an up day near the 52-week high is
// accumulation; high volume on a down day is distribution.
func computeVolumePriceConfirmation(snap *models.TradingViewSnapshotDaily) float64 {
	relVol := safeDeref(snap.RelativeVolume, 1.0)
	changePct := safeDeref(snap.ChangePct, 0)
	gapPct := safeDeref(snap.GapPct, 0)

	// Volume expansion score: [0.5, 5.0] → [0, 1]
	volScore := normalizeMinMax(relVol, 0.5, 5.0)

	// Direction confirmation: positive change + positive gap = accumulation
	var directionMult float64
	switch {
	case changePct > 2.0 && gapPct > 0:
		directionMult = 1.0 // strong up day with gap up
	case changePct > 0.5:
		directionMult = 0.8 // positive close
	case changePct >= -0.5:
		directionMult = 0.5 // flat — ambiguous
	case changePct >= -2.0:
		directionMult = 0.2 // mild distribution
	default:
		directionMult = 0.0 // strong down day: volume is distribution
	}

	return volScore * directionMult
}

// computeTrendConsistency uses RSI-14 and distance from 52-week high to
// gauge trend consistency. For momentum stocks, high RSI (60-85) indicates
// strong persistent demand — only extreme readings (>85) with distance
// from highs suggest exhaustion.
func computeTrendConsistency(snap *models.TradingViewSnapshotDaily) float64 {
	rsi := safeDeref(snap.RSI14, 50)
	var rsiScore float64
	switch {
	case rsi >= 60 && rsi <= 85:
		rsiScore = 1.0 // sweet spot: strong demand, not exhausted
	case rsi > 85:
		rsiScore = 0.7 // very extended but still trending
	case rsi >= 50 && rsi < 60:
		rsiScore = 0.6 // building momentum
	case rsi >= 40 && rsi < 50:
		rsiScore = 0.3 // weak momentum
	default:
		rsiScore = 0.1 // below 40: no momentum
	}

	dist := safeDeref(snap.Distance52wHigh, -50)
	distScore := normalizeMinMax(dist, -25, 0)

	return 0.50*rsiScore + 0.50*distScore
}

// computeExtensionPenalty returns a multiplier (0.5–1.0) that penalises stocks
// where the breakout already occurred and the entry pivot is gone.
//
// Penalty triggers when ALL of:
//   - Near 52W high (dist > -5%)
//   - RSI > 75 (already extended)
//   - Price > 15% above SMA50 (far from support)
//   - Perf3M > 25% (big recent move already happened)
//
// Stocks in tight bases near highs (low RSI, tight to SMAs) are NOT penalised.
func computeExtensionPenalty(snap *models.TradingViewSnapshotDaily) float64 {
	dist := safeDeref(snap.Distance52wHigh, -50)
	rsi := safeDeref(snap.RSI14, 50)
	perf3m := safeDeref(snap.Perf3M, 0)
	price := safeDeref(snap.Close, 0)
	sma50 := safeDeref(snap.SMA50, 0)

	// Not near highs → no penalty.
	if dist < -5.0 {
		return 1.0
	}

	// Compute extension above SMA50.
	extAboveSMA50 := 0.0
	if sma50 > 0 && price > 0 {
		extAboveSMA50 = (price - sma50) / sma50 * 100
	}

	// Count how many "already broke out" signals fire.
	signals := 0
	if rsi > 75 {
		signals++
	}
	if extAboveSMA50 > 15 {
		signals++
	}
	if perf3m > 25 {
		signals++
	}

	switch signals {
	case 3:
		return 0.55 // heavily extended — strong penalty
	case 2:
		return 0.75 // moderately extended
	case 1:
		return 0.90 // mildly extended
	default:
		return 1.0 // near highs but not extended — ideal setup
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func buildTickerSet(snaps []models.TradingViewSnapshotDaily) map[string]bool {
	m := make(map[string]bool, len(snaps))
	for i := range snaps {
		m[snaps[i].Ticker] = true
	}
	return m
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func (e *MomentumEngine) loadRegimeMultiplier(ctx context.Context, date time.Time, tag string) (float64, error) {
	regime, err := e.regime.GetMarketRegimeDaily(ctx, date)
	if err != nil {
		e.log.Warn("MomentumEngine: could not load regime, defaulting to neutral", "date", tag, "error", err)
		return 1.0, nil
	}
	return regimeMultiplier(regime.Regime), nil
}

func (e *MomentumEngine) loadSectorLabels(ctx context.Context, date time.Time, tag string) (map[string]string, error) {
	scores, err := e.sectorScores.GetSectorScores(ctx, date)
	if err != nil {
		e.log.Warn("MomentumEngine: could not load sector scores, defaulting to neutral", "date", tag, "error", err)
		return map[string]string{}, nil
	}
	return buildSectorLabelMap(scores), nil
}

func (e *MomentumEngine) loadLeadersMap(ctx context.Context, date time.Time) (map[string]bool, error) {
	if e.leadership == nil {
		return map[string]bool{}, nil
	}
	leaders, err := e.leadership.GetLeadersForDate(ctx, date)
	if err != nil {
		e.log.Warn("MomentumEngine: could not load leadership map, defaulting to empty", "date", date.Format("2006-01-02"), "error", err)
		return map[string]bool{}, nil
	}
	return leaders, nil
}

func (e *MomentumEngine) logTopN(engine, tag string, candidates []RankedTicker, n int) {
	showing := n
	if len(candidates) < showing {
		showing = len(candidates)
	}
	tickers := make([]string, showing)
	scores := make([]float64, showing)
	for i := 0; i < showing; i++ {
		tickers[i] = candidates[i].Ticker
		scores[i] = candidates[i].Score
	}
	e.log.Info(engine+": candidates ranked",
		"date", tag,
		"total_candidates", len(candidates),
		"top_tickers", tickers,
		"top_scores", scores,
	)
}

// loadNarrativeScore loads the narrative velocity score for a ticker+date.
// Returns 0.0 if the source is nil, data is missing, or on any error.
func (e *MomentumEngine) loadNarrativeScore(ctx context.Context, ticker string, date time.Time) float64 {
	if e.narrativeVelocity == nil {
		return 0.0
	}
	rec, err := e.narrativeVelocity.GetByTickerDate(ctx, ticker, date)
	if err != nil {
		e.log.Debug("MomentumEngine: narrative velocity load failed",
			"ticker", ticker, "date", date.Format("2006-01-02"), "error", err)
		return 0.0
	}
	if rec == nil {
		return 0.0
	}
	return normalizeZeroOne(rec.NarrativeVelocity)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func filterBySource(snaps []models.TradingViewSnapshotDaily, source models.ScreenerSource) []models.TradingViewSnapshotDaily {
	var out []models.TradingViewSnapshotDaily
	for i := range snaps {
		if snaps[i].ScreenerSource == source {
			out = append(out, snaps[i])
		}
	}
	return out
}
