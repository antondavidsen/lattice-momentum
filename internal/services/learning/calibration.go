// Package learning provides analysis and reporting for LLM prompt outcomes,
// including calibration gap analysis for recommended setups.
package learning

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"ai-stock-service/internal/models"
)

// ---- Types ----

// CalibrationReport holds the aggregated stats for a single setup across
// the analysis period, including win rate, average conviction, and the
// calibration gap (conviction vs actual win rate).
type CalibrationReport struct {
	Setup            string
	Count            int
	Wins             int
	WinRatePct       float64
	AvgConviction    float64
	AvgConvictionPct float64
	CalibrationGapPP float64 // conviction% - winRate%
	Flag             string  // "OVERCONFIDENT", "UNDERCONFIDENT", or "OK"
	SuggestedEdits   []string
}

// PromptTickerOutcomeReader defines the data-access boundary for calibration
// analysis. Any implementation that returns prompt ticker outcomes within a
// date range satisfies this interface.
type PromptTickerOutcomeReader interface {
	GetByDateRange(ctx context.Context, from, to time.Time) ([]models.PromptTickerOutcome, error)
}

// ---- Private helpers ----

// setupStats is an internal aggregation struct used during grouping.
type setupStats struct {
	setup       string
	count       int
	wins        int
	convictions []float64
}

func convictionToNumeric(c string) float64 {
	switch c {
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "STARTER":
		return 1
	default:
		return 0
	}
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	result := sum / float64(len(vals))
	if math.IsNaN(result) {
		return 0
	}
	return result
}

// ---- Public API ----

// GenerateCalibrationReport loads outcomes for the given date range, groups
// them by setup, computes win rates, conviction averages, and calibration
// gaps, and returns a report for every setup with at least 10 observations.
func GenerateCalibrationReport(ctx context.Context, reader PromptTickerOutcomeReader, from, today time.Time) ([]CalibrationReport, error) {
	outcomes, err := reader.GetByDateRange(ctx, from, today)
	if err != nil {
		return nil, fmt.Errorf("load outcomes: %w", err)
	}

	// Group by setup
	groups := make(map[string]*setupStats)
	for i := range outcomes {
		o := &outcomes[i]
		if !o.LLMRecommended || o.EvaluatedDays < 5 || o.RecommendedSetup == nil {
			continue
		}
		setup := *o.RecommendedSetup
		s, ok := groups[setup]
		if !ok {
			s = &setupStats{setup: setup}
			groups[setup] = s
		}
		s.count++
		if o.ActualReturn5D != nil && *o.ActualReturn5D > 0 {
			s.wins++
		}
		if o.RecommendedConviction != nil {
			c := convictionToNumeric(*o.RecommendedConviction)
			if c > 0 {
				s.convictions = append(s.convictions, c)
			}
		}
	}

	// Sort setups by count descending
	setups := make([]string, 0, len(groups))
	for k := range groups {
		setups = append(setups, k)
	}
	sort.Slice(setups, func(i, j int) bool {
		return groups[setups[i]].count > groups[setups[j]].count
	})

	// Build reports
	var reports []CalibrationReport
	for _, setup := range setups {
		s := groups[setup]
		if s.count < 10 {
			continue
		}

		winRate := float64(s.wins) / float64(s.count) * 100
		avgConv := mean(s.convictions)
		convPct := avgConv / 3.0 * 100
		gap := convPct - winRate

		flag := "OK"
		if gap > 15 {
			flag = "OVERCONFIDENT"
		} else if gap < -15 {
			flag = "UNDERCONFIDENT"
		}

		r := CalibrationReport{
			Setup:            setup,
			Count:            s.count,
			Wins:             s.wins,
			WinRatePct:       winRate,
			AvgConviction:    avgConv,
			AvgConvictionPct: convPct,
			CalibrationGapPP: gap,
			Flag:             flag,
		}

		if flag == "OVERCONFIDENT" {
			r.SuggestedEdits = append(r.SuggestedEdits,
				fmt.Sprintf("Historical win rate for %s is ~%.0f%%. Default conviction MEDIUM unless 2+ differentiating factors present. Do not rate HIGH.", setup, winRate),
			)
		}
		reports = append(reports, r)
	}
	return reports, nil
}

// FormatCalibrationReport renders the report slice as a human-readable string
// suitable for stdout.
func FormatCalibrationReport(reports []CalibrationReport, from, today time.Time) string {
	var s string
	s += "\n## QUARTERLY CALIBRATION REPORT\n"
	s += fmt.Sprintf("Period: %s to %s\n\n", from.Format("2006-01-02"), today.Format("2006-01-02"))
	s += "| Setup | N | Win Rate | Avg Conviction | Calibration Gap | Flag |\n"
	s += "|-------|---|----------|----------------|-----------------|------|\n"

	for _, r := range reports {
		w := fmt.Sprintf("%.0f%%", r.WinRatePct)
		a := fmt.Sprintf("%.1f (%.0f%%)", r.AvgConviction, r.AvgConvictionPct)
		g := fmt.Sprintf("%+.0fpp", r.CalibrationGapPP)
		s += fmt.Sprintf("| %s | %d | %s | %s | %s | %s |\n", r.Setup, r.Count, w, a, g, r.Flag)

		for _, edit := range r.SuggestedEdits {
			s += fmt.Sprintf("\n  Suggested edit for %s:\n", r.Setup)
			s += fmt.Sprintf("  ADD: %q\n\n", edit)
		}
	}
	return s
}
