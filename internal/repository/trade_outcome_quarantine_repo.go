package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TradeOutcomeQuarantineRepo persists rows that fail plausibility checks.
type TradeOutcomeQuarantineRepo struct {
	db *pgxpool.Pool
}

// NewTradeOutcomeQuarantineRepo creates a new TradeOutcomeQuarantineRepo.
func NewTradeOutcomeQuarantineRepo(db *pgxpool.Pool) *TradeOutcomeQuarantineRepo {
	return &TradeOutcomeQuarantineRepo{db: db}
}

// QuarantineTradeOutcome is the raw DB row for trade_outcomes_quarantine.
// Includes all trade_outcomes_daily fields plus quarantine metadata.
type QuarantineTradeOutcome struct {
	EntryDate            time.Time `db:"entry_date"`
	ListType             string    `db:"list_type"`
	Ticker               string    `db:"ticker"`
	Rank                 int       `db:"rank"`
	EntryPrice           float64   `db:"entry_price"`
	Return1D             *float64  `db:"return_1d"`
	Return2D             *float64  `db:"return_2d"`
	Return3D             *float64  `db:"return_3d"`
	Return4D             *float64  `db:"return_4d"`
	Return5D             *float64  `db:"return_5d"`
	Return10D            *float64  `db:"return_10d"`
	Return20D            *float64  `db:"return_20d"`
	MaxRunup20D          *float64  `db:"max_runup_20d"`
	MaxDrawdown20D       *float64  `db:"max_drawdown_20d"`
	EvaluatedDays        int       `db:"evaluated_days"`
	QuarantineReason     string    `db:"quarantine_reason"`
	CorporateActionCount int       `db:"corporate_action_count"`
	Resolution           string    `db:"resolution"` // pending / confirmed_genuine / adjusted / deleted_bad_data
}

// UpsertQuarantine inserts a quarantined trade outcome row.
// ON CONFLICT updates the row (for re-runs).
func (r *TradeOutcomeQuarantineRepo) UpsertQuarantine(ctx context.Context, q *QuarantineTradeOutcome) error {
	sql := `
		INSERT INTO trade_outcomes_quarantine (
			entry_date, list_type, ticker, rank, entry_price,
			return_1d, return_2d, return_3d, return_4d,
			return_5d, return_10d, return_20d,
			max_runup_20d, max_drawdown_20d, evaluated_days,
			quarantine_reason, corporate_action_count, resolution
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $14, $15,
			$16, $17, $18
		) ON CONFLICT (entry_date, list_type, ticker) DO UPDATE SET
			rank = EXCLUDED.rank,
			entry_price = EXCLUDED.entry_price,
			return_1d = EXCLUDED.return_1d,
			return_2d = EXCLUDED.return_2d,
			return_3d = EXCLUDED.return_3d,
			return_4d = EXCLUDED.return_4d,
			return_5d = EXCLUDED.return_5d,
			return_10d = EXCLUDED.return_10d,
			return_20d = EXCLUDED.return_20d,
			max_runup_20d = EXCLUDED.max_runup_20d,
			max_drawdown_20d = EXCLUDED.max_drawdown_20d,
			evaluated_days = EXCLUDED.evaluated_days,
			quarantine_reason = EXCLUDED.quarantine_reason,
			corporate_action_count = EXCLUDED.corporate_action_count,
			resolution = EXCLUDED.resolution,
			updated_at = NOW()`

	_, err := r.db.Exec(ctx, sql,
		q.EntryDate,
		q.ListType,
		q.Ticker,
		q.Rank,
		q.EntryPrice,
		q.Return1D,
		q.Return2D,
		q.Return3D,
		q.Return4D,
		q.Return5D,
		q.Return10D,
		q.Return20D,
		q.MaxRunup20D,
		q.MaxDrawdown20D,
		q.EvaluatedDays,
		q.QuarantineReason,
		q.CorporateActionCount,
		q.Resolution,
	)
	if err != nil {
		return fmt.Errorf("UpsertQuarantine %s %s %s: %w", q.EntryDate.Format("2006-01-02"), q.ListType, q.Ticker, err)
	}
	return nil
}

// GetQuarantinedByDate returns all quarantined rows for the given entry date.
func (r *TradeOutcomeQuarantineRepo) GetQuarantinedByDate(ctx context.Context, entryDate time.Time) ([]QuarantineTradeOutcome, error) {
	rows, err := r.db.Query(ctx, `
		SELECT entry_date, list_type, ticker, rank, entry_price,
			return_1d, return_2d, return_3d, return_4d,
			return_5d, return_10d, return_20d,
			max_runup_20d, max_drawdown_20d, evaluated_days,
			quarantine_reason, corporate_action_count, resolution
		FROM trade_outcomes_quarantine
		WHERE entry_date = $1
		ORDER BY list_type, rank
	`, entryDate)
	if err != nil {
		return nil, fmt.Errorf("GetQuarantinedByDate %s: %w", entryDate.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []QuarantineTradeOutcome
	for rows.Next() {
		var q QuarantineTradeOutcome
		if err := rows.Scan(
			&q.EntryDate, &q.ListType, &q.Ticker, &q.Rank, &q.EntryPrice,
			&q.Return1D, &q.Return2D, &q.Return3D, &q.Return4D,
			&q.Return5D, &q.Return10D, &q.Return20D,
			&q.MaxRunup20D, &q.MaxDrawdown20D, &q.EvaluatedDays,
			&q.QuarantineReason, &q.CorporateActionCount, &q.Resolution,
		); err != nil {
			return nil, fmt.Errorf("scan quarantine row: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
