// Package sector implements the Sector Momentum Scoring engine (Epic 4,
// Ticket 4.1).
//
// It transforms daily_sectors / candles_daily data into a ranked sector
// momentum snapshot stored in sector_scores_daily.
package sector

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

// ── Output type ───────────────────────────────────────────────────────────────

// Score holds the computed momentum metrics for a single sector ETF on a
// given trading day.  It is the service's output type; the job converts it to
// models.SectorScoreDaily before persisting.
type Score struct {
	Date time.Time
	ETF  string

	Perf1M    float64 // 21-session return
	Perf3M    float64 // 63-session return
	RSvsSPY3M float64 // perf_3m minus spy_perf_3m

	AboveSMA50  bool
	AboveSMA200 bool

	TrendScore float64 // 0.0 | 0.6 | 1.0
	Score      float64 // final weighted composite [0,1]
	Label      string  // LEADING | STRONG | NEUTRAL | WEAK | LAGGING
}

// ── Repository interface ──────────────────────────────────────────────────────

// sectorDataSource is the read-only subset of repository.MarketDataRepo that
// the sector momentum service needs.
type sectorDataSource interface {
	GetIndexHistory(ctx context.Context, ticker string, days int) ([]models.CandleDaily, error)
}

// Compile-time assertion.
var _ sectorDataSource = (*repository.MarketDataRepo)(nil)

// ── Service ───────────────────────────────────────────────────────────────────

// MomentumService builds daily sector momentum scores from candle data.
type MomentumService struct {
	repo sectorDataSource
	log  *slog.Logger
}

// NewMomentumService constructs a MomentumService backed by the production
// repository.
func NewMomentumService(
	repo *repository.MarketDataRepo,
	log *slog.Logger,
) *MomentumService {
	return &MomentumService{repo: repo, log: log}
}

// NewMomentumServiceFromSource constructs a MomentumService from any value that
// satisfies the sectorDataSource interface.  Intended for tests.
func NewMomentumServiceFromSource(
	src sectorDataSource,
	log *slog.Logger,
) *MomentumService {
	return &MomentumService{repo: src, log: log}
}

// BuildSectorScores computes sector momentum scores for all tracked sector ETFs
// as of the given date.
//
// Algorithm (see Epic 4 spec):
//  1. Load SPY + all sector ETF candle histories (250 days).
//  2. Compute per-ETF: perf_1m, perf_3m, rs_vs_spy_3m, SMA50/200, trend_score.
//  3. Cross-section ranking: rank perf_1m, perf_3m, rs_vs_spy_3m → percentile.
//  4. Final score = weighted sum of rank percentiles + trend_score.
//  5. Assign label based on score thresholds.
func (s *MomentumService) BuildSectorScores(ctx context.Context, date time.Time) ([]Score, error) {
	tag := date.Format("2006-01-02")

	// ── Step 1: Load SPY history ──────────────────────────────────────────────
	spyCandles, err := s.repo.GetIndexHistory(ctx, "SPY", historyDays)
	if err != nil {
		return nil, fmt.Errorf("BuildSectorScores [%s]: SPY history: %w", tag, err)
	}
	if len(spyCandles) < minHistoryBars {
		return nil, fmt.Errorf(
			"BuildSectorScores [%s]: SPY has only %d candles (need ≥ %d) — run backfill first",
			tag, len(spyCandles), minHistoryBars,
		)
	}
	spyCloses := extractCloses(spyCandles)

	// ── Step 2: Load sector ETF histories & compute raw metrics ───────────────
	universe := models.SectorETFs
	type rawMetrics struct {
		etf      string
		perf1m   float64
		perf3m   float64
		rsSpy3m  float64
		above50  bool
		above200 bool
		trend    float64
	}

	metrics := make([]rawMetrics, 0, len(universe))

	// SPY performance for relative strength calculation.
	spyPerf3M := perfReturn(spyCloses, perf3MPeriod)

	for _, etf := range universe {
		candles, err := s.repo.GetIndexHistory(ctx, etf, historyDays)
		if err != nil {
			return nil, fmt.Errorf("BuildSectorScores [%s]: %s history: %w", tag, etf, err)
		}
		if len(candles) < minHistoryBars {
			return nil, fmt.Errorf(
				"BuildSectorScores [%s]: %s has only %d candles (need ≥ %d) — run backfill first",
				tag, etf, len(candles), minHistoryBars,
			)
		}

		closes := extractCloses(candles)
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

		metrics = append(metrics, rawMetrics{
			etf:      etf,
			perf1m:   p1m,
			perf3m:   p3m,
			rsSpy3m:  rs3m,
			above50:  a50,
			above200: a200,
			trend:    trend,
		})
	}

	s.log.Info("sector momentum: raw metrics computed",
		"date", tag,
		"sectors", len(metrics),
	)

	// ── Step 3: Cross-section ranking ─────────────────────────────────────────
	// Uses indicators.PercentileRanks which handles tied values by assigning
	// the mean of their ordinal ranks (prevents ranking instability for sectors
	// with identical performance).
	n := len(metrics)
	perf1mVals := make([]float64, n)
	perf3mVals := make([]float64, n)
	rsSpy3mVals := make([]float64, n)
	for i, m := range metrics {
		perf1mVals[i] = m.perf1m
		perf3mVals[i] = m.perf3m
		rsSpy3mVals[i] = m.rsSpy3m
	}
	perf1mRanks := indicators.PercentileRanks(perf1mVals)
	perf3mRanks := indicators.PercentileRanks(perf3mVals)
	rsSpy3mRanks := indicators.PercentileRanks(rsSpy3mVals)

	// ── Step 4 + 5: Final score & label ───────────────────────────────────────
	scores := make([]Score, n)
	for i, m := range metrics {
		raw := weightRS*rsSpy3mRanks[i] +
			weightPerf3M*perf3mRanks[i] +
			weightPerf1M*perf1mRanks[i] +
			weightTrend*m.trend

		// Clamp to [0, 1].
		score := math.Max(0, math.Min(1, raw))

		scores[i] = Score{
			Date:        date,
			ETF:         m.etf,
			Perf1M:      m.perf1m,
			Perf3M:      m.perf3m,
			RSvsSPY3M:   m.rsSpy3m,
			AboveSMA50:  m.above50,
			AboveSMA200: m.above200,
			TrendScore:  m.trend,
			Score:       score,
			Label:       models.SectorScoreLabel(score),
		}
	}

	// Sort by score descending for logging convenience.
	sort.Slice(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })

	if len(scores) > 0 {
		s.log.Info("sector momentum: scoring complete",
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
