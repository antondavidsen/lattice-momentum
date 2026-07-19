package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// RankListRepo handles persistence for the daily_rank_lists table.
type RankListRepo struct {
	db dbPool // reuse the interface defined in market_inputs_repo.go
}

// NewRankListRepo creates a new RankListRepo backed by a live pool.
func NewRankListRepo(db *pgxpool.Pool) *RankListRepo {
	return &RankListRepo{db: db}
}

const upsertRankListSQL = `
	INSERT INTO daily_rank_lists (date, list_type, rank, ticker, score, reason)
	VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	ON CONFLICT (date, list_type, ticker) DO UPDATE SET
		rank       = EXCLUDED.rank,
		score      = EXCLUDED.score,
		reason     = EXCLUDED.reason`

// UpsertRankList inserts or replaces a single ranking row.
// Idempotent: re-running for the same (date, list_type, ticker) overwrites.
func (r *RankListRepo) UpsertRankList(ctx context.Context, m *models.DailyRankList) error {
	_, err := r.db.Exec(ctx, upsertRankListSQL,
		m.Date, string(m.ListType), m.Rank, m.Ticker, m.Score, m.Reason,
	)
	if err != nil {
		return fmt.Errorf("UpsertRankList %s %s %s: %w",
			m.Date.Format("2006-01-02"), m.ListType, m.Ticker, err)
	}
	return nil
}

// GetRankList returns all ranking rows for a given date and list type,
// ordered by rank ascending (1 = best).
func (r *RankListRepo) GetRankList(ctx context.Context, date time.Time, listType models.ListType) ([]models.DailyRankList, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, rank, ticker, score, reason, created_at
		FROM   daily_rank_lists
		WHERE  date = $1 AND list_type = $2
		ORDER  BY rank ASC
	`, date, string(listType))
	if err != nil {
		return nil, fmt.Errorf("GetRankList %s %s: %w", date.Format("2006-01-02"), listType, err)
	}
	defer rows.Close()

	var out []models.DailyRankList
	for rows.Next() {
		var m models.DailyRankList
		var lt string
		var reason []byte
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Rank, &m.Ticker, &m.Score, &reason, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetRankList scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		if len(reason) > 0 {
			m.Reason = append([]byte(nil), reason...)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DistinctTickersLastNDays returns all distinct tickers from daily_rank_lists
// where date >= today - days. Used by CorporateActionJob to know which tickers
// are active.
func (r *RankListRepo) DistinctTickersLastNDays(ctx context.Context, days int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ticker
		FROM   daily_rank_lists
		WHERE  date >= CURRENT_DATE - $1::integer
	`, days)
	if err != nil {
		return nil, fmt.Errorf("DistinctTickersLastNDays: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("DistinctTickersLastNDays scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetMostRecentRankList returns all rank list rows for the most recent date
// on or before the given date, for the specified list type.
// Returns nil, nil when no rows exist.
func (r *RankListRepo) GetMostRecentRankList(ctx context.Context, beforeOrEqual time.Time, listType models.ListType) ([]models.DailyRankList, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, rank, ticker, score, reason, created_at
		FROM   daily_rank_lists
		WHERE  list_type = $1
		  AND  date = (SELECT MAX(date) FROM daily_rank_lists WHERE date <= $2 AND list_type = $1)
		ORDER  BY rank ASC
	`, string(listType), beforeOrEqual)
	if err != nil {
		return nil, fmt.Errorf("GetMostRecentRankList %s %s: %w", beforeOrEqual.Format("2006-01-02"), listType, err)
	}
	defer rows.Close()

	var out []models.DailyRankList
	for rows.Next() {
		var m models.DailyRankList
		var lt string
		var reason []byte
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Rank, &m.Ticker, &m.Score, &reason, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetMostRecentRankList scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		if len(reason) > 0 {
			m.Reason = append([]byte(nil), reason...)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteRankList removes all rows for a given date and list type.
// Useful for full re-computation: delete then insert fresh rows.
func (r *RankListRepo) DeleteRankList(ctx context.Context, date time.Time, listType models.ListType) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM daily_rank_lists WHERE date = $1 AND list_type = $2
	`, date, string(listType))
	if err != nil {
		return fmt.Errorf("DeleteRankList %s %s: %w", date.Format("2006-01-02"), listType, err)
	}
	return nil
}

// GetAllRankLists returns all ranking rows across all list types for the given
// date, ordered by list_type then rank.  Used by the API to return the full
// daily watchlist in a single query.
func (r *RankListRepo) GetAllRankLists(ctx context.Context, date time.Time) ([]models.DailyRankList, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, rank, ticker, score, reason, created_at
		FROM   daily_rank_lists
		WHERE  date = $1
		ORDER  BY list_type, rank
	`, date)
	if err != nil {
		return nil, fmt.Errorf("GetAllRankLists %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.DailyRankList
	for rows.Next() {
		var m models.DailyRankList
		var lt string
		var reason []byte
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Rank, &m.Ticker, &m.Score, &reason, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetAllRankLists scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		if len(reason) > 0 {
			m.Reason = append([]byte(nil), reason...)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
