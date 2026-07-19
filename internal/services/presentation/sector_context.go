package presentation

import (
	"math"
	"sort"

	"ai-stock-service/internal/models"
)

// ── Sector Descriptions ───────────────────────────────────────────────────────
// Plain-English sector descriptions for the commercial report. One sentence max.

var sectorNames = map[string]string{
	"XLK":  "Technology",
	"XLV":  "Healthcare",
	"XLF":  "Financials",
	"XLY":  "Consumer Discretionary",
	"XLP":  "Consumer Staples",
	"XLI":  "Industrials",
	"XLE":  "Energy",
	"XLB":  "Materials",
	"XLU":  "Utilities",
	"XLRE": "Real Estate",
	"XLC":  "Communication Services",
	"SMH":  "Semiconductors",
	"IGV":  "Software",
}

var sectorDescriptions = map[string]string{
	"XLK":  "Technology companies including software, semiconductors, and IT services.",
	"XLV":  "Healthcare companies including pharma, biotech, medical devices, and managed care.",
	"XLF":  "Financial services including banks, insurance, asset management, and fintech.",
	"XLY":  "Consumer discretionary — retail, autos, apparel, restaurants, and leisure.",
	"XLP":  "Consumer staples — food, beverages, household products, and tobacco.",
	"XLI":  "Industrial companies including aerospace, defense, machinery, and logistics.",
	"XLE":  "Energy companies including oil & gas exploration, refining, and equipment.",
	"XLB":  "Basic materials including chemicals, metals, mining, and paper products.",
	"XLU":  "Utility companies including electric, gas, water, and renewable energy.",
	"XLRE": "Real estate investment trusts (REITs) and real estate services.",
	"XLC":  "Communication services including telecom, media, and interactive entertainment.",
	"SMH":  "Semiconductors — chipmakers, foundries, and semiconductor equipment.",
	"IGV":  "Software — enterprise SaaS, cloud infrastructure, and cybersecurity.",
}

// ── Leading / Lagging Selection ───────────────────────────────────────────────

// SelectLeadingAndLagging returns the top 4 leading and bottom 4 lagging sectors
// from the daily sector scores. 4+4 aligns with a 4-column grid for a balanced
// commercial layout.
//
// Ranking formula: uses the pre-computed composite score from sector_scores_daily:
//
//	composite = 0.40 × rs_rank + 0.25 × perf3m_rank + 0.15 × perf1m_rank + 0.20 × trend_score
//
// Leading = top 4 by composite score (descending).
// Lagging = bottom 4 by composite score (ascending).
func SelectLeadingAndLagging(
	scores []models.SectorScoreDaily,
) (leading, lagging []models.SectorContext) {
	if len(scores) == 0 {
		return nil, nil
	}

	const topN = 4

	// Sort descending by composite score.
	sorted := make([]models.SectorScoreDaily, len(scores))
	copy(sorted, scores)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	for i := range sorted {
		s := &sorted[i]
		ctx := models.SectorContext{
			ETF:           s.ETF,
			SectorName:    sectorName(s.ETF),
			Description:   sectorDescription(s.ETF),
			Perf1M:        round4(s.Perf1M * 100), // convert to %
			Perf3M:        round4(s.Perf3M * 100),
			MomentumScore: round4(s.Score),
			RankPosition:  i + 1,
		}

		if i < topN {
			ctx.Label = "Leading"
			leading = append(leading, ctx)
		}
		if i >= len(sorted)-topN {
			ctx.Label = "Lagging"
			lagging = append(lagging, ctx)
		}
	}

	return leading, lagging
}

// BuildSectorSummary generates a plain-English sector summary for the report.
func BuildSectorSummary(leading, lagging []models.SectorContext) string {
	if len(leading) == 0 {
		return "Sector data is not yet available."
	}

	var summary string
	summary += "Market leadership is concentrated in "
	for i, s := range leading {
		if i > 0 && i == len(leading)-1 {
			summary += " and "
		} else if i > 0 {
			summary += ", "
		}
		summary += s.SectorName
	}
	summary += ". "

	if len(lagging) > 0 {
		summary += "Avoid new long positions in "
		for i, s := range lagging {
			if i > 0 && i == len(lagging)-1 {
				summary += " and "
			} else if i > 0 {
				summary += ", "
			}
			summary += s.SectorName
		}
		summary += " — these sectors are showing persistent weakness."
	}

	return summary
}

func sectorName(etf string) string {
	if n, ok := sectorNames[etf]; ok {
		return n
	}
	return etf
}

func sectorDescription(etf string) string {
	if d, ok := sectorDescriptions[etf]; ok {
		return d
	}
	return etf + " sector."
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
