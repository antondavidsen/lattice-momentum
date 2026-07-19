package presentation

import (
	"encoding/json"

	"ai-stock-service/internal/models"
)

// ── Enriched Report Types ─────────────────────────────────────────────────────

// EnrichedReport is the final JSON returned by GET /api/v1/reports/{date}
// when enrichment data is available. This is the "Part 5" commercial report.
type EnrichedReport struct {
	ReportDate string `json:"report_date"`
	Source     string `json:"source"` // "enriched_commercial"
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	Headline   string `json:"headline,omitempty"`

	MarketRegime  *RegimeCard `json:"market_regime,omitempty"`
	MarketSummary string      `json:"market_summary,omitempty"`

	LeadingSectors []models.SectorContext `json:"leading_sectors,omitempty"`
	LaggingSectors []models.SectorContext `json:"lagging_sectors,omitempty"`
	SectorSummary  string                 `json:"sector_summary,omitempty"`

	TradeIdeas []EnrichedTradeIdea `json:"trade_ideas,omitempty"`

	RiskNote       string `json:"risk_note,omitempty"`
	ClosingSummary string `json:"closing_summary,omitempty"`

	PastTradePerformance []EnrichedOutcomeCard `json:"past_trade_performance,omitempty"`
	PerformanceBlurb     string                `json:"performance_blurb,omitempty"`

	FullReportMarkdown string `json:"full_report_markdown,omitempty"`
	GeneratedBy        string `json:"generated_by"`

	ScreenerTickers map[string][]ScreenerTickerItem `json:"screener_tickers,omitempty"`

	TradeOfDay *models.TradeOfDay `json:"trade_of_day,omitempty"`
}

// ScreenerTickerItem is a ticker in the screener tickers row, optionally with rank.
type ScreenerTickerItem struct {
	Ticker string `json:"ticker"`
	Rank   int    `json:"rank,omitempty"` // 0 = unranked
}

// EnrichedTradeIdea is a single trade idea with full enrichment context.
type EnrichedTradeIdea struct {
	Ticker            string                      `json:"ticker"`
	CompanyProfile    *EnrichedCompanyProfile     `json:"company_profile,omitempty"`
	NewsSummary       []models.NewsItem           `json:"news_summary,omitempty"`
	PriceContext      *EnrichedPriceContext       `json:"price_context,omitempty"`
	TradeSetup        *models.CommercialTradeCard `json:"trade_setup,omitempty"`
	ChartURL          string                      `json:"chart_url"`
	RiskRewardSummary string                      `json:"risk_reward_summary,omitempty"`
	ModelScore        *float64                    `json:"model_score,omitempty"` // 0–10 ranking score
}

// EnrichedCompanyProfile is the company profile surfaced to subscribers.
type EnrichedCompanyProfile struct {
	CompanyName      string  `json:"company_name"`
	Description      string  `json:"description"`
	LogoURL          string  `json:"logo_url,omitempty"` // SVG logo from Polygon branding
	MarketCap        float64 `json:"market_cap"`
	MarketCapDisplay string  `json:"market_cap_display"`
	Industry         string  `json:"industry"`
	Sector           string  `json:"sector"`
	Country          string  `json:"country"`

	// Fundamentals (momentum-relevant).
	EPSGrowthYoY     *float64 `json:"eps_growth_yoy,omitempty"`     // latest Q EPS growth (%)
	RevenueGrowthYoY *float64 `json:"revenue_growth_yoy,omitempty"` // latest Q revenue growth (%)
	EPSSurprisePct   *float64 `json:"eps_surprise_pct,omitempty"`   // actual vs consensus (%)
	NextEarningsDate *string  `json:"next_earnings_date,omitempty"` // "2026-05-15"
	ROE              *float64 `json:"roe,omitempty"`                // return on equity (%)
	FloatShares      *int64   `json:"float_shares,omitempty"`       // floating shares
	FloatDisplay     string   `json:"float_display,omitempty"`      // "45.2M", "1.3B"
	InstOwnership    *float64 `json:"inst_ownership_pct,omitempty"` // institutional ownership (%)
}

// EnrichedPriceContext is the price context surfaced alongside a trade idea.
type EnrichedPriceContext struct {
	CurrentPrice float64  `json:"current_price"`
	Perf30D      *float64 `json:"perf_30d,omitempty"`
	Perf90D      *float64 `json:"perf_90d,omitempty"`
	RSvsSPY      *float64 `json:"rs_vs_spy,omitempty"`
	ORHigh       *float64 `json:"or_high,omitempty"` // Opening range high (09:30–09:44 ET)
	ORLow        *float64 `json:"or_low,omitempty"`  // Opening range low (09:30–09:44 ET)
}

// ── Builder ───────────────────────────────────────────────────────────────────

// BuildEnrichedReport composes the final commercial report from all pipeline outputs.
func BuildEnrichedReport(
	date string,
	cr *models.CommercialReport,
	regime *models.MarketRegimeDaily,
	sectors []models.SectorScoreDaily,
	enrichments map[string]*models.TickerEnrichment,
	outcomes []models.TradeOutcomeDaily,
	rankScores map[string]float64,
	tradeOfDay *models.TradeOfDay,
) EnrichedReport {
	report := EnrichedReport{
		ReportDate:  date,
		Source:      "enriched_commercial",
		Title:       "Alpha Engine Daily Report",
		Subtitle:    "AI-Powered Momentum Analysis — " + date,
		GeneratedBy: "Alpha Engine v2",
	}

	if cr != nil {
		report.Headline = cr.Headline
		report.MarketSummary = cr.MarketSummary
		report.RiskNote = cr.RiskNote
		report.ClosingSummary = cr.ClosingSummary
		report.FullReportMarkdown = cr.FullReportMarkdown
		report.PerformanceBlurb = cr.PerformanceBlurb

		// Build regime card
		if regime != nil {
			report.MarketRegime = buildRegimeCard(regime)
		} else {
			report.MarketRegime = buildRegimeFromString(cr.Regime)
		}

		// Build enriched trade ideas from commercial trade cards.
		var cards []models.CommercialTradeCard
		_ = json.Unmarshal(cr.TradeCardsJSON, &cards)

		// Build outcome lookup by ticker for easy joining
		outcomeMap := make(map[string]models.TradeOutcomeDaily)
		for i := range outcomes {
			if _, exists := outcomeMap[outcomes[i].Ticker]; !exists {
				outcomeMap[outcomes[i].Ticker] = outcomes[i]
			}
		}

		for i := range cards {
			card := &cards[i]
			idea := EnrichedTradeIdea{
				Ticker:     card.Ticker,
				TradeSetup: card,
				ChartURL:   "/api/v1/reports/" + date + "/chart/" + card.Ticker,
			}

			// Enrich with profile + news + price context.
			if enr, ok := enrichments[card.Ticker]; ok && enr != nil {
				profile := &EnrichedCompanyProfile{
					CompanyName:      enr.CompanyName,
					Description:      enr.Description,
					LogoURL:          enr.LogoURL,
					MarketCap:        enr.MarketCapUSD,
					MarketCapDisplay: formatMarketCap(enr.MarketCapUSD),
					Industry:         enr.Industry,
					Sector:           enr.Sector,
					Country:          enr.Country,
					EPSGrowthYoY:     enr.EPSGrowthYoY,
					RevenueGrowthYoY: enr.RevenueGrowthYoY,
					EPSSurprisePct:   enr.EPSSurprisePct,
					ROE:              enr.ROE,
					FloatShares:      enr.FloatShares,
					InstOwnership:    enr.InstitutionalOwn,
				}
				if enr.NextEarningsDate != nil {
					s := enr.NextEarningsDate.Format("2006-01-02")
					profile.NextEarningsDate = &s
				}
				if enr.FloatShares != nil {
					profile.FloatDisplay = formatFloat(*enr.FloatShares)
				}
				idea.CompanyProfile = profile
				idea.PriceContext = &EnrichedPriceContext{
					CurrentPrice: enr.CurrentPrice,
					Perf30D:      enr.Perf30D,
					Perf90D:      enr.Perf90D,
					RSvsSPY:      enr.RSvsSPY,
				}
				var newsItems []models.NewsItem
				_ = json.Unmarshal(enr.NewsSummaryJSON, &newsItems)
				idea.NewsSummary = newsItems
			}

			// Attach model score from rank lists (0-100 raw → 0-10 display).
			if score, ok := rankScores[card.Ticker]; ok && score > 0 {
				s := score / 10
				if s > 10 {
					s = 10
				}
				idea.ModelScore = &s
			}

			// Enrich with outcome tracking data
			if outcome, ok := outcomeMap[card.Ticker]; ok {
				idea.TradeSetup.EntryDate = outcome.EntryDate.Format("2006-01-02")
				idea.TradeSetup.ListType = string(outcome.ListType)
				idea.TradeSetup.EntryPrice = outcome.EntryPrice
				idea.TradeSetup.Return1D = outcome.Return1D
				idea.TradeSetup.Return2D = outcome.Return2D
				idea.TradeSetup.Return3D = outcome.Return3D
				idea.TradeSetup.Return4D = outcome.Return4D
				idea.TradeSetup.Return5D = outcome.Return5D
				idea.TradeSetup.Return10D = outcome.Return10D
				idea.TradeSetup.Return20D = outcome.Return20D
				idea.TradeSetup.MaxRunup20D = outcome.MaxRunup20D
				idea.TradeSetup.MaxDrawdown20D = outcome.MaxDrawdown20D
				idea.TradeSetup.EvaluatedDays = outcome.EvaluatedDays
				idea.TradeSetup.IsDuplicateSignal = outcome.IsDuplicateSignal
				idea.TradeSetup.TradingDaysSincePrior = outcome.TradingDaysSincePrior

				// Compute current_return and current_day
				idea.TradeSetup.CurrentReturn, idea.TradeSetup.CurrentDay = computeCurrentReturn(&outcome)
			}

			report.TradeIdeas = append(report.TradeIdeas, idea)
		}
		report.SectorSummary = cr.SectorSummary
	}

	// Sector leaders / laggards.
	leading, lagging := SelectLeadingAndLagging(sectors)
	report.LeadingSectors = leading
	report.LaggingSectors = lagging
	if cr == nil || cr.SectorSummary == "" {
		report.SectorSummary = BuildSectorSummary(leading, lagging)
	}

	// Trade of the day.
	report.TradeOfDay = tradeOfDay

	// Past trade performance.
	for i := range outcomes {
		companyName := ""
		if enr, ok := enrichments[outcomes[i].Ticker]; ok && enr != nil {
			companyName = enr.CompanyName
		}
		report.PastTradePerformance = append(
			report.PastTradePerformance,
			FormatEnrichedOutcome(&outcomes[i], companyName),
		)
	}

	return report
}

// computeCurrentReturn returns the most recent non-null return value and which day it represents.
// Walks return_1d → return_2d → ... → return_20d and takes the last non-nil.
func computeCurrentReturn(o *models.TradeOutcomeDaily) (ret *float64, day int) {
	// Check in order: 1d, 2d, 3d, 4d, 5d, 10d, 20d
	if o.Return1D != nil {
		ret = o.Return1D
		day = 1
	}
	if o.Return2D != nil {
		ret = o.Return2D
		day = 2
	}
	if o.Return3D != nil {
		ret = o.Return3D
		day = 3
	}
	if o.Return4D != nil {
		ret = o.Return4D
		day = 4
	}
	if o.Return5D != nil {
		ret = o.Return5D
		day = 5
	}
	if o.Return10D != nil {
		ret = o.Return10D
		day = 10
	}
	if o.Return20D != nil {
		ret = o.Return20D
		day = 20
	}

	return
}

// buildRegimeCard creates a RegimeCard from a MarketRegimeDaily with real confidence.
func buildRegimeCard(regime *models.MarketRegimeDaily) *RegimeCard {
	conf := 0.0
	if regime.Confidence != nil {
		conf = *regime.Confidence
	}
	label, badge, desc := regimePresentation(regime.Regime, conf)
	return &RegimeCard{
		Label:       label,
		Badge:       badge,
		Confidence:  conf,
		Description: desc,
	}
}

// buildRegimeFromString creates a RegimeCard from a regime label string (no confidence data).
func buildRegimeFromString(regime string) *RegimeCard {
	label, badge, desc := regimePresentation(regime, 0)
	return &RegimeCard{
		Label:       label,
		Badge:       badge,
		Description: desc,
	}
}

// formatMarketCap formats a dollar value into human-readable form.
func formatMarketCap(v float64) string {
	switch {
	case v >= 1e12:
		return fmtDollar(v/1e12, "T")
	case v >= 1e9:
		return fmtDollar(v/1e9, "B")
	case v >= 1e6:
		return fmtDollar(v/1e6, "M")
	default:
		return "$0"
	}
}

func fmtDollar(v float64, suffix string) string {
	if v >= 100 {
		return "$" + itoa(int(v)) + suffix
	}
	return "$" + ftoa(v, 1) + suffix
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func ftoa(v float64, decimals int) string {
	// Simple formatting for market cap display.
	whole := int(v)
	frac := int((v - float64(whole)) * 10)
	if decimals == 1 {
		return itoa(whole) + "." + itoa(frac)
	}
	return itoa(whole)
}

// formatFloat formats a share count into human-readable form (e.g. "45.2M", "1.3B").
func formatFloat(shares int64) string {
	v := float64(shares)
	switch {
	case v >= 1e9:
		return ftoa(v/1e9, 1) + "B"
	case v >= 1e6:
		return ftoa(v/1e6, 1) + "M"
	case v >= 1e3:
		return itoa(int(v/1e3)) + "K"
	default:
		return itoa(int(v))
	}
}
