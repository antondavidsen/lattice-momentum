package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// SnapshotRepo handles persistence for the market_snapshots table.
type SnapshotRepo struct {
	db *pgxpool.Pool
}

// NewSnapshotRepo creates a new SnapshotRepo backed by a live pool.
func NewSnapshotRepo(db *pgxpool.Pool) *SnapshotRepo {
	return &SnapshotRepo{db: db}
}

// Upsert stores a market snapshot, overwriting an existing row for the same date.
func (r *SnapshotRepo) Upsert(ctx context.Context, s *models.MarketSnapshot) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO market_snapshots
			(id, snapshot_date, momentum, episodic_pivots, market_leaders, row_counts,
			 open, high, low)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9)
		ON CONFLICT (snapshot_date) DO UPDATE SET
			momentum        = EXCLUDED.momentum,
			episodic_pivots = EXCLUDED.episodic_pivots,
			market_leaders  = EXCLUDED.market_leaders,
			row_counts      = EXCLUDED.row_counts,
			open            = EXCLUDED.open,
			high            = EXCLUDED.high,
			low             = EXCLUDED.low,
			received_at     = NOW()
	`,
		s.ID, s.SnapshotDate,
		s.Momentum, s.EpisodicPivots, s.MarketLeaders, s.RowCounts,
		s.Open, s.High, s.Low,
	)
	if err != nil {
		return fmt.Errorf("upsert market snapshot %s: %w", s.SnapshotDate.Format("2006-01-02"), err)
	}
	return nil
}

// GetByDate returns the snapshot for a specific date.
func (r *SnapshotRepo) GetByDate(ctx context.Context, date time.Time) (*models.MarketSnapshot, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, snapshot_date, momentum, episodic_pivots, market_leaders, row_counts,
		       open, high, low, received_at
		FROM market_snapshots
		WHERE snapshot_date = $1
	`, date)

	var s models.MarketSnapshot
	var momentum, episodic, leaders, counts []byte
	if err := row.Scan(
		&s.ID, &s.SnapshotDate,
		&momentum, &episodic, &leaders, &counts,
		&s.Open, &s.High, &s.Low,
		&s.ReceivedAt,
	); err != nil {
		return nil, fmt.Errorf("get snapshot %s: %w", date.Format("2006-01-02"), err)
	}
	s.Momentum = append(json.RawMessage(nil), momentum...)
	s.EpisodicPivots = append(json.RawMessage(nil), episodic...)
	s.MarketLeaders = append(json.RawMessage(nil), leaders...)
	s.RowCounts = append(json.RawMessage(nil), counts...)
	return &s, nil
}

// ListRecent returns the N most recent snapshot summaries (no full JSONB screener data).
func (r *SnapshotRepo) ListRecent(ctx context.Context, limit int) ([]models.MarketSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, snapshot_date, row_counts, open, high, low, received_at
		FROM market_snapshots
		ORDER BY snapshot_date DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent snapshots: %w", err)
	}
	defer rows.Close()

	var out []models.MarketSnapshot
	for rows.Next() {
		var s models.MarketSnapshot
		var counts []byte
		if err := rows.Scan(&s.ID, &s.SnapshotDate, &counts, &s.Open, &s.High, &s.Low, &s.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		s.RowCounts = append(json.RawMessage(nil), counts...)
		out = append(out, s)
	}
	return out, rows.Err()
}
