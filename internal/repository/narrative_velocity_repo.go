package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// NarrativeVelocityRepo handles persistence for narrative_velocity_daily.
type NarrativeVelocityRepo struct {
	db *pgxpool.Pool
}

// NewNarrativeVelocityRepo creates a NarrativeVelocityRepo.
func NewNarrativeVelocityRepo(db *pgxpool.Pool) *NarrativeVelocityRepo {
	return &NarrativeVelocityRepo{db: db}
}

const upsertNarrativeVelocitySQL = `
	INSERT INTO narrative_velocity_daily
		(ticker, date, news_freshness_score, coverage_accel_score, narrative_velocity,
		 headlines_24h, baseline_daily_rate, unique_sources_24h, avg_sources_30d)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (ticker, date) DO UPDATE SET
		news_freshness_score = EXCLUDED.news_freshness_score,
		coverage_accel_score = EXCLUDED.coverage_accel_score,
		narrative_velocity   = EXCLUDED.narrative_velocity,
		headlines_24h        = EXCLUDED.headlines_24h,
		baseline_daily_rate  = EXCLUDED.baseline_daily_rate,
		unique_sources_24h   = EXCLUDED.unique_sources_24h,
		avg_sources_30d      = EXCLUDED.avg_sources_30d,
		updated_at           = NOW()`

// UpsertBatch inserts or updates narrative velocity records for a given date.
// Uses ON CONFLICT (ticker, date) DO UPDATE for idempotent writes.
func (r *NarrativeVelocityRepo) UpsertBatch(ctx context.Context, records []models.NarrativeVelocityDaily) error {
	if len(records) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for i := range records {
		rec := &records[i]
		batch.Queue(upsertNarrativeVelocitySQL,
			rec.Ticker, rec.Date,
			rec.NewsFreshnessScore, rec.CoverageAccelScore, rec.NarrativeVelocity,
			rec.Headlines24h, rec.BaselineDailyRate,
			rec.UniqueSources24h, rec.AvgSources30d,
		)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for i := range records {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert narrative_velocity[%d] %s/%s: %w",
				i, records[i].Ticker, records[i].Date.Format("2006-01-02"), err)
		}
	}
	return nil
}

// Upsert inserts or updates a single narrative velocity record.
// Idempotent: re-running for the same (ticker, date) overwrites.
func (r *NarrativeVelocityRepo) Upsert(ctx context.Context, rec *models.NarrativeVelocityDaily) error {
	_, err := r.db.Exec(ctx, upsertNarrativeVelocitySQL,
		rec.Ticker, rec.Date,
		rec.NewsFreshnessScore, rec.CoverageAccelScore, rec.NarrativeVelocity,
		rec.Headlines24h, rec.BaselineDailyRate,
		rec.UniqueSources24h, rec.AvgSources30d,
	)
	if err != nil {
		return fmt.Errorf("UpsertNarrativeVelocity %s/%s: %w",
			rec.Ticker, rec.Date.Format("2006-01-02"), err)
	}
	return nil
}

// GetByTickerDate returns the narrative velocity record for a specific (ticker, date).
// Returns nil, nil when no row exists.
func (r *NarrativeVelocityRepo) GetByTickerDate(ctx context.Context, ticker string, date time.Time) (*models.NarrativeVelocityDaily, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, ticker, date, news_freshness_score, coverage_accel_score, narrative_velocity,
		       headlines_24h, baseline_daily_rate, unique_sources_24h, avg_sources_30d,
		       created_at, updated_at
		FROM narrative_velocity_daily
		WHERE ticker = $1 AND date = $2
	`, ticker, date)

	var n models.NarrativeVelocityDaily
	if err := row.Scan(
		&n.ID, &n.Ticker, &n.Date,
		&n.NewsFreshnessScore, &n.CoverageAccelScore, &n.NarrativeVelocity,
		&n.Headlines24h, &n.BaselineDailyRate,
		&n.UniqueSources24h, &n.AvgSources30d,
		&n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no narrative velocity found for ticker+date
		}
		return nil, fmt.Errorf("GetByTickerDate %s/%s: %w", ticker, date.Format("2006-01-02"), err)
	}
	return &n, nil
}

// GetByDate returns all narrative velocity records for a given date as a map keyed by ticker.
func (r *NarrativeVelocityRepo) GetByDate(ctx context.Context, date time.Time) (map[string]models.NarrativeVelocityDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, ticker, date, news_freshness_score, coverage_accel_score, narrative_velocity,
		       headlines_24h, baseline_daily_rate, unique_sources_24h, avg_sources_30d,
		       created_at, updated_at
		FROM narrative_velocity_daily
		WHERE date = $1
	`, date)
	if err != nil {
		return nil, fmt.Errorf("query narrative_velocity for %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	result := make(map[string]models.NarrativeVelocityDaily)
	for rows.Next() {
		var n models.NarrativeVelocityDaily
		if err := rows.Scan(
			&n.ID, &n.Ticker, &n.Date,
			&n.NewsFreshnessScore, &n.CoverageAccelScore, &n.NarrativeVelocity,
			&n.Headlines24h, &n.BaselineDailyRate,
			&n.UniqueSources24h, &n.AvgSources30d,
			&n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan narrative_velocity: %w", err)
		}
		result[n.Ticker] = n
	}
	return result, rows.Err()
}
