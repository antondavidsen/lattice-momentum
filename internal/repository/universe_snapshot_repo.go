package repository

import (
	"context"
	"fmt"
	"time"
)

// UniverseSnapshotRepo handles persistence for the universe_snapshot_daily table.
type UniverseSnapshotRepo struct {
	db dbPool
}

// NewUniverseSnapshotRepo creates a new UniverseSnapshotRepo backed by a live pool.
func NewUniverseSnapshotRepo(db dbPool) *UniverseSnapshotRepo {
	return &UniverseSnapshotRepo{db: db}
}

// UpsertUniverseSnapshot inserts or replaces a row in universe_snapshot_daily.
func (r *UniverseSnapshotRepo) UpsertUniverseSnapshot(ctx context.Context, date time.Time, ticker string, isTradeable bool, exchange string, price float64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO universe_snapshot_daily (date, ticker, is_tradeable, exchange, price_at_close)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (date, ticker) DO UPDATE SET
			is_tradeable  = EXCLUDED.is_tradeable,
			exchange      = EXCLUDED.exchange,
			price_at_close = EXCLUDED.price_at_close
	`, date, ticker, isTradeable, exchange, price)
	if err != nil {
		return fmt.Errorf("upsert universe snapshot %s/%s: %w", date.Format("2006-01-02"), ticker, err)
	}
	return nil
}

// GetTradeableOnDate returns all tickers that were tradeable on the given date.
func (r *UniverseSnapshotRepo) GetTradeableOnDate(ctx context.Context, date time.Time) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ticker
		FROM   universe_snapshot_daily
		WHERE  date = $1 AND is_tradeable = true
		ORDER BY ticker
	`, date)
	if err != nil {
		return nil, fmt.Errorf("get tradeable on %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
