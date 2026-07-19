// Package outcomes implements the Trade Outcome Calculator (Epic 6, Tickets 6.1 & 6.2).
//
// Entry Model (Ticket 6.2):
//
//	Signal published after market close on day T.
//	Official entry price = T+1 Open (next trading day's opening price).
//	Tracking window = T+1 through T+20 (20 trading days).
//
// Metrics:
//
//	MRU = (highest high T+1..T+20 − entry) / entry (continuous)
//	MDD = (lowest low T+1..T+20 − entry) / entry (continuous)
//	1D-4D = (close day N − entry) / entry (fixed horizon, daily granularity)
//	5D = (close day 5 − entry) / entry (fixed horizon)
//	10D = (close day 10 − entry) / entry (fixed horizon)
//	20D = (close day 20 − entry) / entry (fixed horizon)
package outcomes

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// ── Constants ─────────────────────────────────────────────────────────────────
const (
	// Horizon defines the maximum evaluation window in trading days.
	Horizon = 20

	// Return checkpoints (trading days after entry).
	return1D  = 1
	return2D  = 2
	return3D  = 3
	return4D  = 4
	return5D  = 5
	return10D = 10
	return20D = 20
)

// ── Plausibility thresholds ───────────────────────────────────────────────────
const (
	// MaxPlausibleOneDayReturn caps absolute 1D return (75%).
	MaxPlausibleOneDayReturn = 0.75

	// MaxPlausibleTwentyDReturn caps 20D return (200%).
	MaxPlausibleTwentyDReturn = 2.00

	// MinPlausibleTwentyDReturn floors 20D return (-70%).
	MinPlausibleTwentyDReturn = -0.70
)

// ── Repository interfaces ─────────────────────────────────────────────────────
// rankListReader is the read-only subset of RankListRepo the service needs.
type rankListReader interface {
	GetAllRankLists(ctx context.Context, date time.Time) ([]models.DailyRankList, error)
}

// candleReader is the read-only subset of CandlesDailyRepo the service needs.
type candleReader interface {
	GetCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}

// corporateActionReader is the subset of CorporateActionRepo to query actions.
type corporateActionReader interface {
	GetByTickerDateRange(ctx context.Context, ticker string, start, end time.Time) ([]models.CorporateAction, error)
}

// candleCloseReader reads candles for a ticker+date range to check adjusted_close.
type candleCloseReader interface {
	GetCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}

// Compile-time assertions.
var _ rankListReader = (*repository.RankListRepo)(nil)
var _ candleReader = (*repository.CandlesDailyRepo)(nil)
var _ corporateActionReader = (*repository.CorporateActionRepo)(nil)
var _ candleCloseReader = (*repository.CandlesDailyRepo)(nil)

// TradeOutcomeResult ── Output type ───────────────────────────────────────────────────────────────
// TradeOutcomeResult holds the computed forward performance for a single
// ranked signal. The job layer converts this to models.TradeOutcomeDaily
// before persisting.
type TradeOutcomeResult struct {
	EntryDate      time.Time
	ListType       models.ListType
	Ticker         string
	Rank           int
	EntryPrice     float64
	Return1D       *float64
	Return2D       *float64
	Return3D       *float64
	Return4D       *float64
	Return5D       *float64
	Return10D      *float64
	Return20D      *float64
	MaxRunup20D    *float64
	MaxDrawdown20D *float64
	EvaluatedDays  int
}

// TradeOutcomeService ── Service ───────────────────────────────────────────────────────────────────
// TradeOutcomeService computes forward performance metrics for rank-list signals.
type TradeOutcomeService struct {
	ranks        rankListReader
	candles      candleReader
	caReader     corporateActionReader
	candleCloser candleCloseReader // reads candles_daily for adjusted_close check
	log          *slog.Logger
}

// NewTradeOutcomeService constructs a service from the production concrete types.
func NewTradeOutcomeService(
	ranks *repository.RankListRepo,
	candles *repository.CandlesDailyRepo,
	log *slog.Logger,
) *TradeOutcomeService {
	return &TradeOutcomeService{ranks: ranks, candles: candles, log: log}
}

// NewTradeOutcomeServiceFromSources constructs a service from any values that
// satisfy the reader interfaces. Intended for tests.
func NewTradeOutcomeServiceFromSources(
	ranks rankListReader,
	candles candleReader,
	log *slog.Logger,
) *TradeOutcomeService {
	return &TradeOutcomeService{ranks: ranks, candles: candles, log: log}
}

// WithCorporateActionReader sets the corporate action reader for adjusting returns.
func (s *TradeOutcomeService) WithCorporateActionReader(caReader corporateActionReader) *TradeOutcomeService {
	s.caReader = caReader
	return s
}

// WithCandleCloseReader sets the adjusted_close candle reader for corporate action detection.
func (s *TradeOutcomeService) WithCandleCloseReader(candleReader candleCloseReader) *TradeOutcomeService {
	s.candleCloser = candleReader
	return s
}

// AdjustForCorporateActions queries corporate_actions in the window [entryDate, entryDate+horizonDays]
// and adjusts the raw return by compounding the correction factor.
//
// Prefers candles_daily.adjusted_close when non-null (meaning the data provider already
// adjusted the close); falls back to the corporate-action calendar.
//
// Returns: adjustedReturn, actionCount, error.
func (s *TradeOutcomeService) AdjustForCorporateActions(ctx context.Context, ticker string, entryDate time.Time, rawReturn float64, horizonDays int) (adjustedReturn float64, actionCount int, err error) {
	if s.caReader == nil {
		return rawReturn, 0, nil
	}

	endDate := entryDate.AddDate(0, 0, horizonDays*2) // generous calendar range

	// 1. Check if adjusted_close is already applied (non-null) → skip adjustment
	if s.candleCloser != nil {
		candles, err := s.candleCloser.GetCandles(ctx, ticker, entryDate, endDate)
		if err == nil {
			adjustedNonNull := false
			for i := range candles {
				if candles[i].AdjustedClose != nil {
					adjustedNonNull = true
					break
				}
			}
			if adjustedNonNull {
				// Data provider already adjusted the close — no need for manual adjustment
				return rawReturn, 0, nil
			}
		}
		// If GetCandles fails, fall through to calendar-based adjustment
	}

	// 2. Query corporate actions in the measurement window
	actions, err := s.caReader.GetByTickerDateRange(ctx, ticker, entryDate, endDate)
	if err != nil {
		return rawReturn, 0, fmt.Errorf("AdjustForCorporateActions query %s [%s..%s]: %w", ticker, entryDate.Format("2006-01-02"), endDate.Format("2006-01-02"), err)
	}

	if len(actions) == 0 {
		return rawReturn, 0, nil
	}

	// 3. Compound multiplicative adjustment
	// For a split (ratio=2): rawReturn = (close/entry - 1). The adjusted close
	// would be close/2, so adjustedReturn = rawReturn / R where R = split_to/split_from.
	// For a reverse split (ratio=0.5): adjustedReturn = rawReturn * R (since R < 1).
	adj := rawReturn
	for _, a := range actions {
		switch a.ActionType {
		case "split":
			// Forward split: stock divided, price goes down → overstates return
			adj /= a.Ratio
		case "reverse_split":
			// Reverse split: stock consolidated, price goes up → understates return
			adj *= a.Ratio
		default:
			s.log.Warn("unknown corporate action type", "type", a.ActionType, "ticker", ticker)
		}
	}

	return adj, len(actions), nil
}

// PlausibilityCheck evaluates whether a TradeOutcomeResult passes sanity checks.
// Returns a non-empty string reason if the result fails plausibility.
func (s *TradeOutcomeService) PlausibilityCheck(result *TradeOutcomeResult) string {
	if result.Return1D != nil {
		abs1D := *result.Return1D
		if abs1D < 0 {
			abs1D = -abs1D
		}
		if abs1D > MaxPlausibleOneDayReturn {
			return "one_day_return_exceeded"
		}
	}

	if result.Return20D != nil {
		if *result.Return20D > MaxPlausibleTwentyDReturn {
			return "twenty_day_return_high"
		}
		if *result.Return20D < MinPlausibleTwentyDReturn {
			return "twenty_day_return_low"
		}
	}

	return "" // plausible
}

// ComputeOutcomes calculates forward performance for every signal published on
// signalDate. It fetches the rank lists, then for each ticker retrieves candles
// from the entry date through the cutoff and computes the metrics.
//
// The today parameter defines the current pipeline date. Candles up to and
// including today are eligible. The nightly pipeline runs at 21:00 UTC (one
// hour after US market close), so today's bar is always a complete session
// that has already been ingested in earlier pipeline steps.
func (s *TradeOutcomeService) ComputeOutcomes(ctx context.Context, signalDate, today time.Time) ([]TradeOutcomeResult, error) {
	lists, err := s.ranks.GetAllRankLists(ctx, signalDate)
	if err != nil {
		return nil, fmt.Errorf("load rank lists for %s: %w", signalDate.Format("2006-01-02"), err)
	}
	if len(lists) == 0 {
		return nil, nil
	}

	// Use today as the candle cutoff (inclusive).
	cutoff := today
	calendarEnd := signalDate.AddDate(0, 0, Horizon*2+10)
	if calendarEnd.After(cutoff) {
		calendarEnd = cutoff
	}

	var results []TradeOutcomeResult
	for i := range lists {
		rl := &lists[i]
		candles, err := s.candles.GetCandles(ctx, rl.Ticker, signalDate, calendarEnd)
		if err != nil {
			s.log.Warn("skipping ticker: candle fetch failed", "ticker", rl.Ticker, "entry_date", signalDate.Format("2006-01-02"), "error", err)
			continue
		}

		result := s.evaluate(rl, candles, cutoff)
		if result.EntryPrice == 0 {
			s.log.Debug("skipping ticker: no T+1 candle available", "ticker", rl.Ticker, "signal_date", signalDate.Format("2006-01-02"))
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// evaluate computes the forward performance metrics for a single signal using
// the canonical entry model (Ticket 6.2):
//
//   - Entry price = T+1 Open (the first trading day after the signal date).
//   - Tracking candles = T+1 through T+20 (up to 20 trading days).
//   - MRU / MDD use all tracking candles' highs and lows (including T+1).
//   - Fixed-horizon returns use the close of the Nth tracking day.
//
// cutoff is the last date (inclusive) that may be used.
func (s *TradeOutcomeService) evaluate(rl *models.DailyRankList, candles []models.CandleDaily, cutoff time.Time) TradeOutcomeResult {
	result := TradeOutcomeResult{
		EntryDate: rl.Date,
		ListType:  rl.ListType,
		Ticker:    rl.Ticker,
		Rank:      rl.Rank,
	}

	if len(candles) == 0 {
		return result
	}

	// Discard any candle on or before the signal date and after cutoff.
	var trackingCandles []models.CandleDaily
	for i := range candles {
		if candles[i].Date.After(rl.Date) && !candles[i].Date.After(cutoff) {
			trackingCandles = append(trackingCandles, candles[i])
		}
	}
	if len(trackingCandles) == 0 {
		return result
	}

	// Entry price = T+1 Open (first tracking candle's opening price).
	entryPrice := trackingCandles[0].Open
	if entryPrice == 0 {
		return result
	}
	result.EntryPrice = entryPrice

	// Cap at the 20-day horizon.
	if len(trackingCandles) > Horizon {
		trackingCandles = trackingCandles[:Horizon]
	}
	result.EvaluatedDays = len(trackingCandles)

	// ── Fixed-horizon returns (close of day N vs entry) ───────────────────
	// Daily granularity for days 1-4
	if len(trackingCandles) >= return1D {
		r := (trackingCandles[return1D-1].Close - entryPrice) / entryPrice
		result.Return1D = &r
	}
	if len(trackingCandles) >= return2D {
		r := (trackingCandles[return2D-1].Close - entryPrice) / entryPrice
		result.Return2D = &r
	}
	if len(trackingCandles) >= return3D {
		r := (trackingCandles[return3D-1].Close - entryPrice) / entryPrice
		result.Return3D = &r
	}
	if len(trackingCandles) >= return4D {
		r := (trackingCandles[return4D-1].Close - entryPrice) / entryPrice
		result.Return4D = &r
	}
	// Existing 5d/10d/20d checkpoints
	if len(trackingCandles) >= return5D {
		r := (trackingCandles[return5D-1].Close - entryPrice) / entryPrice
		result.Return5D = &r
	}
	if len(trackingCandles) >= return10D {
		r := (trackingCandles[return10D-1].Close - entryPrice) / entryPrice
		result.Return10D = &r
	}
	if len(trackingCandles) >= return20D {
		r := (trackingCandles[return20D-1].Close - entryPrice) / entryPrice
		result.Return20D = &r
	}

	// ── Continuous metrics (MRU / MDD over entire tracking window) ────────
	maxHigh := trackingCandles[0].High
	minLow := trackingCandles[0].Low
	for i := range trackingCandles {
		if trackingCandles[i].High > maxHigh {
			maxHigh = trackingCandles[i].High
		}
		if trackingCandles[i].Low < minLow {
			minLow = trackingCandles[i].Low
		}
	}
	mru := (maxHigh - entryPrice) / entryPrice
	mdd := (minLow - entryPrice) / entryPrice
	result.MaxRunup20D = &mru
	result.MaxDrawdown20D = &mdd

	return result
}
