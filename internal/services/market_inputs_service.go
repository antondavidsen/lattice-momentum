// Package services provides high-level orchestration services that compose
// repositories and pure-function indicators into meaningful domain outputs.
package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/indicators"
	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// MinHistoryDays is the minimum number of trading-day candles fetched for every
// symbol. 250 sessions ≈ one full trading year, which guarantees that both
// SMA-50 and SMA-200 can always be derived from the returned history.
const MinHistoryDays = 250

// distributionDaysLookback is the IBD-standard rolling window used when
// counting distribution days.
const distributionDaysLookback = 20

// ── Output type ───────────────────────────────────────────────────────────────

// MarketInputs holds every pre-computed daily signal required for market regime
// classification.
//
// All fields are pure values derived from indicator functions.
// No scoring or regime label logic lives here — this is clean input data only.
type MarketInputs struct {
	// Date is the trading session these inputs describe (used for logging /
	// auditing; the candle window is always anchored to the latest N sessions).
	Date time.Time

	// ── v1 boolean flags (kept for backward compatibility) ───────────────────

	// Index position relative to moving averages.
	SpyAbove50  bool
	SpyAbove200 bool
	QqqAbove50  bool
	QqqAbove200 bool

	// DistributionDays counts IBD-style distribution days for SPY over the
	// last distributionDaysLookback sessions.
	DistributionDays int

	// Market breadth: percentage (0–100) of tracked stocks whose latest close
	// is above the given SMA.
	BreadthAbove50  float64
	BreadthAbove200 float64

	// Latest-session relative-strength ratios (dimensionless price ratios).
	//   > 1.0  →  numerator index outperforming SPY
	//   < 1.0  →  numerator index underperforming SPY
	QqqvsspyRs float64
	IwmvsspyRs float64

	// ── v2 continuous signals (migration 020) ────────────────────────────────

	// Percentage distance of the latest close from the given SMA.
	// Positive = above SMA, negative = below.
	// Example: +3.5 means the close is 3.5 % above the SMA.
	SpyPctFromSMA50  float64
	SpyPctFromSMA200 float64
	QqqPctFromSMA50  float64
	QqqPctFromSMA200 float64

	// Golden / death cross: TRUE when SMA-50 is above SMA-200.
	SpySMA50AboveSMA200 bool
	QqqSMA50AboveSMA200 bool

	// SpyDrawdownPct is SPY's trailing drawdown from the 252-session high,
	// expressed as a negative percentage (e.g. −15.0 = 15 % below the high).
	SpyDrawdownPct float64

	// ── R-02 regime signal enrichment ──────────────────────────────────────────

	// VIXLevel is the daily close of the VIX volatility index.
	// Higher values indicate elevated fear / hedging demand.
	VIXLevel float64

	// VIXROCpct is the 1-day percentage change in VIX close.
	// Positive = volatility spiking higher (bearish signal).
	VIXROCpct float64

	// TickMinDaily is the session low of the NYSE $TICK index.
	// Values below −600 indicate broad selling pressure.
	TickMinDaily float64

	// BreadthVelocity5d is the 5-session change in breadth_above_50
	// (percentage points). Positive = breadth improving; negative = deteriorating.
	BreadthVelocity5d float64
}

// ── Repository interface ──────────────────────────────────────────────────────

// marketDataSource is the read-only subset of repository.MarketDataRepo that
// MarketInputsService actually needs.
// Defining it as an interface allows the service to be tested with a mock
// without requiring a live database connection.
type marketDataSource interface {
	GetIndexHistory(ctx context.Context, ticker string, days int) ([]models.CandleDaily, error)
	GetAllStocksHistory(ctx context.Context, days int) (map[string][]models.CandleDaily, error)
}

// compile-time assertion: *repository.MarketDataRepo must satisfy the interface.
var _ marketDataSource = (*repository.MarketDataRepo)(nil)

// ── Service ───────────────────────────────────────────────────────────────────

// MarketInputsService builds MarketInputs by loading candle data from the
// database and applying pure-function indicators.
type MarketInputsService struct {
	repo marketDataSource
	log  *slog.Logger
}

// NewMarketInputsService constructs a MarketInputsService backed by the
// production repository.
func NewMarketInputsService(
	repo *repository.MarketDataRepo,
	log *slog.Logger,
) *MarketInputsService {
	return &MarketInputsService{repo: repo, log: log}
}

// NewMarketInputsServiceFromSource constructs a MarketInputsService from any
// value that satisfies the marketDataSource interface.
// Intended for use in tests where a mock replaces the real repository.
func NewMarketInputsServiceFromSource(
	src marketDataSource,
	log *slog.Logger,
) *MarketInputsService {
	return &MarketInputsService{repo: src, log: log}
}

// BuildMarketInputs computes all inputs required by the Market Regime Engine
// for the given date.
//
// It loads the last MinHistoryDays trading sessions for SPY, QQQ, IWM and all
// tracked individual stocks, then applies indicator functions to produce a
// fully populated MarketInputs struct.
//
// The date parameter labels which session the inputs describe. Candle windows
// are always derived from the most-recent rows in candles_daily; if you need
// point-in-time inputs for back-testing, extend the repo interface to accept
// an upper-bound date parameter.
func (s *MarketInputsService) BuildMarketInputs(ctx context.Context, date time.Time) (MarketInputs, error) {
	tag := date.Format("2006-01-02") // reused across log lines

	// ── 1. Fetch index histories ──────────────────────────────────────────────
	spy, err := s.repo.GetIndexHistory(ctx, "SPY", MinHistoryDays)
	if err != nil {
		return MarketInputs{}, fmt.Errorf("BuildMarketInputs [%s]: SPY history: %w", tag, err)
	}
	qqq, err := s.repo.GetIndexHistory(ctx, "QQQ", MinHistoryDays)
	if err != nil {
		return MarketInputs{}, fmt.Errorf("BuildMarketInputs [%s]: QQQ history: %w", tag, err)
	}
	iwm, err := s.repo.GetIndexHistory(ctx, "IWM", MinHistoryDays)
	if err != nil {
		return MarketInputs{}, fmt.Errorf("BuildMarketInputs [%s]: IWM history: %w", tag, err)
	}

	s.log.Info("market inputs: index histories loaded",
		"date", tag,
		"spy_bars", len(spy),
		"qqq_bars", len(qqq),
		"iwm_bars", len(iwm),
	)

	// ── Minimum-candle guard ─────────────────────────────────────────────────
	// SMA-200 requires at least 200 bars.  If any index has fewer, ComputeSMA
	// returns 0, which makes every SMA comparison silently evaluate as TRUE
	// (price > 0 is almost always true for a real index).  The classifier would
	// then consume wrong boolean signals with no observable error.
	//
	// This guard is the single source-of-truth for the 200-bar requirement.
	// It fires during the first ~200 trading days after bootstrap and on any
	// day where the candle ingest step failed for a benchmark index.
	const minBars = 200
	switch {
	case len(spy) < minBars:
		return MarketInputs{}, fmt.Errorf(
			"BuildMarketInputs [%s]: SPY has only %d candles (need ≥ %d) — run backfill first",
			tag, len(spy), minBars,
		)
	case len(qqq) < minBars:
		return MarketInputs{}, fmt.Errorf(
			"BuildMarketInputs [%s]: QQQ has only %d candles (need ≥ %d) — run backfill first",
			tag, len(qqq), minBars,
		)
	case len(iwm) < minBars:
		return MarketInputs{}, fmt.Errorf(
			"BuildMarketInputs [%s]: IWM has only %d candles (need ≥ %d) — run backfill first",
			tag, len(iwm), minBars,
		)
	}

	// ── 2. Extract close-price series (required by all indicator functions) ───
	spyCloses := extractCloses(spy)
	qqqCloses := extractCloses(qqq)
	iwmCloses := extractCloses(iwm)

	// ── 3. SMA comparisons ───────────────────────────────────────────────────
	spyLatest := lastClose(spy)
	qqqLatest := lastClose(qqq)

	spySMA50 := indicators.ComputeSMA(spyCloses, 50)
	spySMA200 := indicators.ComputeSMA(spyCloses, 200)
	qqqSMA50 := indicators.ComputeSMA(qqqCloses, 50)
	qqqSMA200 := indicators.ComputeSMA(qqqCloses, 200)

	inputs := MarketInputs{
		Date: date,

		// v1 boolean flags (backward compatibility)
		SpyAbove50:  spyLatest > spySMA50,
		SpyAbove200: spyLatest > spySMA200,
		QqqAbove50:  qqqLatest > qqqSMA50,
		QqqAbove200: qqqLatest > qqqSMA200,

		// v2 continuous SMA-distance signals
		SpyPctFromSMA50:  pctFromSMA(spyLatest, spySMA50),
		SpyPctFromSMA200: pctFromSMA(spyLatest, spySMA200),
		QqqPctFromSMA50:  pctFromSMA(qqqLatest, qqqSMA50),
		QqqPctFromSMA200: pctFromSMA(qqqLatest, qqqSMA200),

		// Golden / death cross
		SpySMA50AboveSMA200: spySMA50 > spySMA200,
		QqqSMA50AboveSMA200: qqqSMA50 > qqqSMA200,

		// 52-week (252-session) trailing drawdown for SPY
		SpyDrawdownPct: trailingDrawdownPct(spyCloses),
	}

	// ── 4. Distribution days (SPY, 20-session lookback) ──────────────────────
	inputs.DistributionDays = indicators.CountDistributionDays(spy, distributionDaysLookback)

	// ── 5. Relative-strength ratios ───────────────────────────────────────────
	// Compute full series; surface only the latest value for today's regime.
	qqqVsSPY := indicators.ComputeRelativeStrength(qqqCloses, spyCloses)
	iwmVsSPY := indicators.ComputeRelativeStrength(iwmCloses, spyCloses)

	if n := len(qqqVsSPY); n > 0 {
		inputs.QqqvsspyRs = qqqVsSPY[n-1]
	}
	if n := len(iwmVsSPY); n > 0 {
		inputs.IwmvsspyRs = iwmVsSPY[n-1]
	}

	// ── 6. Market breadth ────────────────────────────────────────────────────
	stocks, err := s.repo.GetAllStocksHistory(ctx, MinHistoryDays)
	if err != nil {
		return MarketInputs{}, fmt.Errorf("BuildMarketInputs [%s]: stock history: %w", tag, err)
	}

	s.log.Info("market inputs: stock histories loaded",
		"date", tag,
		"tickers", len(stocks),
	)

	inputs.BreadthAbove50 = indicators.CalculateBreadth(stocks, 50)
	inputs.BreadthAbove200 = indicators.CalculateBreadth(stocks, 200)

	// ── 7. R-02 regime signal enrichment ──────────────────────────────────────
	// VIX level & 1-day ROC, $TICK session low, breadth velocity (5-session Δ).
	// These are best-effort: if VIX or $TICK data is missing, the fields remain
	// at their zero value (0.0) and the classifier treats them as neutral.
	vixCandles, vixErr := s.repo.GetIndexHistory(ctx, "VIX", 2)
	switch {
	case vixErr == nil && len(vixCandles) >= 2:
		inputs.VIXLevel = vixCandles[len(vixCandles)-1].Close
		prevClose := vixCandles[len(vixCandles)-2].Close
		if prevClose > 0 {
			inputs.VIXROCpct = (inputs.VIXLevel - prevClose) / prevClose * 100
		}
	case vixErr != nil:
		s.log.Warn("market inputs: VIX data unavailable", "date", tag, "error", vixErr)
	case len(vixCandles) == 1:
		inputs.VIXLevel = vixCandles[0].Close
		s.log.Warn("market inputs: VIX has only 1 candle, ROC unavailable", "date", tag, "vix_level", inputs.VIXLevel)
	default:
		s.log.Warn("market inputs: VIX has no candles", "date", tag)
	}

	tickCandles, tickErr := s.repo.GetIndexHistory(ctx, "$TICK", 1)
	if tickErr == nil && len(tickCandles) >= 1 {
		inputs.TickMinDaily = tickCandles[len(tickCandles)-1].Low
	} else if tickErr != nil {
		s.log.Warn("market inputs: $TICK data unavailable", "date", tag, "error", tickErr)
	}
	// When tickErr != nil or no candles, TickMinDaily stays 0.0.
	// The job's marketInputsToModel converts 0.0 → nil when $TICK is missing,
	// and the classifier's ScoreTick handles nil by returning a neutral 0.25.

	// Breadth velocity: 5-session change in breadth_above_50.
	// We need the breadth value from 5 sessions ago.  Re-compute it from the
	// stock history using a shorter window.
	inputs.BreadthVelocity5d = computeBreadthVelocity(stocks, inputs.BreadthAbove50)

	s.log.Info("market inputs built",
		"date", tag,

		"spy_above_50", inputs.SpyAbove50,
		"spy_above_200", inputs.SpyAbove200,
		"qqq_above_50", inputs.QqqAbove50,
		"qqq_above_200", inputs.QqqAbove200,
		"dist_days", inputs.DistributionDays,
		"breadth_50", fmt.Sprintf("%.1f%%", inputs.BreadthAbove50),
		"breadth_200", fmt.Sprintf("%.1f%%", inputs.BreadthAbove200),
		"qqq_vs_spy_rs", fmt.Sprintf("%.4f", inputs.QqqvsspyRs),
		"iwm_vs_spy_rs", fmt.Sprintf("%.4f", inputs.IwmvsspyRs),
		// v2
		"spy_pct_from_sma50", fmt.Sprintf("%.2f%%", inputs.SpyPctFromSMA50),
		"spy_pct_from_sma200", fmt.Sprintf("%.2f%%", inputs.SpyPctFromSMA200),
		"qqq_pct_from_sma50", fmt.Sprintf("%.2f%%", inputs.QqqPctFromSMA50),
		"qqq_pct_from_sma200", fmt.Sprintf("%.2f%%", inputs.QqqPctFromSMA200),
		"spy_golden_cross", inputs.SpySMA50AboveSMA200,
		"qqq_golden_cross", inputs.QqqSMA50AboveSMA200,
		"spy_drawdown_pct", fmt.Sprintf("%.2f%%", inputs.SpyDrawdownPct),
	)

	return inputs, nil
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

// lastClose returns the Close of the most-recent candle, or 0 if the slice is
// empty.
func lastClose(candles []models.CandleDaily) float64 {
	if len(candles) == 0 {
		return 0
	}
	return candles[len(candles)-1].Close
}

// pctFromSMA returns (price − sma) / sma × 100.
// A positive result means price is above the SMA; negative means below.
// Returns 0 when sma is zero to avoid a divide-by-zero.
func pctFromSMA(price, sma float64) float64 {
	if sma == 0 {
		return 0
	}
	return (price - sma) / sma * 100
}

// trailingDrawdownPct computes the percentage decline of the final close from
// the maximum close in the series (i.e. the 52-week high when the series spans
// 252 sessions).  The result is always ≤ 0.
// Returns 0 when the slice is empty or the maximum is zero.
func trailingDrawdownPct(closes []float64) float64 {
	if len(closes) == 0 {
		return 0
	}
	high := closes[0]
	for i := range closes {
		if closes[i] > high {
			high = closes[i]
		}
	}
	if high == 0 {
		return 0
	}
	return (closes[len(closes)-1] - high) / high * 100
}

// computeBreadthVelocity computes the 5-session change in breadth_above_50.
// It re-computes breadth from the stock history truncated to N-5 sessions,
// then returns todayBreadth - pastBreadth (percentage points).
// Returns 0 when there are fewer than 6 sessions of data.
func computeBreadthVelocity(stocks map[string][]models.CandleDaily, todayBreadth float64) float64 {
	// Truncate each stock's history to the last (N-5) sessions.
	truncated := make(map[string][]models.CandleDaily, len(stocks))
	for ticker := range stocks {
		candles := stocks[ticker]
		if len(candles) > 5 {
			truncated[ticker] = candles[:len(candles)-5]
		}
	}
	if len(truncated) == 0 {
		return 0
	}
	pastBreadth := indicators.CalculateBreadth(truncated, 50)
	return todayBreadth - pastBreadth
}
