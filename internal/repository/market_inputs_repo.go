package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// dbPool is the subset of pgxpool.Pool that MarketInputsRepo uses.
// Defining it as an interface decouples the repo from the concrete pool and
// allows test code to inject a mock without touching production wiring.
type dbPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Compile-time assertion: *pgxpool.Pool must always satisfy dbPool.
var _ dbPool = (*pgxpool.Pool)(nil)

// MarketInputsRepo handles persistence for the market_inputs_daily table.
type MarketInputsRepo struct {
	db dbPool
}

// NewMarketInputsRepo creates a new MarketInputsRepo backed by a live pool.
func NewMarketInputsRepo(db *pgxpool.Pool) *MarketInputsRepo {
	return &MarketInputsRepo{db: db}
}

const upsertMarketInputsSQL = `
	INSERT INTO market_inputs_daily (
		date,
		spy_above_50,  spy_above_200,
		qqq_above_50,  qqq_above_200,
		distribution_days,
		breadth_above_50,  breadth_above_200,
		qqq_vs_spy_rs,     iwm_vs_spy_rs,
		spy_pct_from_sma50,  spy_pct_from_sma200,
		qqq_pct_from_sma50,  qqq_pct_from_sma200,
		spy_sma50_above_sma200, qqq_sma50_above_sma200,
		spy_drawdown_pct,
		vix_level, vix_roc_pct,
		tick_min_daily, breadth_velocity_5d
	) VALUES (
		$1,
		$2,  $3,
		$4,  $5,
		$6,
		$7,  $8,
		$9,  $10,
		$11, $12,
		$13, $14,
		$15, $16,
		$17,
		$18, $19,
		$20, $21
	)
	ON CONFLICT (date) DO UPDATE SET
		spy_above_50          = EXCLUDED.spy_above_50,
		spy_above_200         = EXCLUDED.spy_above_200,
		qqq_above_50          = EXCLUDED.qqq_above_50,
		qqq_above_200         = EXCLUDED.qqq_above_200,
		distribution_days     = EXCLUDED.distribution_days,
		breadth_above_50      = EXCLUDED.breadth_above_50,
		breadth_above_200     = EXCLUDED.breadth_above_200,
		qqq_vs_spy_rs         = EXCLUDED.qqq_vs_spy_rs,
		iwm_vs_spy_rs         = EXCLUDED.iwm_vs_spy_rs,
		spy_pct_from_sma50    = EXCLUDED.spy_pct_from_sma50,
		spy_pct_from_sma200   = EXCLUDED.spy_pct_from_sma200,
		qqq_pct_from_sma50    = EXCLUDED.qqq_pct_from_sma50,
		qqq_pct_from_sma200   = EXCLUDED.qqq_pct_from_sma200,
		spy_sma50_above_sma200 = EXCLUDED.spy_sma50_above_sma200,
		qqq_sma50_above_sma200 = EXCLUDED.qqq_sma50_above_sma200,
		spy_drawdown_pct      = EXCLUDED.spy_drawdown_pct,
		vix_level             = EXCLUDED.vix_level,
		vix_roc_pct           = EXCLUDED.vix_roc_pct,
		tick_min_daily        = EXCLUDED.tick_min_daily,
		breadth_velocity_5d   = EXCLUDED.breadth_velocity_5d,
		updated_at            = NOW()
	-- NOTE: created_at is intentionally omitted from the conflict clause so
	-- that the original creation timestamp is preserved on every re-run.`

// InsertMarketInputs persists a MarketInputsDaily row.
//
// The operation is idempotent: if a row already exists for m.Date it is fully
// overwritten. This makes the nightly job safe to rerun without manual cleanup.
func (r *MarketInputsRepo) InsertMarketInputs(ctx context.Context, m *models.MarketInputsDaily) error {
	_, err := r.db.Exec(ctx, upsertMarketInputsSQL,
		m.Date,
		m.SpyAbove50, m.SpyAbove200,
		m.QqqAbove50, m.QqqAbove200,
		m.DistributionDays,
		m.BreadthAbove50, m.BreadthAbove200,
		m.QQQvsSPYRS, m.IWMvsSPYRS,
		m.SpyPctFromSMA50, m.SpyPctFromSMA200,
		m.QqqPctFromSMA50, m.QqqPctFromSMA200,
		m.SpySMA50AboveSMA200, m.QqqSMA50AboveSMA200,
		m.SpyDrawdownPct,
		m.VIXLevel, m.VIXROCpct,
		m.TickMinDaily, m.BreadthVelocity5d,
	)
	if err != nil {
		return fmt.Errorf("InsertMarketInputs %s: %w", m.Date.Format("2006-01-02"), err)
	}
	return nil
}

const selectMarketInputsSQL = `
	SELECT
		date,
		spy_above_50,  spy_above_200,
		qqq_above_50,  qqq_above_200,
		distribution_days,
		breadth_above_50,  breadth_above_200,
		qqq_vs_spy_rs,     iwm_vs_spy_rs,
		spy_pct_from_sma50,  spy_pct_from_sma200,
		qqq_pct_from_sma50,  qqq_pct_from_sma200,
		spy_sma50_above_sma200, qqq_sma50_above_sma200,
		spy_drawdown_pct,
		vix_level, vix_roc_pct, tick_min_daily, breadth_velocity_5d,
		created_at,          updated_at
	FROM market_inputs_daily
	WHERE date = $1`

// GetMarketInputs returns the stored inputs for a specific trading session.
//
// Returns pgx.ErrNoRows (wrapped) when no row exists for the given date.
// Callers can check with errors.Is(err, pgx.ErrNoRows).
func (r *MarketInputsRepo) GetMarketInputs(ctx context.Context, date time.Time) (*models.MarketInputsDaily, error) {
	row := r.db.QueryRow(ctx, selectMarketInputsSQL, date)

	var m models.MarketInputsDaily
	if err := row.Scan(
		&m.Date,
		&m.SpyAbove50, &m.SpyAbove200,
		&m.QqqAbove50, &m.QqqAbove200,
		&m.DistributionDays,
		&m.BreadthAbove50, &m.BreadthAbove200,
		&m.QQQvsSPYRS, &m.IWMvsSPYRS,
		&m.SpyPctFromSMA50, &m.SpyPctFromSMA200,
		&m.QqqPctFromSMA50, &m.QqqPctFromSMA200,
		&m.SpySMA50AboveSMA200, &m.QqqSMA50AboveSMA200,
		&m.SpyDrawdownPct,
		&m.VIXLevel, &m.VIXROCpct, &m.TickMinDaily, &m.BreadthVelocity5d,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetMarketInputs %s: %w", date.Format("2006-01-02"), err)
	}
	return &m, nil
}

const selectLatestMarketInputsSQL = `
	SELECT
		date,
		spy_above_50,  spy_above_200,
		qqq_above_50,  qqq_above_200,
		distribution_days,
		breadth_above_50,  breadth_above_200,
		qqq_vs_spy_rs,     iwm_vs_spy_rs,
		spy_pct_from_sma50,  spy_pct_from_sma200,
		qqq_pct_from_sma50,  qqq_pct_from_sma200,
		spy_sma50_above_sma200, qqq_sma50_above_sma200,
		spy_drawdown_pct,
		vix_level, vix_roc_pct, tick_min_daily, breadth_velocity_5d,
		created_at,          updated_at
	FROM market_inputs_daily
	ORDER BY date DESC
	LIMIT 1`

// GetLatestMarketInputs returns the most recently computed market inputs row.
//
// Returns pgx.ErrNoRows (wrapped) when the table is empty.
// The classifier calls this to retrieve the current day's pre-computed signals
// without needing to know the exact date in advance.
func (r *MarketInputsRepo) GetLatestMarketInputs(ctx context.Context) (*models.MarketInputsDaily, error) {
	row := r.db.QueryRow(ctx, selectLatestMarketInputsSQL)

	var m models.MarketInputsDaily
	if err := row.Scan(
		&m.Date,
		&m.SpyAbove50, &m.SpyAbove200,
		&m.QqqAbove50, &m.QqqAbove200,
		&m.DistributionDays,
		&m.BreadthAbove50, &m.BreadthAbove200,
		&m.QQQvsSPYRS, &m.IWMvsSPYRS,
		&m.SpyPctFromSMA50, &m.SpyPctFromSMA200,
		&m.QqqPctFromSMA50, &m.QqqPctFromSMA200,
		&m.SpySMA50AboveSMA200, &m.QqqSMA50AboveSMA200,
		&m.SpyDrawdownPct,
		&m.VIXLevel, &m.VIXROCpct, &m.TickMinDaily, &m.BreadthVelocity5d,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetLatestMarketInputs: %w", err)
	}
	return &m, nil
}

// ListRecentMarketInputs returns the N most recent rows, newest first.
// Useful for health-check endpoints and back-testing data validation.
func (r *MarketInputsRepo) ListRecentMarketInputs(ctx context.Context, limit int) ([]models.MarketInputsDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			date,
			spy_above_50,  spy_above_200,
			qqq_above_50,  qqq_above_200,
			distribution_days,
			breadth_above_50,  breadth_above_200,
			qqq_vs_spy_rs,     iwm_vs_spy_rs,
			spy_pct_from_sma50,  spy_pct_from_sma200,
			qqq_pct_from_sma50,  qqq_pct_from_sma200,
			spy_sma50_above_sma200, qqq_sma50_above_sma200,
			spy_drawdown_pct,
			vix_level, vix_roc_pct, tick_min_daily, breadth_velocity_5d,
			created_at,          updated_at
		FROM market_inputs_daily
		ORDER BY date DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListRecentMarketInputs: %w", err)
	}
	defer rows.Close()

	var out []models.MarketInputsDaily
	for rows.Next() {
		var m models.MarketInputsDaily
		if err := rows.Scan(
			&m.Date,
			&m.SpyAbove50, &m.SpyAbove200,
			&m.QqqAbove50, &m.QqqAbove200,
			&m.DistributionDays,
			&m.BreadthAbove50, &m.BreadthAbove200,
			&m.QQQvsSPYRS, &m.IWMvsSPYRS,
			&m.SpyPctFromSMA50, &m.SpyPctFromSMA200,
			&m.QqqPctFromSMA50, &m.QqqPctFromSMA200,
			&m.SpySMA50AboveSMA200, &m.QqqSMA50AboveSMA200,
			&m.SpyDrawdownPct,
			&m.VIXLevel, &m.VIXROCpct, &m.TickMinDaily, &m.BreadthVelocity5d,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListRecentMarketInputs scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
