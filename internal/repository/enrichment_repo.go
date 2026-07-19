package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// EnrichmentRepo handles persistence for the ticker_enrichments table.
type EnrichmentRepo struct {
	db dbPool
}

// NewEnrichmentRepo creates a new EnrichmentRepo backed by a live pool.
func NewEnrichmentRepo(db *pgxpool.Pool) *EnrichmentRepo {
	return &EnrichmentRepo{db: db}
}

// enrichmentSelectCols is the shared column list for all enrichment SELECT queries.
const enrichmentSelectCols = `
	ticker, enrichment_date,
	company_name, description, market_cap_usd, industry, sector, country, logo_url,
	current_price, perf_30d, perf_90d, rs_vs_spy,
	news_summary_json, cache_ttl_hours,
	eps_growth_yoy, revenue_growth_yoy, eps_surprise_pct,
	next_earnings_date, roe, float_shares, institutional_own_pct,
	created_at, updated_at`

const upsertEnrichmentSQL = `
	INSERT INTO ticker_enrichments (
		ticker, enrichment_date,
		company_name, description, market_cap_usd, industry, sector, country, logo_url,
		current_price, perf_30d, perf_90d, rs_vs_spy,
		news_summary_json, cache_ttl_hours,
		eps_growth_yoy, revenue_growth_yoy, eps_surprise_pct,
		next_earnings_date, roe, float_shares, institutional_own_pct
	) VALUES (
		$1, $2,
		$3, $4, $5, $6, $7, $8, $9,
		$10, $11, $12, $13,
		$14::jsonb, $15,
		$16, $17, $18,
		$19, $20, $21, $22
	)
	ON CONFLICT (ticker, enrichment_date) DO UPDATE SET
		company_name     = EXCLUDED.company_name,
		description      = EXCLUDED.description,
		market_cap_usd   = EXCLUDED.market_cap_usd,
		industry         = EXCLUDED.industry,
		sector           = EXCLUDED.sector,
		country          = EXCLUDED.country,
		logo_url         = EXCLUDED.logo_url,
		current_price    = EXCLUDED.current_price,
		perf_30d         = EXCLUDED.perf_30d,
		perf_90d         = EXCLUDED.perf_90d,
		rs_vs_spy        = EXCLUDED.rs_vs_spy,
		news_summary_json = EXCLUDED.news_summary_json,
		cache_ttl_hours  = EXCLUDED.cache_ttl_hours,
		eps_growth_yoy   = EXCLUDED.eps_growth_yoy,
		revenue_growth_yoy = EXCLUDED.revenue_growth_yoy,
		eps_surprise_pct = EXCLUDED.eps_surprise_pct,
		next_earnings_date = EXCLUDED.next_earnings_date,
		roe              = EXCLUDED.roe,
		float_shares     = EXCLUDED.float_shares,
		institutional_own_pct = EXCLUDED.institutional_own_pct,
		updated_at       = NOW()`

// UpsertEnrichment inserts or replaces a ticker enrichment row.
// Idempotent: re-running for the same (ticker, enrichment_date) overwrites.
func (r *EnrichmentRepo) UpsertEnrichment(ctx context.Context, m *models.TickerEnrichment) error {
	newsJSON := m.NewsSummaryJSON
	if newsJSON == nil {
		newsJSON = json.RawMessage("[]")
	}

	_, err := r.db.Exec(ctx, upsertEnrichmentSQL,
		m.Ticker, m.EnrichmentDate,
		m.CompanyName, m.Description, m.MarketCapUSD, m.Industry, m.Sector, m.Country, m.LogoURL,
		m.CurrentPrice, m.Perf30D, m.Perf90D, m.RSvsSPY,
		newsJSON, m.CacheTTLHours,
		m.EPSGrowthYoY, m.RevenueGrowthYoY, m.EPSSurprisePct,
		m.NextEarningsDate, m.ROE, m.FloatShares, m.InstitutionalOwn,
	)
	if err != nil {
		return fmt.Errorf("UpsertEnrichment %s %s: %w",
			m.Ticker, m.EnrichmentDate.Format("2006-01-02"), err)
	}
	return nil
}

// GetByTickerDate returns the enrichment for a specific (ticker, date).
// Returns nil, nil when no row exists.
func (r *EnrichmentRepo) GetByTickerDate(ctx context.Context, ticker string, date time.Time) (*models.TickerEnrichment, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+enrichmentSelectCols+`
		FROM   ticker_enrichments
		WHERE  ticker = $1 AND enrichment_date = $2
	`, ticker, date)

	m, err := scanEnrichment(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no enrichment found for ticker+date
		}
		return nil, fmt.Errorf("GetByTickerDate %s %s: %w", ticker, date.Format("2006-01-02"), err)
	}
	return m, nil
}

// GetByDate returns all enrichment rows for a given date, ordered by ticker.
func (r *EnrichmentRepo) GetByDate(ctx context.Context, date time.Time) ([]models.TickerEnrichment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+enrichmentSelectCols+`
		FROM   ticker_enrichments
		WHERE  enrichment_date = $1
		ORDER  BY ticker
	`, date)
	if err != nil {
		return nil, fmt.Errorf("GetByDate %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.TickerEnrichment
	for rows.Next() {
		m, err := scanEnrichmentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("GetByDate scan: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetByDateRange returns all enrichment rows between from and to (inclusive), ordered by ticker, date.
func (r *EnrichmentRepo) GetByDateRange(ctx context.Context, from, to time.Time) ([]models.TickerEnrichment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+enrichmentSelectCols+`
		FROM   ticker_enrichments
		WHERE  enrichment_date >= $1 AND enrichment_date <= $2
		ORDER  BY ticker, enrichment_date
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("GetByDateRange %s..%s: %w", from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.TickerEnrichment
	for rows.Next() {
		m, err := scanEnrichmentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("GetByDateRange scan: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetLatestForTicker returns the most recent enrichment for a ticker.
// Returns nil, nil when no row exists.
func (r *EnrichmentRepo) GetLatestForTicker(ctx context.Context, ticker string) (*models.TickerEnrichment, error) {

	row := r.db.QueryRow(ctx, `
		SELECT `+enrichmentSelectCols+`
		FROM   ticker_enrichments
		WHERE  ticker = $1
		ORDER  BY enrichment_date DESC
		LIMIT  1
	`, ticker)

	m, err := scanEnrichment(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no enrichment found for ticker
		}
		return nil, fmt.Errorf("GetLatestForTicker %s: %w", ticker, err)
	}
	return m, nil
}

// scanEnrichment scans a single pgx.Row into a TickerEnrichment.
func scanEnrichment(row pgx.Row) (*models.TickerEnrichment, error) {
	var m models.TickerEnrichment
	var newsSummary []byte
	if err := row.Scan(
		&m.Ticker, &m.EnrichmentDate,
		&m.CompanyName, &m.Description, &m.MarketCapUSD, &m.Industry, &m.Sector, &m.Country, &m.LogoURL,
		&m.CurrentPrice, &m.Perf30D, &m.Perf90D, &m.RSvsSPY,
		&newsSummary, &m.CacheTTLHours,
		&m.EPSGrowthYoY, &m.RevenueGrowthYoY, &m.EPSSurprisePct,
		&m.NextEarningsDate, &m.ROE, &m.FloatShares, &m.InstitutionalOwn,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	cloneEnrichmentStrings(&m)
	if len(newsSummary) > 0 {
		m.NewsSummaryJSON = append([]byte(nil), newsSummary...)
	}
	return &m, nil
}

// scanEnrichmentRow is like scanEnrichment but for pgx.Rows (multi-row queries).
func scanEnrichmentRow(rows pgx.Rows) (*models.TickerEnrichment, error) {
	var m models.TickerEnrichment
	var newsSummary []byte
	if err := rows.Scan(
		&m.Ticker, &m.EnrichmentDate,
		&m.CompanyName, &m.Description, &m.MarketCapUSD, &m.Industry, &m.Sector, &m.Country, &m.LogoURL,
		&m.CurrentPrice, &m.Perf30D, &m.Perf90D, &m.RSvsSPY,
		&newsSummary, &m.CacheTTLHours,
		&m.EPSGrowthYoY, &m.RevenueGrowthYoY, &m.EPSSurprisePct,
		&m.NextEarningsDate, &m.ROE, &m.FloatShares, &m.InstitutionalOwn,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	cloneEnrichmentStrings(&m)
	if len(newsSummary) > 0 {
		m.NewsSummaryJSON = append([]byte(nil), newsSummary...)
	}
	return &m, nil
}

// cloneEnrichmentStrings detaches string fields from pgx connection buffers.
func cloneEnrichmentStrings(m *models.TickerEnrichment) {
	m.Ticker = strings.Clone(m.Ticker)
	m.CompanyName = strings.Clone(m.CompanyName)
	m.Description = strings.Clone(m.Description)
	m.Industry = strings.Clone(m.Industry)
	m.Sector = strings.Clone(m.Sector)
	m.Country = strings.Clone(m.Country)
	m.LogoURL = strings.Clone(m.LogoURL)
}
