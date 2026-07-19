// Package ranking implements the Daily Ranking Engine (multi-list output).
//
// This public subset ships one engine:
//
//   - Momentum List  — pure technical breakout leaders
//
// Each engine implements the Engine interface so the orchestrating job
// can treat them uniformly.
package ranking

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"ai-stock-service/internal/models"
)

// ── Core types ────────────────────────────────────────────────────────────────

// RankedTicker is the output of a single ranking engine for one ticker.
type RankedTicker struct {
	Ticker string
	Score  float64
	Reason map[string]float64 // component scores for auditability
}

// Engine is the interface implemented by each of the three engines.
type Engine interface {
	// Compute produces a ranked list of tickers for the given date.
	// The returned slice is sorted by score descending (best first).
	Compute(ctx context.Context, date time.Time) ([]RankedTicker, error)
}

// ── Data source interfaces ────────────────────────────────────────────────────

// SnapshotSource loads TradingView daily snapshots for a given date.
type SnapshotSource interface {
	ListByDate(ctx context.Context, date time.Time) ([]models.TradingViewSnapshotDaily, error)
}

// RegimeSource loads the market regime classification for a given date.
type RegimeSource interface {
	GetMarketRegimeDaily(ctx context.Context, date time.Time) (*models.MarketRegimeDaily, error)
}

// SectorScoreSource loads sector scores for a given date.
type SectorScoreSource interface {
	GetSectorScores(ctx context.Context, date time.Time) ([]models.SectorScoreDaily, error)
}

// LeadershipSource loads sector leadership flags for a given date.
// Used by all three engines to apply a 5% score boost for sector leaders.
type LeadershipSource interface {
	GetLeadersForDate(ctx context.Context, date time.Time) (map[string]bool, error)
}

// NarrativeVelocitySource loads per-ticker narrative velocity scores.
// Used by the EP and Momentum engines to apply a narrative velocity multiplier
// bonus to event quality / breakout strength.
// Optional — nil means no narrative velocity data (bonus defaults to 1.0).
type NarrativeVelocitySource interface {
	GetByTickerDate(ctx context.Context, ticker string, date time.Time) (*models.NarrativeVelocityDaily, error)
}

// CandleReader loads daily candles for a ticker for pennant detection.
type CandleReader interface {
	GetCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}

// Compile-time assertion: all three engines implement Engine.
var _ Engine = (*MomentumEngine)(nil)

// ── Regime multiplier ─────────────────────────────────────────────────────────

// regimeMultiplier returns the scoring multiplier for a given regime label.
//
//	strong_bull → 1.15
//	bull        → 1.05
//	neutral     → 1.00
//	correction  → 0.85
//	bear        → 0.70
func regimeMultiplier(regime string) float64 {
	switch strings.ToLower(regime) {
	case "strong_bull":
		return 1.15
	case "bull":
		return 1.05
	case "neutral":
		return 1.00
	case "correction":
		return 0.85
	case "bear":
		return 0.70
	default:
		return 1.00
	}
}

// ── Sector multiplier ─────────────────────────────────────────────────────────

// sectorMultiplier returns the scoring multiplier for a given sector label.
//
//	LEADING  → +10% (1.10)
//	STRONG   → +5%  (1.05)
//	NEUTRAL  → 0%   (1.00)
//	WEAK     → -5%  (0.95)
//	LAGGING  → -15% (0.85)
func sectorMultiplier(label string) float64 {
	switch strings.ToUpper(label) {
	case "LEADING":
		return 1.10
	case "STRONG":
		return 1.05
	case "NEUTRAL":
		return 1.00
	case "WEAK":
		return 0.95
	case "LAGGING":
		return 0.85
	default:
		return 1.00
	}
}

// sectorETFMap maps TradingView sector names to their corresponding sector ETF.
var sectorETFMap = map[string]string{
	"technology":             "XLK",
	"healthcare":             "XLV",
	"financial services":     "XLF",
	"financials":             "XLF",
	"consumer cyclical":      "XLY",
	"consumer discretionary": "XLY",
	"industrials":            "XLI",
	"energy":                 "XLE",
	"basic materials":        "XLB",
	"materials":              "XLB",
	"utilities":              "XLU",
	"consumer defensive":     "XLP",
	"consumer staples":       "XLP",
	"real estate":            "XLRE",
	"communication services": "XLC",
}

// lookupSectorLabel finds the sector score label for a ticker given its sector
// name and the day's sector scores.  Returns "NEUTRAL" if no mapping found.
func lookupSectorLabel(tickerSector string, sectorScores map[string]string) string {
	if tickerSector == "" {
		return "NEUTRAL"
	}
	etf, ok := sectorETFMap[strings.ToLower(tickerSector)]
	if !ok {
		return "NEUTRAL"
	}
	if label, ok := sectorScores[etf]; ok {
		return label
	}
	return "NEUTRAL"
}

// buildSectorLabelMap converts a slice of SectorScoreDaily to a map of
// ETF → label for quick lookups.
func buildSectorLabelMap(scores []models.SectorScoreDaily) map[string]string {
	m := make(map[string]string, len(scores))
	for i := range scores {
		m[scores[i].ETF] = scores[i].Label
	}
	return m
}

// ── Scoring helpers ───────────────────────────────────────────────────────────

// normalizeZeroOne clamps v to [0, 1].
func normalizeZeroOne(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// normalizeMinMax normalizes v from [nMin, nMax] to [0, 1].
// Returns 0 if nMin == nMax or v < nMin, 1 if v > nMax.
func normalizeMinMax(v, nMin, nMax float64) float64 {
	if nMax <= nMin {
		return 0
	}
	return normalizeZeroOne((v - nMin) / (nMax - nMin))
}

// safeDeref returns the value pointed to by p, or fallback if p is nil.
func safeDeref(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}

// safeDerefStr returns the value pointed to by p, or fallback if p is nil.
func safeDerefStr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// avgDollarVolume estimates average daily dollar volume from the snapshot.
// Uses avg_volume_10d × close as proxy. Returns 0 if data is missing.
func avgDollarVolume(snap *models.TradingViewSnapshotDaily) float64 {
	if snap.AvgVolume10d == nil || snap.Close == nil {
		return 0
	}
	return float64(*snap.AvgVolume10d) * *snap.Close
}

// MustJSON marshals v to JSON; returns "{}" on error.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// topN returns at most n elements from the sorted (descending) input.
func topN(ranked []RankedTicker, n int) []RankedTicker {
	if len(ranked) <= n {
		return ranked
	}
	return ranked[:n]
}

// sortDescending sorts ranked tickers by score descending, with stable
// tie-breaking on ticker name (alphabetical ascending) for determinism.
func sortDescending(ranked []RankedTicker) {
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Ticker < ranked[j].Ticker
	})
}

// scaleScore multiplies the raw score by regime and sector multipliers,
// then scales to a 0–100 display range. Capped at 100 to keep the
// displayed "X / 10" score within bounds.
func scaleScore(raw, regimeMult, sectorMult float64) float64 {
	s := raw * regimeMult * sectorMult * 100
	if s > 100 {
		return 100
	}
	return s
}
