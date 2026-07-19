package models

import (
	"encoding/json"
	"time"
)

// TickerEnrichment stores the Massive.com-enriched profile + context for a single
// ticker on a given date. One row per (ticker, enrichment_date) in the
// ticker_enrichments table.
type TickerEnrichment struct {
	Ticker         string    `db:"ticker"`
	EnrichmentDate time.Time `db:"enrichment_date"`

	// ── Company Profile ─────────────────────────────
	CompanyName  string  `db:"company_name"`
	Description  string  `db:"description"`    // 2–3 sentences
	MarketCapUSD float64 `db:"market_cap_usd"` // in dollars
	Industry     string  `db:"industry"`
	Sector       string  `db:"sector"`
	Country      string  `db:"country"`
	LogoURL      string  `db:"logo_url"` // SVG logo URL from Polygon branding

	// ── Price Context ───────────────────────────────
	CurrentPrice float64  `db:"current_price"`
	Perf30D      *float64 `db:"perf_30d"`  // % change
	Perf90D      *float64 `db:"perf_90d"`  // % change
	RSvsSPY      *float64 `db:"rs_vs_spy"` // relative strength (positive = outperforming)

	// ── News Summary ────────────────────────────────
	// JSON array of 0–3 bullet-point objects.
	NewsSummaryJSON json.RawMessage `db:"news_summary_json"`

	// ── Fundamentals (momentum-relevant) ────────────
	EPSGrowthYoY     *float64   `db:"eps_growth_yoy"`        // latest Q EPS growth vs year-ago Q (%)
	RevenueGrowthYoY *float64   `db:"revenue_growth_yoy"`    // latest Q revenue growth vs year-ago Q (%)
	EPSSurprisePct   *float64   `db:"eps_surprise_pct"`      // actual EPS vs consensus (%)
	NextEarningsDate *time.Time `db:"next_earnings_date"`    // next expected earnings report date
	ROE              *float64   `db:"roe"`                   // return on equity (%)
	FloatShares      *int64     `db:"float_shares"`          // floating shares
	InstitutionalOwn *float64   `db:"institutional_own_pct"` // institutional ownership (%)

	// ── Staleness management ────────────────────────
	CacheTTLHours int       `db:"cache_ttl_hours"` // 24 for price/news, 168 for profile
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// NewsItem is a single enrichment news bullet surfaced to subscribers.
type NewsItem struct {
	Headline    string `json:"headline"`
	Category    string `json:"category"`               // "earnings" | "guidance" | "m_and_a" | "regulation" | "analyst" | "unusual"
	Summary     string `json:"summary"`                // 1 sentence max
	PublishedAt string `json:"published_at,omitempty"` // RFC 3339 date, e.g. "2026-04-15"
}
