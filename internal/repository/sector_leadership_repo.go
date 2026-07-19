package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// SectorLeadershipRepo handles persistence for the sector_leadership_daily table.
type SectorLeadershipRepo struct {
	db dbPool
}

// NewSectorLeadershipRepo creates a new SectorLeadershipRepo.
func NewSectorLeadershipRepo(db *pgxpool.Pool) *SectorLeadershipRepo {
	return &SectorLeadershipRepo{db: db}
}

const upsertSectorLeadershipBatchSQL = `
	INSERT INTO sector_leadership_daily (
		ticker, date, sector_etf,
		leadership_score, ticker_return_5d, sector_return_5d, is_leader
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7
	)
	ON CONFLICT (ticker, date) DO UPDATE SET
		sector_etf        = EXCLUDED.sector_etf,
		leadership_score  = EXCLUDED.leadership_score,
		ticker_return_5d  = EXCLUDED.ticker_return_5d,
		sector_return_5d  = EXCLUDED.sector_return_5d,
		is_leader         = EXCLUDED.is_leader,
		updated_at        = NOW()`

// UpsertBatch persists a batch of sector leadership records.
// Each record is upserted individually; the operation is idempotent.
func (r *SectorLeadershipRepo) UpsertBatch(ctx context.Context, records []models.SectorLeadershipDaily) error {
	for i := range records {
		rec := &records[i]
		_, err := r.db.Exec(ctx, upsertSectorLeadershipBatchSQL,
			rec.Ticker,
			rec.Date,
			rec.SectorETF,
			rec.LeadershipScore,
			rec.TickerReturn5d,
			rec.SectorReturn5d,
			rec.IsLeader,
		)
		if err != nil {
			return fmt.Errorf("UpsertBatch %s/%s: %w", rec.Date.Format("2006-01-02"), rec.Ticker, err)
		}
	}
	return nil
}

const selectSectorLeadershipByDateSQL = `
	SELECT id, ticker, date, sector_etf,
	       leadership_score, ticker_return_5d, sector_return_5d, is_leader,
	       created_at, updated_at
	FROM   sector_leadership_daily
	WHERE  date = $1`

// GetByDate returns all sector leadership records for a given date, keyed by ticker.
// Returns an empty map (not nil) when no records exist.
func (r *SectorLeadershipRepo) GetByDate(ctx context.Context, date time.Time) (map[string]models.SectorLeadershipDaily, error) {
	rows, err := r.db.Query(ctx, selectSectorLeadershipByDateSQL, date)
	if err != nil {
		return nil, fmt.Errorf("GetByDate %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	out := make(map[string]models.SectorLeadershipDaily)
	for rows.Next() {
		var rec models.SectorLeadershipDaily
		if err := rows.Scan(
			&rec.ID, &rec.Ticker, &rec.Date, &rec.SectorETF,
			&rec.LeadershipScore, &rec.TickerReturn5d, &rec.SectorReturn5d, &rec.IsLeader,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetByDate %s scan: %w", date.Format("2006-01-02"), err)
		}
		out[rec.Ticker] = rec
	}
	return out, rows.Err()
}

const selectLeadersByDateSQL = `
	SELECT ticker, is_leader
	FROM   sector_leadership_daily
	WHERE  date = $1`

// GetLeadersForDate returns a map of ticker → is_leader for the given date.
// Returns an empty map (not nil) when no records exist.
func (r *SectorLeadershipRepo) GetLeadersForDate(ctx context.Context, date time.Time) (map[string]bool, error) {
	rows, err := r.db.Query(ctx, selectLeadersByDateSQL, date)
	if err != nil {
		return nil, fmt.Errorf("GetLeadersForDate %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var ticker string
		var isLeader bool
		if err := rows.Scan(&ticker, &isLeader); err != nil {
			return nil, fmt.Errorf("GetLeadersForDate %s scan: %w", date.Format("2006-01-02"), err)
		}
		out[ticker] = isLeader
	}
	return out, rows.Err()
}
