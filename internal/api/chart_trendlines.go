package api

import (
	"math"
	"strings"

	"ai-stock-service/internal/models"
)

// SwingPoint is a local extremum in the candle series.
type SwingPoint struct {
	Index int
	Time  string
	Price float64
}

func detectSwingHighs(candles []models.CandleDaily, order int) []SwingPoint {
	n := len(candles)
	var out []SwingPoint
	for i := order; i < n-order; i++ {
		isHigh := true
		for j := 1; j <= order; j++ {
			if candles[i].High <= candles[i-j].High || candles[i].High <= candles[i+j].High {
				isHigh = false
				break
			}
		}
		if isHigh {
			out = append(out, SwingPoint{Index: i, Time: candles[i].Date.Format("2006-01-02"), Price: candles[i].High})
		}
	}
	return out
}

func detectSwingLows(candles []models.CandleDaily, order int) []SwingPoint {
	n := len(candles)
	var out []SwingPoint
	for i := order; i < n-order; i++ {
		isLow := true
		for j := 1; j <= order; j++ {
			if candles[i].Low >= candles[i-j].Low || candles[i].Low >= candles[i+j].Low {
				isLow = false
				break
			}
		}
		if isLow {
			out = append(out, SwingPoint{Index: i, Time: candles[i].Date.Format("2006-01-02"), Price: candles[i].Low})
		}
	}
	return out
}

// computeTrendlines analyses candle data and the LLM-classified base type to
// produce trendlines for chart rendering.
func computeTrendlines(candles []models.CandleDaily, baseType string) []ChartTrendline {
	n := len(candles)
	if n < 20 {
		return nil
	}
	bt := strings.ToLower(baseType)
	switch {
	case strings.Contains(bt, "tight") || strings.Contains(bt, "flat"):
		return flatBaseTrendlines(candles)
	case strings.Contains(bt, "vcp") || strings.Contains(bt, "contraction") || strings.Contains(bt, "volatility"):
		return vcpTrendlines(candles)
	case strings.Contains(bt, "cup"):
		return cupWithHandleTrendlines(candles)
	default:
		return defaultTrendlines(candles)
	}
}

func flatBaseTrendlines(candles []models.CandleDaily) []ChartTrendline {
	n := len(candles)
	window := 15
	if window > n {
		window = n
	}
	recent := candles[n-window:]
	var maxHigh float64
	minLow := math.MaxFloat64
	for i := range recent {
		c := &recent[i]
		if c.High > maxHigh {
			maxHigh = c.High
		}
		if c.Low < minLow {
			minLow = c.Low
		}
	}
	start := recent[0].Date.Format("2006-01-02")
	end := recent[len(recent)-1].Date.Format("2006-01-02")
	return []ChartTrendline{
		{Point1: ChartPoint{Time: start, Value: maxHigh}, Point2: ChartPoint{Time: end, Value: maxHigh}, Role: "resistance", Label: "Base resistance"},
		{Point1: ChartPoint{Time: start, Value: minLow}, Point2: ChartPoint{Time: end, Value: minLow}, Role: "support", Label: "Base support"},
	}
}

func vcpTrendlines(candles []models.CandleDaily) []ChartTrendline {
	highs := detectSwingHighs(candles, 3)
	lows := detectSwingLows(candles, 3)
	var lines []ChartTrendline
	if len(highs) >= 2 {
		for i := len(highs) - 1; i >= 1; i-- {
			if highs[i].Price < highs[i-1].Price {
				lines = append(lines, ChartTrendline{
					Point1: ChartPoint{Time: highs[i-1].Time, Value: highs[i-1].Price},
					Point2: ChartPoint{Time: highs[i].Time, Value: highs[i].Price},
					Role:   "resistance", Label: "VCP resistance",
				})
				break
			}
		}
	}
	if len(lows) >= 2 {
		for i := len(lows) - 1; i >= 1; i-- {
			if lows[i].Price > lows[i-1].Price {
				lines = append(lines, ChartTrendline{
					Point1: ChartPoint{Time: lows[i-1].Time, Value: lows[i-1].Price},
					Point2: ChartPoint{Time: lows[i].Time, Value: lows[i].Price},
					Role:   "support", Label: "VCP support",
				})
				break
			}
		}
		if len(lines) < 2 && len(lows) >= 2 {
			l1, l2 := lows[len(lows)-2], lows[len(lows)-1]
			lines = append(lines, ChartTrendline{
				Point1: ChartPoint{Time: l1.Time, Value: l1.Price},
				Point2: ChartPoint{Time: l2.Time, Value: l2.Price},
				Role:   "support", Label: "VCP support",
			})
		}
	}
	return lines
}

func cupWithHandleTrendlines(candles []models.CandleDaily) []ChartTrendline {
	n := len(candles)
	lows := detectSwingLows(candles, 3)
	var lines []ChartTrendline
	if len(lows) > 0 {
		deepest := lows[0]
		for _, l := range lows[1:] {
			if l.Price < deepest.Price {
				deepest = l
			}
		}
		var lipPrice float64
		for i := 0; i < deepest.Index && i < n; i++ {
			if candles[i].High > lipPrice {
				lipPrice = candles[i].High
			}
		}
		if lipPrice > 0 {
			lines = append(lines, ChartTrendline{
				Point1: ChartPoint{Time: candles[0].Date.Format("2006-01-02"), Value: lipPrice},
				Point2: ChartPoint{Time: candles[n-1].Date.Format("2006-01-02"), Value: lipPrice},
				Role:   "resistance", Label: "Cup lip resistance",
			})
		}
		if last := lows[len(lows)-1]; last.Index != deepest.Index {
			lines = append(lines, ChartTrendline{
				Point1: ChartPoint{Time: deepest.Time, Value: deepest.Price},
				Point2: ChartPoint{Time: last.Time, Value: last.Price},
				Role:   "support", Label: "Cup support",
			})
		}
	}
	return lines
}

func defaultTrendlines(candles []models.CandleDaily) []ChartTrendline {
	highs := detectSwingHighs(candles, 3)
	lows := detectSwingLows(candles, 3)
	var lines []ChartTrendline
	if len(highs) >= 2 {
		h1, h2 := highs[len(highs)-2], highs[len(highs)-1]
		lines = append(lines, ChartTrendline{
			Point1: ChartPoint{Time: h1.Time, Value: h1.Price},
			Point2: ChartPoint{Time: h2.Time, Value: h2.Price},
			Role:   "resistance", Label: "Resistance",
		})
	}
	if len(lows) >= 2 {
		l1, l2 := lows[len(lows)-2], lows[len(lows)-1]
		lines = append(lines, ChartTrendline{
			Point1: ChartPoint{Time: l1.Time, Value: l1.Price},
			Point2: ChartPoint{Time: l2.Time, Value: l2.Price},
			Role:   "support", Label: "Support",
		})
	}
	return lines
}
