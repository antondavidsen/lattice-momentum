package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// CommercialReportRepo handles persistence for the commercial_reports table.
type CommercialReportRepo struct {
	db dbPool
}

// NewCommercialReportRepo creates a new CommercialReportRepo backed by a live pool.
func NewCommercialReportRepo(db *pgxpool.Pool) *CommercialReportRepo {
	return &CommercialReportRepo{db: db}
}

const upsertCommercialReportSQL = `
	INSERT INTO commercial_reports (
		report_date, regime, headline, market_summary, sector_summary,
		trade_cards_json, risk_note, closing_summary,
		full_report_markdown, performance_blurb,
		source_list_types, provider, model, prompt_version,
		input_tokens, output_tokens, duration_ms,
		gate_level, regime_label, vix_level
	) VALUES (
		$1, $2, $3, $4, $5,
		$6::jsonb, $7, $8,
		$9, $10,
		$11, $12, $13, $14,
		$15, $16, $17,
		$18, $19, $20
	)
	ON CONFLICT (report_date) DO UPDATE SET
		regime               = EXCLUDED.regime,
		headline             = EXCLUDED.headline,
		market_summary       = EXCLUDED.market_summary,
		sector_summary       = EXCLUDED.sector_summary,
		trade_cards_json     = EXCLUDED.trade_cards_json,
		risk_note            = EXCLUDED.risk_note,
		closing_summary      = EXCLUDED.closing_summary,
		full_report_markdown = EXCLUDED.full_report_markdown,
		performance_blurb    = EXCLUDED.performance_blurb,
		source_list_types    = EXCLUDED.source_list_types,
		provider             = EXCLUDED.provider,
		model                = EXCLUDED.model,
		prompt_version       = EXCLUDED.prompt_version,
		input_tokens         = EXCLUDED.input_tokens,
		output_tokens        = EXCLUDED.output_tokens,
		duration_ms          = EXCLUDED.duration_ms,
		gate_level           = EXCLUDED.gate_level,
		regime_label         = EXCLUDED.regime_label,
		vix_level            = EXCLUDED.vix_level,
		updated_at           = NOW()`

// UpsertCommercialReport inserts or replaces a commercial report for a date.
// Idempotent: re-running for the same report_date overwrites.
func (r *CommercialReportRepo) UpsertCommercialReport(ctx context.Context, commercialReport *models.CommercialReport) error {
	_, err := r.db.Exec(ctx, upsertCommercialReportSQL,
		commercialReport.ReportDate, commercialReport.Regime, commercialReport.Headline, commercialReport.MarketSummary, commercialReport.SectorSummary,
		commercialReport.TradeCardsJSON, commercialReport.RiskNote, commercialReport.ClosingSummary,
		commercialReport.FullReportMarkdown, commercialReport.PerformanceBlurb,
		commercialReport.SourceListTypes, commercialReport.Provider, commercialReport.Model, commercialReport.PromptVersion,
		commercialReport.InputTokens, commercialReport.OutputTokens, commercialReport.DurationMs,
		commercialReport.GateLevel, commercialReport.RegimeLabel, commercialReport.VIXLevel,
	)

	if err != nil {
		return fmt.Errorf("UpsertCommercialReport %s: %w", commercialReport.ReportDate.Format("2006-01-02"), err)
	}
	return nil
}

// GetByDate returns the commercial report for a specific date.
// Returns nil, nil when no report exists.
func (r *CommercialReportRepo) GetByDate(ctx context.Context, date time.Time) (*models.CommercialReport, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, report_date, regime, headline, market_summary, sector_summary,
		       trade_cards_json, risk_note, closing_summary,
		       full_report_markdown, performance_blurb,
		       source_list_types, provider, model, prompt_version,
		       input_tokens, output_tokens, duration_ms,
		       gate_level, regime_label, vix_level,
		       created_at, updated_at
		FROM   commercial_reports
		WHERE  report_date = $1
	`, date)

	m, err := scanCommercialReport(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no report found by date
		}
		return nil, fmt.Errorf("GetByDate %s: %w", date.Format("2006-01-02"), err)
	}
	return m, nil
}

// GetLatest returns the most recent commercial report.
// Returns nil, nil when the table is empty.
func (r *CommercialReportRepo) GetLatest(ctx context.Context) (*models.CommercialReport, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, report_date, regime, headline, market_summary, sector_summary,
		       trade_cards_json, risk_note, closing_summary,
		       full_report_markdown, performance_blurb,
		       source_list_types, provider, model, prompt_version,
		       input_tokens, output_tokens, duration_ms,
		       gate_level, regime_label, vix_level,
		       created_at, updated_at
		FROM   commercial_reports
		ORDER  BY report_date DESC
		LIMIT  1
	`)

	m, err := scanCommercialReport(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no reports exist in table
		}
		return nil, fmt.Errorf("GetLatest: %w", err)
	}
	return m, nil
}

// GetMostRecent returns the most recent commercial report on or before the given date.
// Returns nil, nil when no report exists.
func (r *CommercialReportRepo) GetMostRecent(ctx context.Context, beforeOrEqual time.Time) (*models.CommercialReport, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, report_date, regime, headline, market_summary, sector_summary,
		       trade_cards_json, risk_note, closing_summary,
		       full_report_markdown, performance_blurb,
		       source_list_types, provider, model, prompt_version,
		       input_tokens, output_tokens, duration_ms,
		       gate_level, regime_label, vix_level,
		       created_at, updated_at
		FROM   commercial_reports
		WHERE  report_date <= $1
		ORDER  BY report_date DESC
		LIMIT  1
	`, beforeOrEqual)

	m, err := scanCommercialReport(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no report found on or before date
		}
		return nil, fmt.Errorf("GetMostRecent %s: %w", beforeOrEqual.Format("2006-01-02"), err)
	}
	return m, nil
}

// ListAvailableDates returns distinct report dates (newest first), limited to n.
func (r *CommercialReportRepo) ListAvailableDates(ctx context.Context, limit int) ([]time.Time, error) {
	rows, err := r.db.Query(ctx, `
		SELECT report_date FROM commercial_reports
		ORDER BY report_date DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("CommercialReportRepo.ListAvailableDates: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("CommercialReportRepo.ListAvailableDates scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetByDates returns commercial reports for specific dates (for joining with outcomes).
func (r *CommercialReportRepo) GetByDates(ctx context.Context, dates []time.Time) ([]models.CommercialReport, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	// Build placeholders $1, $2, ...
	placeholders := make([]string, len(dates))
	args := make([]any, len(dates))
	for i, d := range dates {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = d
	}

	query := `
		SELECT id, report_date, regime, headline, market_summary, sector_summary,
		       trade_cards_json, risk_note, closing_summary,
		       full_report_markdown, performance_blurb,
		       source_list_types, provider, model, prompt_version,
		       input_tokens, output_tokens, duration_ms,
		       gate_level, regime_label, vix_level,
		       created_at, updated_at
		FROM   commercial_reports
		WHERE  report_date IN (` + strings.Join(placeholders, ",") + `)
	`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("CommercialReportRepo.GetByDates: %w", err)
	}
	defer rows.Close()

	var out []models.CommercialReport
	for rows.Next() {
		var m models.CommercialReport
		var tradeCards []byte
		if err := rows.Scan(
			&m.ID, &m.ReportDate, &m.Regime, &m.Headline, &m.MarketSummary, &m.SectorSummary,
			&tradeCards, &m.RiskNote, &m.ClosingSummary,
			&m.FullReportMarkdown, &m.PerformanceBlurb,
			&m.SourceListTypes, &m.Provider, &m.Model, &m.PromptVersion,
			&m.InputTokens, &m.OutputTokens, &m.DurationMs,
			&m.GateLevel, &m.RegimeLabel, &m.VIXLevel,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {

			return nil, fmt.Errorf("CommercialReportRepo.GetByDates scan: %w", err)
		}
		m.Regime = strings.Clone(m.Regime)
		m.Headline = strings.Clone(m.Headline)
		m.MarketSummary = strings.Clone(m.MarketSummary)
		m.SectorSummary = strings.Clone(m.SectorSummary)
		m.RiskNote = strings.Clone(m.RiskNote)
		m.ClosingSummary = strings.Clone(m.ClosingSummary)
		m.FullReportMarkdown = strings.Clone(m.FullReportMarkdown)
		m.PerformanceBlurb = strings.Clone(m.PerformanceBlurb)
		m.SourceListTypes = cloneStringSlice(m.SourceListTypes)
		m.Provider = strings.Clone(m.Provider)
		m.Model = strings.Clone(m.Model)
		m.PromptVersion = strings.Clone(m.PromptVersion)
		if len(tradeCards) > 0 {
			m.TradeCardsJSON = append([]byte(nil), tradeCards...)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ── scan helper ───────────────────────────────────────────────────────────────

func scanCommercialReport(row pgx.Row) (*models.CommercialReport, error) {
	var m models.CommercialReport
	var tradeCards []byte
	if err := row.Scan(
		&m.ID, &m.ReportDate, &m.Regime, &m.Headline, &m.MarketSummary, &m.SectorSummary,
		&tradeCards, &m.RiskNote, &m.ClosingSummary,
		&m.FullReportMarkdown, &m.PerformanceBlurb,
		&m.SourceListTypes, &m.Provider, &m.Model, &m.PromptVersion,
		&m.InputTokens, &m.OutputTokens, &m.DurationMs,
		&m.GateLevel, &m.RegimeLabel, &m.VIXLevel,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {

		return nil, err
	}
	// Defensive copies: detach from pgx connection buffers to prevent
	// "found pointer to free object" GC crashes under concurrent load.
	m.Regime = strings.Clone(m.Regime)
	m.Headline = strings.Clone(m.Headline)
	m.MarketSummary = strings.Clone(m.MarketSummary)
	m.SectorSummary = strings.Clone(m.SectorSummary)
	m.RiskNote = strings.Clone(m.RiskNote)
	m.ClosingSummary = strings.Clone(m.ClosingSummary)
	m.FullReportMarkdown = strings.Clone(m.FullReportMarkdown)
	m.PerformanceBlurb = strings.Clone(m.PerformanceBlurb)
	m.SourceListTypes = cloneStringSlice(m.SourceListTypes)
	m.Provider = strings.Clone(m.Provider)
	m.Model = strings.Clone(m.Model)
	m.PromptVersion = strings.Clone(m.PromptVersion)
	if len(tradeCards) > 0 {
		m.TradeCardsJSON = append([]byte(nil), tradeCards...)
	}
	return &m, nil
}

// UpdateMorningBriefSource marks a commercial report as consumed by the
// morning brief pipeline. Idempotent — safe to call multiple times.
func (r *CommercialReportRepo) UpdateMorningBriefSource(ctx context.Context, date time.Time) error {
	return r.SetMorningBriefSource(ctx, date, true)
}

// SetMorningBriefSource sets the is_morning_brief_source flag on a commercial
// report. Idempotent — safe to call multiple times.
func (r *CommercialReportRepo) SetMorningBriefSource(ctx context.Context, reportDate time.Time, isSource bool) error {
	_, err := r.db.Exec(ctx, `
		UPDATE commercial_reports
		SET    is_morning_brief_source = $1,
		       updated_at = NOW()
		WHERE  report_date = $2
	`, isSource, reportDate)
	if err != nil {
		return fmt.Errorf("SetMorningBriefSource %s: %w", reportDate.Format("2006-01-02"), err)
	}
	return nil
}

func cloneStringSlice(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.Clone(s)
	}
	return out
}
