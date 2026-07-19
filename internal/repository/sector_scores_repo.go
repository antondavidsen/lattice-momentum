package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// SectorScoresRepo handles persistence for the sector_scores_daily table.
type SectorScoresRepo struct {
	db dbPool // reuse the interface defined in market_inputs_repo.go
}

// NewSectorScoresRepo creates a new SectorScoresRepo backed by a live pool.
func NewSectorScoresRepo(db *pgxpool.Pool) *SectorScoresRepo {
	return &SectorScoresRepo{db: db}
}

const upsertSectorScoreSQL = `
	INSERT INTO sector_scores_daily (
		date, etf,
		perf_1m, perf_3m, rs_vs_spy_3m,
		above_sma50, above_sma200,
		trend_score, score, label
	) VALUES (
		$1, $2,
		$3, $4, $5,
		$6, $7,
		$8, $9, $10
	)
	ON CONFLICT (date, etf) DO UPDATE SET
		perf_1m       = EXCLUDED.perf_1m,
		perf_3m       = EXCLUDED.perf_3m,
		rs_vs_spy_3m  = EXCLUDED.rs_vs_spy_3m,
		above_sma50   = EXCLUDED.above_sma50,
		above_sma200  = EXCLUDED.above_sma200,
		trend_score   = EXCLUDED.trend_score,
		score         = EXCLUDED.score,
		label         = EXCLUDED.label,
		updated_at    = NOW()`

// UpsertSectorScore inserts or replaces a single sector score row.
// Idempotent: re-running for the same (date, etf) overwrites the previous row.
func (r *SectorScoresRepo) UpsertSectorScore(ctx context.Context, m *models.SectorScoreDaily) error {
	_, err := r.db.Exec(ctx, upsertSectorScoreSQL,
		m.Date, m.ETF,
		m.Perf1M, m.Perf3M, m.RSvsSPY3M,
		m.AboveSMA50, m.AboveSMA200,
		m.TrendScore, m.Score, m.Label,
	)
	if err != nil {
		return fmt.Errorf("UpsertSectorScore %s %s: %w", m.Date.Format("2006-01-02"), m.ETF, err)
	}
	return nil
}

// GetSectorScores returns all sector score rows for the given date, ordered by
// score descending (strongest sector first).
func (r *SectorScoresRepo) GetSectorScores(ctx context.Context, date time.Time) ([]models.SectorScoreDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT date, etf,
		       perf_1m, perf_3m, rs_vs_spy_3m,
		       above_sma50, above_sma200,
		       trend_score, score, label,
		       created_at, updated_at
		FROM   sector_scores_daily
		WHERE  date = $1
		ORDER  BY score DESC
	`, date)
	if err != nil {
		return nil, fmt.Errorf("GetSectorScores %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.SectorScoreDaily
	for rows.Next() {
		var m models.SectorScoreDaily
		if err := rows.Scan(
			&m.Date, &m.ETF,
			&m.Perf1M, &m.Perf3M, &m.RSvsSPY3M,
			&m.AboveSMA50, &m.AboveSMA200,
			&m.TrendScore, &m.Score, &m.Label,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetSectorScores scan: %w", err)
		}
		m.ETF = strings.Clone(m.ETF)
		m.Label = strings.Clone(m.Label)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetLatest returns the most recent set of sector scores (all ETFs for one date).
func (r *SectorScoresRepo) GetLatest(ctx context.Context) ([]models.SectorScoreDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT date, etf,
		       perf_1m, perf_3m, rs_vs_spy_3m,
		       above_sma50, above_sma200,
		       trend_score, score, label,
		       created_at, updated_at
		FROM   sector_scores_daily
		WHERE  date = (SELECT MAX(date) FROM sector_scores_daily)
		ORDER  BY score DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetLatest sector_scores: %w", err)
	}
	defer rows.Close()

	var out []models.SectorScoreDaily
	for rows.Next() {
		var m models.SectorScoreDaily
		if err := rows.Scan(
			&m.Date, &m.ETF,
			&m.Perf1M, &m.Perf3M, &m.RSvsSPY3M,
			&m.AboveSMA50, &m.AboveSMA200,
			&m.TrendScore, &m.Score, &m.Label,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetLatest sector_scores scan: %w", err)
		}
		m.ETF = strings.Clone(m.ETF)
		m.Label = strings.Clone(m.Label)
		out = append(out, m)
	}
	return out, rows.Err()
}
