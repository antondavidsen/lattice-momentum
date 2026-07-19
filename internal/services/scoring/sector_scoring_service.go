// Package scoring implements the Sector Scoring engine (Epic 4, Ticket 4.2).
//
// Deprecated: Use internal/services/sector/sector_momentum_service.go instead.
// This standalone service is being consolidated into the nightly pipeline service.
// Tie-aware PercentileRanks() has moved to internal/indicators/percentile.go.
// The canonical ETF universe is now at internal/models/sector_etfs.go.
//
// TODO: Remove in next major version.
//
// It transforms candles_daily data (sector ETFs + SPY benchmark) into a ranked
// sector scoring snapshot stored in sector_scores_daily.
//
// This job is pure analytics: reads candles → computes metrics → returns snapshot.
// All scoring logic is broken into small, pure, testable functions.
package scoring

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"ai-stock-service/internal/indicators"
	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// minHistoryBars is the minimum number of candles required per ETF.
	// SMA-200 is the longest look-back, so we need at least 200 bars.
	minHistoryBars = 200

	// historyDays is how many trading-day candles to request from the repo.
	// 250 ≈ one trading year, leaving headroom above the 200-bar SMA minimum.
	historyDays = 250

	// Performance look-back periods (trading sessions).
	perf1MPeriod = 21
	perf3MPeriod = 63
)

// Composite score weights.
const (
	weightRS     = 0.40
	weightPerf3M = 0.25
	weightPerf1M = 0.15
	weightTrend  = 0.20
)

// ── Internal types ────────────────────────────────────────────────────────────

// sectorHistory bundles an ETF symbol with its loaded candle data.
type sectorHistory struct {
	etf     string
	candles []models.CandleDaily
}

// rawSectorMetrics holds per-ETF metrics before cross-section ranking.
type rawSectorMetrics struct {
	etf      string
	perf1m   float64
	perf3m   float64
	rs3m     float64
	trend    float64
	above50  bool
	above200 bool
}

// ── Repository interface ──────────────────────────────────────────────────────

// sectorDataSource is the read-only subset of repository.MarketDataRepo that
// the scoring service needs.
type sectorDataSource interface {
	GetIndexHistory(ctx context.Context, ticker string, days int) ([]models.CandleDaily, error)
}

// Compile-time assertion.
var _ sectorDataSource = (*repository.MarketDataRepo)(nil)

// ── Service ───────────────────────────────────────────────────────────────────

// Service builds daily sector scores from candle data.
type Service struct {
	repo sectorDataSource
	log  *slog.Logger
}

// NewScoringService constructs a ScoringService backed by the production
// repository.
func NewScoringService(
	repo *repository.MarketDataRepo,
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, log: log}
}

// NewScoringServiceFromSource constructs a ScoringService from any value that
// satisfies the sectorDataSource interface.  Intended for tests.
func NewScoringServiceFromSource(
	src sectorDataSource,
	log *slog.Logger,
) *Service {
	return &Service{repo: src, log: log}
}

// BuildSectorScores computes sector scores for all tracked sector ETFs as of
// the given date.
//
// Algorithm:
//  1. Load SPY + all sector ETF candle histories (250 days).
//  2. Compute per-ETF: perf_1m, perf_3m, rs_vs_spy_3m, SMA50/200, trend_score.
//  3. Cross-section ranking: rank perf_1m, perf_3m, rs_vs_spy_3m → percentile.
//  4. Final score = weighted sum of rank percentiles + trend_score.
//  5. Assign label based on score thresholds.
//
// Returns scores sorted by Score descending (leader first).
func (s *Service) BuildSectorScores(ctx context.Context, date time.Time) ([]models.SectorScoreDaily, error) {
	tag := date.Format("2006-01-02")

	// ── Step 1: Load histories ────────────────────────────────────────────────
	sectors, spyCandles, err := s.loadSectorHistories(ctx)
	if err != nil {
		return nil, fmt.Errorf("BuildSectorScores [%s]: %w", tag, err)
	}

	// ── Step 2: Compute raw metrics ───────────────────────────────────────────
	metrics := computeRawMetrics(sectors, spyCandles)

	s.log.Info("sector scoring: raw metrics computed",
		"date", tag,
		"sectors", len(metrics),
	)

	// ── Step 3+4+5: Build final scores (ranking + weighted composite + label)
	scores := buildFinalScores(date, metrics)

	if len(scores) > 0 {
		n := len(scores)
		s.log.Info("sector scoring: complete",
			"date", tag,
			"top_sector", scores[0].ETF,
			"top_score", fmt.Sprintf("%.4f", scores[0].Score),
			"top_label", scores[0].Label,
			"bottom_sector", scores[n-1].ETF,
			"bottom_score", fmt.Sprintf("%.4f", scores[n-1].Score),
			"bottom_label", scores[n-1].Label,
		)
	}

	return scores, nil
}

// ── Step A — load histories ───────────────────────────────────────────────────

// loadSectorHistories fetches SPY + all sector ETF candle data from the repo.
// Returns an error if any ETF has fewer than minHistoryBars candles.
func (s *Service) loadSectorHistories(
	ctx context.Context,
) ([]sectorHistory, []models.CandleDaily, error) {
	// Load SPY history
	spyCandles, err := s.repo.GetIndexHistory(ctx, "SPY", historyDays)
	if err != nil {
		return nil, nil, fmt.Errorf("SPY history: %w", err)
	}
	if len(spyCandles) < minHistoryBars {
		return nil, nil, fmt.Errorf(
			"SPY has only %d candles (need ≥ %d) — run backfill first",
			len(spyCandles), minHistoryBars,
		)
	}

	// Load all sector ETFs
	sectors := make([]sectorHistory, 0, len(models.SectorETFs))
	for _, etf := range models.SectorETFs {
		candles, err := s.repo.GetIndexHistory(ctx, etf, historyDays)
		if err != nil {
			return nil, nil, fmt.Errorf("%s history: %w", etf, err)
		}
		if len(candles) < minHistoryBars {
			return nil, nil, fmt.Errorf(
				"%s has only %d candles (need ≥ %d) — run backfill first",
				etf, len(candles), minHistoryBars,
			)
		}
		sectors = append(sectors, sectorHistory{etf: etf, candles: candles})
	}

	return sectors, spyCandles, nil
}

// ── Step B — compute raw metrics ──────────────────────────────────────────────

// computeRawMetrics is a pure function that derives per-ETF performance,
// relative strength, and trend metrics from candle data.
func computeRawMetrics(sectors []sectorHistory, spy []models.CandleDaily) []rawSectorMetrics {
	spyCloses := extractCloses(spy)
	spyPerf3M := perfReturn(spyCloses, perf3MPeriod)

	metrics := make([]rawSectorMetrics, len(sectors))
	for i, sh := range sectors {
		closes := extractCloses(sh.candles)
		latest := closes[len(closes)-1]

		p1m := perfReturn(closes, perf1MPeriod)
		p3m := perfReturn(closes, perf3MPeriod)
		rs3m := p3m - spyPerf3M

		sma50 := indicators.ComputeSMA(closes, 50)
		sma200 := indicators.ComputeSMA(closes, 200)

		a50 := latest > sma50
		a200 := latest > sma200

		var trend float64
		switch {
		case a50 && a200:
			trend = 1.0
		case a50:
			trend = 0.6
		default:
			trend = 0.0
		}

		metrics[i] = rawSectorMetrics{
			etf:      sh.etf,
			perf1m:   p1m,
			perf3m:   p3m,
			rs3m:     rs3m,
			trend:    trend,
			above50:  a50,
			above200: a200,
		}
	}
	return metrics
}

// ── Step C — ranking helper ───────────────────────────────────────────────────

// PercentileRanks returns an aligned slice of percentile ranks [0,1] for the
// input values.  The highest value receives 1.0, the lowest receives 0.0.
// When n == 1, the single element receives 1.0.
//
// Deprecated: Use indicators.PercentileRanks() instead.
// This function is preserved for backward compatibility during migration.
// The canonical implementation has moved to internal/indicators/percentile.go.
//
// Tied values receive the mean of the ordinal ranks they span, so
// [10, 10, 10] → [0.5, 0.5, 0.5] rather than arbitrary distinct ranks.
// This prevents ranking instability for sectors with identical performance.
func PercentileRanks(values []float64) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []float64{1.0}
	}

	// Build index-value pairs and sort by value ascending.
	type iv struct {
		idx int
		val float64
	}
	pairs := make([]iv, n)
	for i, v := range values {
		pairs[i] = iv{idx: i, val: v}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].val < pairs[j].val })

	// Assign average rank for tied values.
	// After ascending sort, position 0 = worst, position n-1 = best.
	// Groups of identical values share the mean of their ordinal positions.
	out := make([]float64, n)
	for i := 0; i < n; {
		// Find the end of this tie group.
		j := i + 1
		for j < n && pairs[j].val == pairs[i].val {
			j++
		}
		// Average ordinal rank for positions i..j-1.
		var sumRank float64
		for k := i; k < j; k++ {
			sumRank += float64(k)
		}
		avgPctile := (sumRank / float64(j-i)) / float64(n-1)
		for k := i; k < j; k++ {
			out[pairs[k].idx] = avgPctile
		}
		i = j
	}
	return out
}

// ── Step D — final score builder ──────────────────────────────────────────────

// buildFinalScores computes ranks, calculates weighted scores, assigns labels,
// and returns the result sorted by Score descending.
// Pure function → easy to test.
func buildFinalScores(
	runDate time.Time,
	metrics []rawSectorMetrics,
) []models.SectorScoreDaily {
	n := len(metrics)
	if n == 0 {
		return nil
	}

	// Extract values for ranking.
	perf1mVals := make([]float64, n)
	perf3mVals := make([]float64, n)
	rs3mVals := make([]float64, n)
	for i, m := range metrics {
		perf1mVals[i] = m.perf1m
		perf3mVals[i] = m.perf3m
		rs3mVals[i] = m.rs3m
	}

	perf1mRanks := PercentileRanks(perf1mVals)
	perf3mRanks := PercentileRanks(perf3mVals)
	rs3mRanks := PercentileRanks(rs3mVals)

	scores := make([]models.SectorScoreDaily, n)
	for i, m := range metrics {
		raw := weightRS*rs3mRanks[i] +
			weightPerf3M*perf3mRanks[i] +
			weightPerf1M*perf1mRanks[i] +
			weightTrend*m.trend

		// Clamp to [0, 1].
		score := math.Max(0, math.Min(1, raw))

		scores[i] = models.SectorScoreDaily{
			Date:        runDate,
			ETF:         m.etf,
			Perf1M:      m.perf1m,
			Perf3M:      m.perf3m,
			RSvsSPY3M:   m.rs3m,
			AboveSMA50:  m.above50,
			AboveSMA200: m.above200,
			TrendScore:  m.trend,
			Score:       score,
			Label:       models.SectorScoreLabel(score),
		}
	}

	// Sort by score descending (leader first).
	sort.Slice(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })

	return scores
}

// ── private helpers ───────────────────────────────────────────────────────────

// extractCloses returns a float64 slice of Close prices in the same order as
// the input (ascending by date, oldest → newest).
func extractCloses(candles []models.CandleDaily) []float64 {
	out := make([]float64, len(candles))
	for i := range candles {
		out[i] = candles[i].Close
	}
	return out
}

// perfReturn computes (latest close / close N periods ago) − 1.
// Returns 0 when there are insufficient data points or the reference close is 0.
func perfReturn(closes []float64, period int) float64 {
	if len(closes) <= period {
		return 0
	}
	ref := closes[len(closes)-1-period]
	if ref == 0 {
		return 0
	}
	return closes[len(closes)-1]/ref - 1
}
