package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// chartHandler provides GET /api/v1/reports/{date}/chart/{ticker}.
// It returns OHLC candle data + trade levels for the frontend chart component.
type chartHandler struct {
	candleRepo     *repository.CandlesDailyRepo
	commercialRepo *repository.CommercialReportRepo
	enrichRepo     *repository.EnrichmentRepo
}

// getChartData returns ~4 months of OHLC data for a ticker plus the trade
// levels (entry, stop, targets) from the commercial report for that date.
//
// 4 months (~85 trading days) shows recent price structure clearly without
// excessive history.
//
// GET /api/v1/reports/{date}/chart/{ticker}
func (h *chartHandler) getChartData(w http.ResponseWriter, r *http.Request) {
	dateStr := r.PathValue("date")
	ticker := r.PathValue("ticker")

	if ticker == "" {
		writeError(w, http.StatusBadRequest, "missing ticker")
		return
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date — expected YYYY-MM-DD")
		return
	}

	// Fetch 6 months of calendar days — enough for 50-period SMA to have
	// meaningful coverage over the displayed 4-month window.
	from := date.AddDate(0, -6, 0)
	candles, err := h.candleRepo.GetCandles(r.Context(), ticker, from, date)
	if err != nil {
		slog.Error("chart: load candles", "ticker", ticker, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load chart data")
		return
	}

	if len(candles) == 0 {
		writeError(w, http.StatusNotFound, "no candle data for "+ticker)
		return
	}

	// Format candles for lightweight-charts.
	chartCandles := make([]ChartOHLC, len(candles))
	for i := range candles {
		c := &candles[i]
		chartCandles[i] = ChartOHLC{
			Time:   c.Date.Format("2006-01-02"),
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		}
	}

	// Extract trade levels from the commercial report's trade cards.
	levels, baseType := h.extractTradeLevelsAndBaseType(r, date, ticker)

	// Fallback: derive trade levels from chart pattern structure (Pine-script
	// style entry/stop/target boxes) when the commercial report is missing.
	if levels == nil || (levels.EntryLow == nil && levels.StopLoss == nil) {
		if patternLevels := computePatternTradeLevels(candles, baseType); patternLevels != nil {
			levels = patternLevels
		}
	}

	// Load company name from enrichment.
	companyName := ticker
	if h.enrichRepo != nil {
		if enr, err := h.enrichRepo.GetByTickerDate(r.Context(), ticker, date); err == nil && enr != nil {
			if enr.CompanyName != "" {
				companyName = enr.CompanyName
			}
		}
	}

	// Compute moving averages from candle closes.
	sma21 := computeSMA(chartCandles, 21)
	sma50 := computeSMA(chartCandles, 50)

	// Build markers for the trade date.
	markers := buildChartMarkers(dateStr, levels)

	// Compute trendlines from swing point detection + pattern classification.
	trendlines := computeTrendlines(candles, baseType)

	// Derive pivot price from the resistance trendline's endpoint and compute
	// "X% from pivot" — only when a resistance line exists.
	var pivotPrice, pctFromPivot *float64
	if pivot, pct, ok := computePivotDistance(candles, trendlines); ok {
		pivotPrice = &pivot
		pctFromPivot = &pct
	}

	resp := ChartResponse{
		Ticker:       ticker,
		SMA21:        sma21,
		SMA50:        sma50,
		CompanyName:  companyName,
		BaseType:     baseType,
		PatternLabel: patternLabel(baseType),
		PivotPrice:   pivotPrice,
		PctFromPivot: pctFromPivot,
		Candles:      chartCandles,
		TradeLevels:  levels,
		Markers:      markers,
		Trendlines:   trendlines,
	}

	writeJSON(w, http.StatusOK, resp)
}

// extractTradeLevelsAndBaseType parses trade levels and the chart base type
// classification from the commercial report's trade cards JSON for a ticker.
func (h *chartHandler) extractTradeLevelsAndBaseType(r *http.Request, date time.Time, ticker string) (tradeLevels *TradeLevels, baseType string) {
	if h.commercialRepo == nil {
		return nil, ""
	}

	cr, err := h.commercialRepo.GetByDate(r.Context(), date)
	if err != nil || cr == nil {
		return nil, ""
	}

	var cards []models.CommercialTradeCard
	if err := json.Unmarshal(cr.TradeCardsJSON, &cards); err != nil {
		return nil, ""
	}

	for i := range cards {
		card := &cards[i]
		if card.Ticker != ticker {
			continue
		}

		levels := &TradeLevels{}

		if lo, hi, ok := parseRange(card.EntryZone); ok {
			levels.EntryLow = &lo
			levels.EntryHigh = &hi
		}
		if v := parsePrice(card.StopLoss); v > 0 {
			levels.StopLoss = &v
		}
		if v := parsePrice(card.Target1); v > 0 {
			levels.Target1 = &v
		}
		if v := parsePrice(card.Target2); v > 0 {
			levels.Target2 = &v
		}

		return levels, card.BaseType
	}

	return nil, ""
}

// buildChartMarkers creates visual markers for the chart.
func buildChartMarkers(dateStr string, levels *TradeLevels) []ChartMarker {
	if levels == nil {
		return nil
	}

	var markers []ChartMarker
	if levels.EntryLow != nil {
		markers = append(markers, ChartMarker{
			Time:     dateStr,
			Position: "belowBar",
			Color:    "#2196F3",
			Shape:    "arrowUp",
			Text:     "Buy Zone",
		})
	}
	return markers
}

// parsePrice extracts a float64 from a price string like "$143.50" or "143.50".
func parsePrice(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")
	// Take the first number if there are multiple (e.g. "143.00 – 145.50")
	if idx := strings.IndexAny(s, " –-"); idx > 0 {
		s = s[:idx]
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// parseRange extracts two prices from a range string like "$143.00 – $145.50".
func parseRange(s string) (lo, hi float64, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")

	// Try various separators: " – ", " - ", "–", "-"
	for _, sep := range []string{" – ", " - ", "–", "-"} {
		parts := strings.SplitN(s, sep, 2)
		if len(parts) == 2 {
			lo, errLo := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			hi, errHi := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if errLo == nil && errHi == nil && lo > 0 && hi > 0 {
				return lo, hi, true
			}
		}
	}
	return 0, 0, false
}

// computeSMA calculates a simple moving average over N periods from OHLC data.
// Returns a ChartPoint slice starting from the Nth candle onwards.
func computeSMA(candles []ChartOHLC, period int) []ChartPoint {
	if len(candles) < period {
		return nil
	}

	out := make([]ChartPoint, 0, len(candles)-period+1)
	var sum float64
	for i, c := range candles {
		sum += c.Close
		if i >= period {
			sum -= candles[i-period].Close
		}
		if i >= period-1 {
			out = append(out, ChartPoint{
				Time:  c.Time,
				Value: sum / float64(period),
			})
		}
	}
	return out
}

// patternLabel returns a human-readable pattern name for chart display.
func patternLabel(baseType string) string {
	if baseType == "" {
		return ""
	}
	bt := strings.ToLower(baseType)
	switch {
	case strings.Contains(bt, "vcp") || strings.Contains(bt, "contraction") || strings.Contains(bt, "volatility"):
		return "VCP (Volatility Contraction)"
	case strings.Contains(bt, "cup"):
		return "Cup with Handle"
	case strings.Contains(bt, "tight"):
		return "Tight Base"
	case strings.Contains(bt, "flat"):
		return "Flat Base"
	default:
		return baseType
	}
}

// computePivotDistance finds the resistance trendline's endpoint price (the
// pivot / breakout level) and returns how far the last close is from it as a
// percentage.  Positive = above pivot (already breaking out), negative = below.
//
// Only returns ok=true when:
//   - at least one "resistance" trendline exists
//   - there is at least one candle to take a close from
//   - the distance is within ±15 % (beyond that it's not actionable)
func computePivotDistance(candles []models.CandleDaily, trendlines []ChartTrendline) (pivot, pct float64, ok bool) {
	if len(candles) == 0 || len(trendlines) == 0 {
		return 0, 0, false
	}

	// Find the resistance trendline — use the endpoint (p2) price as the pivot.
	var found bool
	for _, tl := range trendlines {
		if tl.Role == "resistance" {
			pivot = tl.Point2.Value
			found = true
			break
		}
	}
	if !found || pivot <= 0 {
		return 0, 0, false
	}

	lastClose := candles[len(candles)-1].Close
	if lastClose <= 0 {
		return 0, 0, false
	}

	pct = math.Round(((lastClose-pivot)/pivot*100)*10) / 10 // one decimal
	if pct < -15 || pct > 15 {
		return 0, 0, false // too far away to be meaningful
	}

	return pivot, pct, true
}
