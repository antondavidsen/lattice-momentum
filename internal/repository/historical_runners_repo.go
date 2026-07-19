package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"ai-stock-service/internal/models"
)

// HistoricalRunnersRepo handles persistence for the historical_runners table.
type HistoricalRunnersRepo struct {
	db dbPool
}

// NewHistoricalRunnersRepo creates a new repo backed by a live pool.
func NewHistoricalRunnersRepo(db *pgxpool.Pool) *HistoricalRunnersRepo {
	return &HistoricalRunnersRepo{db: db}
}

const upsertHistoricalRunnerSQL = `
	INSERT INTO historical_runners (
		ticker, date,
		catalyst_category, catalyst_headline, catalyst_score, catalyst_confidence,
		prev_close, open_price, high_price, low_price, close_price,
		volume, avg_volume_20d,
		gap_pct, rel_volume, intraday_return, intraday_range_pct,
		max_intraday_runup, close_vs_range,
		float_shares, market_cap, sector,
		day2_open, day2_close, day2_return, held_gains_d2, day5_return,
		source, feature_vector, is_unremarkable
	) VALUES (
		$1, $2,
		$3, $4, $5, $6,
		$7, $8, $9, $10, $11,
		$12, $13,
		$14, $15, $16, $17,
		$18, $19,
		$20, $21, $22,
		$23, $24, $25, $26, $27,
		$28, $29, $30
	)
	ON CONFLICT (ticker, date) DO UPDATE SET
		catalyst_category   = CASE WHEN historical_runners.source = 'curated' AND EXCLUDED.source != 'curated'
		                           THEN historical_runners.catalyst_category ELSE EXCLUDED.catalyst_category END,
		catalyst_headline   = CASE WHEN historical_runners.source = 'curated' AND EXCLUDED.source != 'curated'
		                           THEN historical_runners.catalyst_headline ELSE EXCLUDED.catalyst_headline END,
		catalyst_score      = CASE WHEN historical_runners.source = 'curated' AND EXCLUDED.source != 'curated'
		                           THEN historical_runners.catalyst_score ELSE EXCLUDED.catalyst_score END,
		catalyst_confidence = CASE WHEN historical_runners.source = 'curated' AND EXCLUDED.source != 'curated'
		                           THEN historical_runners.catalyst_confidence ELSE EXCLUDED.catalyst_confidence END,
		prev_close          = EXCLUDED.prev_close,
		open_price          = EXCLUDED.open_price,
		high_price          = EXCLUDED.high_price,
		low_price           = EXCLUDED.low_price,
		close_price         = EXCLUDED.close_price,
		volume              = EXCLUDED.volume,
		avg_volume_20d      = EXCLUDED.avg_volume_20d,
		gap_pct             = EXCLUDED.gap_pct,
		rel_volume          = EXCLUDED.rel_volume,
		intraday_return     = EXCLUDED.intraday_return,
		intraday_range_pct  = EXCLUDED.intraday_range_pct,
		max_intraday_runup  = EXCLUDED.max_intraday_runup,
		close_vs_range      = EXCLUDED.close_vs_range,
		float_shares        = COALESCE(EXCLUDED.float_shares, historical_runners.float_shares),
		market_cap          = COALESCE(EXCLUDED.market_cap, historical_runners.market_cap),
		sector              = COALESCE(EXCLUDED.sector, historical_runners.sector),
		day2_open           = COALESCE(EXCLUDED.day2_open, historical_runners.day2_open),
		day2_close          = COALESCE(EXCLUDED.day2_close, historical_runners.day2_close),
		day2_return         = COALESCE(EXCLUDED.day2_return, historical_runners.day2_return),
		held_gains_d2       = COALESCE(EXCLUDED.held_gains_d2, historical_runners.held_gains_d2),
		day5_return         = COALESCE(EXCLUDED.day5_return, historical_runners.day5_return),
		source              = CASE WHEN historical_runners.source = 'curated' AND EXCLUDED.source != 'curated'
		                           THEN historical_runners.source ELSE EXCLUDED.source END,
		feature_vector      = COALESCE(EXCLUDED.feature_vector, historical_runners.feature_vector),
		is_unremarkable     = EXCLUDED.is_unremarkable,
		updated_at          = NOW()`

// Upsert inserts or updates a single historical runner row.
// Curated rows are protected: candle_scan source cannot overwrite curated metadata.
func (r *HistoricalRunnersRepo) Upsert(ctx context.Context, m *models.HistoricalRunner) error {
	_, err := r.db.Exec(ctx, upsertHistoricalRunnerSQL,
		m.Ticker, m.Date,
		m.CatalystCategory, m.CatalystHeadline, m.CatalystScore, m.CatalystConfidence,
		m.PrevClose, m.OpenPrice, m.HighPrice, m.LowPrice, m.ClosePrice,
		m.Volume, m.AvgVolume20D,
		m.GapPct, m.RelVolume, m.IntradayReturn, m.IntradayRangePct,
		m.MaxIntradayRunup, m.CloseVsRange,
		m.FloatShares, m.MarketCap, m.Sector,
		m.Day2Open, m.Day2Close, m.Day2Return, m.HeldGainsD2, m.Day5Return,
		m.Source, m.FeatureVector, m.IsUnremarkable,
	)
	if err != nil {
		return fmt.Errorf("UpsertHistoricalRunner %s %s: %w", m.Ticker, m.Date.Format("2006-01-02"), err)
	}
	return nil
}

// UpsertBatch inserts/updates multiple rows. Each row respects curated protection.
func (r *HistoricalRunnersRepo) UpsertBatch(ctx context.Context, runners []models.HistoricalRunner) error {
	for i := range runners {
		if err := r.Upsert(ctx, &runners[i]); err != nil {
			return err
		}
	}
	return nil
}

// scanRunner scans a single row from the standard column set.
func scanRunner(scan func(dest ...any) error) (models.HistoricalRunner, error) {
	var m models.HistoricalRunner
	err := scan(
		&m.ID, &m.Ticker, &m.Date,
		&m.CatalystCategory, &m.CatalystHeadline, &m.CatalystScore, &m.CatalystConfidence,
		&m.PrevClose, &m.OpenPrice, &m.HighPrice, &m.LowPrice, &m.ClosePrice,
		&m.Volume, &m.AvgVolume20D,
		&m.GapPct, &m.RelVolume, &m.IntradayReturn, &m.IntradayRangePct,
		&m.MaxIntradayRunup, &m.CloseVsRange,
		&m.FloatShares, &m.MarketCap, &m.Sector,
		&m.Day2Open, &m.Day2Close, &m.Day2Return, &m.HeldGainsD2, &m.Day5Return,
		&m.Source, &m.FeatureVector,
		&m.IsUnremarkable,
		&m.CreatedAt, &m.UpdatedAt,
	)
	return m, err
}

const selectRunnerCols = `
	id, ticker, date,
	catalyst_category, catalyst_headline, catalyst_score, catalyst_confidence,
	prev_close, open_price, high_price, low_price, close_price,
	volume, avg_volume_20d,
	gap_pct, rel_volume, intraday_return, intraday_range_pct,
	max_intraday_runup, close_vs_range,
	float_shares, market_cap, sector,
	day2_open, day2_close, day2_return, held_gains_d2, day5_return,
	source, feature_vector, is_unremarkable,
	created_at, updated_at`

// GetAll returns every row. Used for in-memory loading at startup.
func (r *HistoricalRunnersRepo) GetAll(ctx context.Context) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx, `SELECT `+selectRunnerCols+` FROM historical_runners ORDER BY date DESC`)
	if err != nil {
		return nil, fmt.Errorf("GetAll historical_runners: %w", err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("GetAll scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetByDateRange returns runners within a date range (inclusive).
func (r *HistoricalRunnersRepo) GetByDateRange(ctx context.Context, from, to time.Time) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE date >= $1 AND date <= $2 ORDER BY date DESC`,
		from, to)
	if err != nil {
		return nil, fmt.Errorf("GetByDateRange: %w", err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("GetByDateRange scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetByCatalystCategory returns up to limit runners of the given category.
func (r *HistoricalRunnersRepo) GetByCatalystCategory(ctx context.Context, category string, limit int) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE catalyst_category = $1 ORDER BY date DESC LIMIT $2`,
		category, limit)
	if err != nil {
		return nil, fmt.Errorf("GetByCatalystCategory: %w", err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("GetByCatalystCategory scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FindSimilar uses pgvector cosine distance to find the closest runners.
func (r *HistoricalRunnersRepo) FindSimilar(ctx context.Context, vec pgvector.Vector, limit int) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE feature_vector IS NOT NULL ORDER BY feature_vector <=> $1 LIMIT $2`,
		vec, limit)
	if err != nil {
		return nil, fmt.Errorf("FindSimilar: %w", err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("FindSimilar scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountBySource returns a map of source → count.
func (r *HistoricalRunnersRepo) CountBySource(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `SELECT source, COUNT(*) FROM historical_runners GROUP BY source`)
	if err != nil {
		return nil, fmt.Errorf("CountBySource: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var src string
		var cnt int
		if err := rows.Scan(&src, &cnt); err != nil {
			return nil, fmt.Errorf("CountBySource scan: %w", err)
		}
		out[src] = cnt
	}
	return out, rows.Err()
}

// GetUnknownCatalyst returns runners where catalyst_category = 'unknown', up to limit.
func (r *HistoricalRunnersRepo) GetUnknownCatalyst(ctx context.Context, limit int) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE catalyst_category = 'unknown' ORDER BY date DESC LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("GetUnknownCatalyst: %w", err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("GetUnknownCatalyst scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateCatalyst sets the catalyst fields for a specific (ticker, date).
func (r *HistoricalRunnersRepo) UpdateCatalyst(ctx context.Context, ticker string, date time.Time, category, headline, score int, confidence float64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE historical_runners
		SET catalyst_category   = $3,
		    catalyst_headline   = $4,
		    catalyst_score      = $5,
		    catalyst_confidence = $6,
		    updated_at          = NOW()
		WHERE ticker = $1 AND date = $2
	`, ticker, date, category, headline, score, confidence)
	if err != nil {
		return fmt.Errorf("UpdateCatalyst %s %s: %w", ticker, date.Format("2006-01-02"), err)
	}
	return nil
}

// UpdateFeatureVector sets the feature_vector for a specific (ticker, date).
func (r *HistoricalRunnersRepo) UpdateFeatureVector(ctx context.Context, ticker string, date time.Time, vec pgvector.Vector) error {
	_, err := r.db.Exec(ctx, `
		UPDATE historical_runners
		SET feature_vector = $3, updated_at = NOW()
		WHERE ticker = $1 AND date = $2
	`, ticker, date, vec)
	if err != nil {
		return fmt.Errorf("UpdateFeatureVector %s %s: %w", ticker, date.Format("2006-01-02"), err)
	}
	return nil
}

// BaseRateRow holds aggregated stats for one catalyst category.
type BaseRateRow struct {
	Category     string
	Count        int
	MeanReturn   float64
	MedianReturn float64
	WinRate      float64
}

// GetBaseRateStats returns aggregate performance by catalyst category.
func (r *HistoricalRunnersRepo) GetBaseRateStats(ctx context.Context) ([]BaseRateRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT catalyst_category,
		       COUNT(*)::INT,
		       AVG(intraday_return),
		       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY intraday_return),
		       SUM(CASE WHEN intraday_return > 0 THEN 1 ELSE 0 END)::FLOAT / NULLIF(COUNT(*), 0)
		FROM   historical_runners
		GROUP  BY catalyst_category
		ORDER  BY COUNT(*) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetBaseRateStats: %w", err)
	}
	defer rows.Close()

	var out []BaseRateRow
	for rows.Next() {
		var b BaseRateRow
		if err := rows.Scan(&b.Category, &b.Count, &b.MeanReturn, &b.MedianReturn, &b.WinRate); err != nil {
			return nil, fmt.Errorf("GetBaseRateStats scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBySource returns all runners from a specific source, ordered by date ASC.
func (r *HistoricalRunnersRepo) ListBySource(ctx context.Context, source string) ([]models.HistoricalRunner, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE source = $1 ORDER BY date ASC`,
		source)
	if err != nil {
		return nil, fmt.Errorf("ListBySource %s: %w", source, err)
	}
	defer rows.Close()

	var out []models.HistoricalRunner
	for rows.Next() {
		m, err := scanRunner(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("ListBySource scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetByTickerDate returns a single runner by (ticker, date), or nil if not found.
func (r *HistoricalRunnersRepo) GetByTickerDate(ctx context.Context, ticker string, date time.Time) (*models.HistoricalRunner, error) {
	m, err := scanRunner(r.db.QueryRow(ctx,
		`SELECT `+selectRunnerCols+` FROM historical_runners WHERE ticker = $1 AND date = $2`,
		ticker, date).Scan)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
