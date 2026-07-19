package outcomes

import (
	"context"
	"time"

	"ai-stock-service/internal/models"
)

// ── Constants ─────────────────────────────────────────────────────────────────
const (
	// MaxExitDays is the maximum number of trading days to simulate.
	MaxExitDays = 20
)

// ── Types ─────────────────────────────────────────────────────────────────────

// ExitResult is the outcome of a single replayed trade.
type ExitResult struct {
	StopHit   bool
	T1Hit     bool
	T2Hit     bool
	ExitType  string // "stop", "target_2", "time"
	ExitPrice float64
	ExitDate  time.Time
	ActualRR  float64 // signed: (exit - entry) / (entry - stop)
}

// LoadCandlesFn is injected for testability.
type LoadCandlesFn func(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)

// ── ReplayExitSequence ────────────────────────────────────────────────────────

// ReplayExitSequence simulates a path-aware exit for a single recommended trade.
// It loads candles from entryDate to up to entryDate + 20 trading days.
// The entry is assumed to be at T+1 open (first trading day after signal).
//
// Algorithm (per spec):
//  1. Load candles for (ticker, entryDate .. entryDate + 20 trading days)
//  2. Iterate day by day.
//     - If low <= stopPrice: EXIT: StopHit=true, ExitPrice=stopPrice, ExitType="stop"
//     - If high >= target1: set T1Hit=true but CONTINUE
//     - If high >= target2: EXIT: T2Hit=true, ExitPrice=target2, ExitType="target_2"
//  3. If no exit after 20 days: ExitType="time", ExitPrice = close of last day
//  4. Compute ActualRR = (ExitPrice - entryPrice) / (entryPrice - stopPrice) [signed]
//
// Stop takes priority over targets on whipsaw days (same candle).
// Days with no candle data (gaps) are skipped.
func ReplayExitSequence(
	ctx context.Context,
	ticker string,
	entryDate time.Time,
	entryPrice float64,
	stopPrice float64,
	target1 float64,
	target2 float64,
	loadCandles LoadCandlesFn,
) (ExitResult, error) {
	zero := time.Time{}
	result := ExitResult{ExitType: "time", ActualRR: 0, ExitDate: zero}

	// Query candles from entryDate through entryDate + ~1.5× calendar days
	// to ensure we cover 20 trading days (worst case: ~28 calendar days).
	from := entryDate
	to := entryDate.AddDate(0, 0, MaxExitDays*2)

	candles, err := loadCandles(ctx, ticker, from, to)
	if err != nil {
		return result, err
	}

	// Filter to trading days starting from entryDate (inclusive).
	// Entry is typically T+1 open; we check trading days after that.
	var tradingDays []models.CandleDaily
	for i := range candles {
		if !candles[i].Date.Before(entryDate) {
			tradingDays = append(tradingDays, candles[i])
		}
	}

	if len(tradingDays) == 0 {
		// No data — return time exit with zero values
		result.ExitType = "time"
		return result, nil
	}

	// Cap at MaxExitDays trading days.
	if len(tradingDays) > MaxExitDays {
		tradingDays = tradingDays[:MaxExitDays]
	}

	// Use adjusted_close if available; fall back to close.
	exitPrice := func(c models.CandleDaily) float64 {
		if c.AdjustedClose != nil {
			return *c.AdjustedClose
		}
		return c.Close
	}

	lastDay := tradingDays[len(tradingDays)-1]

	// Day-by-day replay.
	for i := range tradingDays {
		c := &tradingDays[i]
		dayLow := c.Low
		dayHigh := c.High

		// 1. Check stop first (stop takes priority on whipsaw days).
		if dayLow <= stopPrice {
			result.StopHit = true
			result.ExitType = "stop"
			result.ExitPrice = stopPrice
			result.ExitDate = c.Date

			// If T1 was also hit on the same day, note it.
			if dayHigh >= target1 {
				result.T1Hit = true
			}
			break
		}

		// 2. Check T1 (informational only, continue tracking).
		if dayHigh >= target1 {
			result.T1Hit = true
		}

		// 3. Check T2 (full exit).
		if dayHigh >= target2 && target2 > 0 {
			result.T2Hit = true
			result.ExitType = "target_2"
			result.ExitPrice = target2
			result.ExitDate = c.Date
			break
		}
	}

	// If no stop or T2 hit, exit at close of last available trading day.
	if result.ExitType == "time" {
		result.ExitPrice = exitPrice(lastDay)
		result.ExitDate = lastDay.Date
	}

	// Compute ActualRR = (exitPrice - entryPrice) / (entryPrice - stopPrice) [signed].
	risk := entryPrice - stopPrice
	if risk != 0 {
		result.ActualRR = (result.ExitPrice - entryPrice) / risk
	}

	return result, nil
}
