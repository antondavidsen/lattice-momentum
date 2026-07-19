package api

import (
	"math"
	"strings"

	"ai-stock-service/internal/models"
)

// Pattern-based entry / stop-loss / target computation.
//
// Ported from the MarketSmith Pine Script indicator which draws coloured
// boxes on breakout:
//
//   Blue  (entry)  : topBase           → topBase × 1.05   (buy zone, 0–5% above base top)
//   Red   (stop)   : topBase × 0.92   → topBase × 0.95   (stop-loss, 5–8% below base top)
//   Green (target) : topBase × 1.20   → topBase × 1.25   (take-profit, 20–25% above base top)
//
// "topBase" is the highest resistance level of the detected base pattern —
// the left lip of a cup, horizontal resistance for a flat base, or the
// pivot high of a double-bottom.

// computePatternTradeLevels derives trade levels purely from price structure
// and the classified base type.  It is used as a fallback when the
// commercial-report trade card does not supply explicit levels.
func computePatternTradeLevels(candles []models.CandleDaily, baseType string) *TradeLevels {
	topBase := detectBaseTop(candles, baseType)
	if topBase <= 0 {
		return nil
	}

	entryLow := topBase
	entryHigh := topBase * 1.05
	stopLoss := topBase * 0.95 // mid-point of the 0.92–0.95 stop zone
	target1 := topBase * 1.20
	target2 := topBase * 1.25

	return &TradeLevels{
		EntryLow:  &entryLow,
		EntryHigh: &entryHigh,
		StopLoss:  &stopLoss,
		Target1:   &target1,
		Target2:   &target2,
	}
}

// detectBaseTop finds the resistance price at the top of the most recent
// base pattern.  Strategy varies by base type (mirroring the Pine script).
func detectBaseTop(candles []models.CandleDaily, baseType string) float64 {
	n := len(candles)
	if n < 20 {
		return 0
	}

	bt := strings.ToLower(baseType)

	switch {
	case strings.Contains(bt, "cup"):
		return cupBaseTop(candles)
	case strings.Contains(bt, "double") || strings.Contains(bt, "bottom"):
		return doubleBottomBaseTop(candles)
	case strings.Contains(bt, "flat") || strings.Contains(bt, "tight"):
		return flatBaseTop(candles)
	case strings.Contains(bt, "vcp") || strings.Contains(bt, "contraction"):
		return vcpBaseTop(candles)
	default:
		// Generic: use the highest high in a recent consolidation window.
		return genericBaseTop(candles)
	}
}

// cupBaseTop mirrors the Pine script's cup detection — the "left lip" of the
// cup is the highest high before the deepest swing low.
func cupBaseTop(candles []models.CandleDaily) float64 {
	n := len(candles)
	lows := detectSwingLows(candles, 3)
	if len(lows) == 0 {
		return 0
	}

	// Find the deepest swing low (bottom of the cup).
	deepest := lows[0]
	for _, l := range lows[1:] {
		if l.Price < deepest.Price {
			deepest = l
		}
	}

	// Left lip: highest high before the cup bottom.
	var lipPrice float64
	for i := 0; i < deepest.Index && i < n; i++ {
		if candles[i].High > lipPrice {
			lipPrice = candles[i].High
		}
	}

	// Validate cup depth (Pine script: 8%–50%).
	if lipPrice <= 0 {
		return 0
	}
	depth := (lipPrice - deepest.Price) / lipPrice
	if depth < 0.08 || depth > 0.50 {
		return 0
	}

	return lipPrice
}

// doubleBottomBaseTop finds the pivot high at the centre of the W.
func doubleBottomBaseTop(candles []models.CandleDaily) float64 {
	highs := detectSwingHighs(candles, 3)
	lows := detectSwingLows(candles, 3)
	if len(highs) < 1 || len(lows) < 2 {
		return 0
	}

	// The "middle peak" of the W — the pivot high between two lows.
	l1 := lows[len(lows)-2]
	l2 := lows[len(lows)-1]
	var middlePeak float64
	for _, h := range highs {
		if h.Index > l1.Index && h.Index < l2.Index {
			if h.Price > middlePeak {
				middlePeak = h.Price
			}
		}
	}
	if middlePeak > 0 {
		return middlePeak
	}

	// Fallback: highest high before the second low.
	for i := l1.Index; i < l2.Index && i < len(candles); i++ {
		if candles[i].High > middlePeak {
			middlePeak = candles[i].High
		}
	}
	return middlePeak
}

// flatBaseTop returns the resistance ceiling of a tight/flat base — the
// highest high in the most recent consolidation window (≤15% depth).
func flatBaseTop(candles []models.CandleDaily) float64 {
	n := len(candles)
	window := 25
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
	// Flat base depth is ≤ 15%.
	if maxHigh <= 0 {
		return 0
	}
	depth := (maxHigh - minLow) / maxHigh
	if depth > 0.20 {
		return 0
	}
	return maxHigh
}

// vcpBaseTop uses the most recent swing high as resistance.
func vcpBaseTop(candles []models.CandleDaily) float64 {
	highs := detectSwingHighs(candles, 3)
	if len(highs) == 0 {
		return 0
	}
	return highs[len(highs)-1].Price
}

// genericBaseTop scans the last ~25 bars for the highest high.
func genericBaseTop(candles []models.CandleDaily) float64 {
	n := len(candles)
	window := 25
	if window > n {
		window = n
	}
	var maxHigh float64
	for i := range candles[n-window:] {
		c := &candles[n-window+i]
		if c.High > maxHigh {
			maxHigh = c.High
		}
	}
	return maxHigh
}
