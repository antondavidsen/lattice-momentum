package regime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
)

// ── Data source interfaces ────────────────────────────────────────────────────

// MarketInputsSource provides market inputs data for breadth divergence detection.
type MarketInputsSource interface {
	GetMarketInputs(ctx context.Context, date time.Time) (*models.MarketInputsDaily, error)
}

// CandleSource provides SPY close prices for 5-day return calculation.
type CandleSource interface {
	GetIndexHistory(ctx context.Context, ticker string, days int) ([]models.CandleDaily, error)
}

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// breadthDivergenceLookbackDays is the number of trading days to fetch for
	// 5-day return calculation. We need at least 6 to compute (t / t-5).
	breadthDivergenceLookbackDays = 10

	// spyReturnThreshold is the minimum SPY 5-day return to trigger divergence
	// detection. SPY must be up more than +1%.
	spyReturnThreshold = 0.01 // 1%
)

// ── Service ───────────────────────────────────────────────────────────────────

// BreadthDivergenceService detects when SPY is up but market breadth is
// flat/falling — a classic distribution warning signal.
type BreadthDivergenceService struct {
	inputs  MarketInputsSource
	candles CandleSource
	log     *slog.Logger
}

// NewBreadthDivergenceService constructs a BreadthDivergenceService.
func NewBreadthDivergenceService(
	inputs MarketInputsSource,
	candles CandleSource,
	log *slog.Logger,
) *BreadthDivergenceService {
	return &BreadthDivergenceService{
		inputs:  inputs,
		candles: candles,
		log:     log,
	}
}

// Compute returns 1.0 if breadth divergence is detected on the given date,
// 0.0 otherwise.
//
// Divergence condition:
//
//	SPY 5-day return > +1%  AND  NYSE A/D line 5-day change <= 0
//
// The NYSE A/D line is proxied by breadth_above_50 (percentage of stocks above
// their 50-day SMA), which measures the same concept of market participation.
//
// Returns 0.0 with nil error when data is missing (logs a warning).
func (s *BreadthDivergenceService) Compute(ctx context.Context, date time.Time) (float64, error) {
	tag := date.Format("2006-01-02")

	// ── Step 1: Load SPY candles for 5-day return ──────────────────────────────
	spyCandles, err := s.candles.GetIndexHistory(ctx, "SPY", breadthDivergenceLookbackDays)
	if err != nil {
		s.log.Warn("breadth divergence: SPY candle data unavailable",
			"date", tag, "error", err)
		return 0, nil
	}
	if len(spyCandles) < 6 {
		s.log.Warn("breadth divergence: insufficient SPY candles",
			"date", tag, "count", len(spyCandles))
		return 0, nil
	}

	spyCloses := extractCloses(spyCandles)
	spyReturn5d := perfReturn(spyCloses, 5)

	// ── Step 2: Load market inputs for breadth data ────────────────────────────
	inputs, err := s.inputs.GetMarketInputs(ctx, date)
	if err != nil {
		s.log.Warn("breadth divergence: market inputs unavailable",
			"date", tag, "error", err)
		return 0, nil
	}

	// ── Step 3: Load market inputs from 5 trading days ago for breadth change ──
	// We need breadth_above_50 from 5 sessions ago to compute the 5-day change.
	// Since we don't have a direct "get by date offset" method, we use the
	// current breadth_velocity_5d which is already the 5-session change.
	// If breadth_velocity_5d is 0 (not computed), we fall back to comparing
	// with the market inputs from 5 days ago.
	breadthChange5d := inputs.BreadthVelocity5d

	// If breadth velocity is 0 (not computed), try to compute it manually.
	if breadthChange5d == 0 {
		breadthChange5d = s.computeBreadthChange(ctx, date, inputs.BreadthAbove50)
	}

	// ── Step 4: Check divergence condition ─────────────────────────────────────
	// SPY 5-day return > +1%  AND  breadth 5-day change <= 0
	divergence := spyReturn5d > spyReturnThreshold && breadthChange5d <= 0

	var signal float64
	if divergence {
		signal = 1.0
	}

	s.log.Info("breadth divergence: computed",
		"date", tag,
		"spy_return_5d", fmt.Sprintf("%.4f", spyReturn5d),
		"breadth_change_5d", fmt.Sprintf("%.2f", breadthChange5d),
		"divergence", divergence,
		"signal", signal,
	)

	return signal, nil
}

// computeBreadthChange attempts to compute the 5-day change in breadth_above_50
// by fetching market inputs from 5 trading days ago.
func (s *BreadthDivergenceService) computeBreadthChange(ctx context.Context, date time.Time, currentBreadth float64) float64 {
	// Try to find market inputs from approximately 5 trading days ago.
	// We search backwards up to 10 calendar days to find a valid row.
	for offset := 7; offset <= 10; offset++ {
		pastDate := date.AddDate(0, 0, -offset)
		pastInputs, err := s.inputs.GetMarketInputs(ctx, pastDate)
		if err != nil || pastInputs == nil {
			continue
		}
		return currentBreadth - pastInputs.BreadthAbove50
	}
	return 0
}

// ── helpers (duplicated from services package to avoid import cycle) ──────────

// extractCloses returns a float64 slice of Close prices in ascending date order.
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
