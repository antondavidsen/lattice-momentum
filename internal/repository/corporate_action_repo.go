package repository

import (
	"context"
	"fmt"
	"time"

	"ai-stock-service/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CorporateActionRepo persists corporate-action data from Polygon reference API.
type CorporateActionRepo struct {
	db *pgxpool.Pool
}

// NewCorporateActionRepo creates a new CorporateActionRepo.
func NewCorporateActionRepo(db *pgxpool.Pool) *CorporateActionRepo {
	return &CorporateActionRepo{db: db}
}

// Pool exposes the underlying connection pool for ad-hoc queries.
func (r *CorporateActionRepo) Pool() *pgxpool.Pool {
	return r.db
}

// UpsertBatch inserts corporate actions with ON CONFLICT DO NOTHING.
// Duplicates (ticker, ex_date, action_type) are silently skipped.
func (r *CorporateActionRepo) UpsertBatch(ctx context.Context, actions []models.CorporateAction) error {
	if len(actions) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, a := range actions {
		sql := `INSERT INTO corporate_actions (ticker, ex_date, action_type, ratio, dividend_amt, source)
		        VALUES ($1, $2, $3, $4, $5, $6)
		        ON CONFLICT (ticker, ex_date, action_type) DO NOTHING`
		batch.Queue(sql, a.Ticker, a.ExDate, a.ActionType, a.Ratio, a.DividendAmt, a.Source)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	_, err := br.Exec()
	if err != nil {
		return fmt.Errorf("corporateActionRepo.UpsertBatch: %w", err)
	}
	return nil
}

// GetByTickerDateRange retrieves corporate actions for a ticker within a date range.
func (r *CorporateActionRepo) GetByTickerDateRange(ctx context.Context, ticker string, start, end time.Time) ([]models.CorporateAction, error) {
	sql := `SELECT id, ticker, ex_date, action_type, ratio, dividend_amt, source, created_at
	        FROM corporate_actions
	        WHERE ticker = $1 AND ex_date >= $2 AND ex_date <= $3
	        ORDER BY ex_date ASC`

	rows, err := r.db.Query(ctx, sql, ticker, start, end)
	if err != nil {
		return nil, fmt.Errorf("corporateActionRepo.GetByTickerDateRange %s [%s, %s]: %w", ticker, start.Format("2006-01-02"), end.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var results []models.CorporateAction
	for rows.Next() {
		var a models.CorporateAction
		if err := rows.Scan(&a.ID, &a.Ticker, &a.ExDate, &a.ActionType, &a.Ratio, &a.DividendAmt, &a.Source, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan corporate_action: %w", err)
		}
		results = append(results, a)
	}
	return results, nil
}
