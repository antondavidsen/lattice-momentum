// Package repository provides a repository layer for the candles_daily table in PostgreSQL.
package repository

import (
	"ai-stock-service/internal/db"
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// sanitizeF64 replaces NaN / ±Inf with 0 so pgx never tries to encode a
// special float64 as a PostgreSQL DOUBLE PRECISION value.
func sanitizeF64(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// sanitizeF64Ptr applies sanitizeF64 to a nullable float pointer.
func sanitizeF64Ptr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := sanitizeF64(*p)
	return &v
}

// CandlesDailyRepo handles persistence for the unified candles_daily table.
type CandlesDailyRepo struct {
	db *pgxpool.Pool
	// UpsertBatch is transactionally safe, but pgx v5.9.1's COPY protocol
	// implementation appears to have a race condition when used concurrently.
	// A mutex ensures that only one bulk insert runs at a time.
	mu sync.Mutex
}

// NewCandlesDailyRepo creates a new CandlesDailyRepo.
func NewCandlesDailyRepo(pgDB *pgxpool.Pool) *CandlesDailyRepo {
	return &CandlesDailyRepo{db: pgDB}
}

// stageColumns are the columns written to / read from candles_stage.
var stageColumns = []string{
	"ticker", "date",
	"open", "high", "low", "close", "adjusted_close",
	"volume", "provider",
}

// createStageSQL creates a lightweight temp table that lives only for the
// duration of the current transaction (ON COMMIT DROP).
// No id, no FK, no unique constraint — those are enforced in the merge step.
const createStageSQL = `
	CREATE TEMP TABLE candles_stage (
		ticker          TEXT             NOT NULL,
		date            DATE             NOT NULL,
		open            DOUBLE PRECISION NOT NULL,
		high            DOUBLE PRECISION NOT NULL,
		low             DOUBLE PRECISION NOT NULL,
		close           DOUBLE PRECISION NOT NULL,
		adjusted_close  DOUBLE PRECISION,
		volume          BIGINT           NOT NULL,
		provider        TEXT             NOT NULL
	) ON COMMIT DROP`

// mergeSQL promotes rows from the staging table into candles_daily, handling
// conflicts by overwriting the OHLCV and provider fields.
const mergeSQL = `
	INSERT INTO candles_daily
		(ticker, date, open, high, low, close, adjusted_close, volume, provider)
	SELECT ticker, date, open, high, low, close, adjusted_close, volume, provider
	FROM   candles_stage
	ON CONFLICT (ticker, date) DO UPDATE SET
		open           = EXCLUDED.open,
		high           = EXCLUDED.high,
		low            = EXCLUDED.low,
		close          = EXCLUDED.close,
		adjusted_close = EXCLUDED.adjusted_close,
		volume         = EXCLUDED.volume,
		provider       = EXCLUDED.provider`

// UpsertBatch inserts or updates a slice of candles using PostgreSQL's COPY
// protocol via a temp staging table.
//
// NOTE: Futures symbols (ES1!, NQ1!, RTY1!) require the Polygon INDICES
// namespace prefix (I:ES1!, I:NQ1!, I:RTY1!) in the API call. The ticker
// stored in candles_daily is the raw symbol without the prefix — the prefix
// is only used at the HTTP request layer. candles_daily has no enum constraint
// on ticker, so no schema change is needed for new symbols.
//
// WHY not pgx.SendBatch:
//
//	pgx v5.9.1 / Go 1.26 has a race in QueryExecModeCacheStatement that
//	corrupts the StatementDescription pointer (sd ≈ 0x6b6) in
//	ExtendedQueryBuilder.Build under concurrent goroutines, causing SIGSEGV.
//	CopyFrom uses a completely different wire protocol (COPY FROM STDIN) —
//	no prepared statements, no ExtendedQueryBuilder, no statement cache.
func (r *CandlesDailyRepo) UpsertBatch(ctx context.Context, candles []models.CandleDaily) error {
	if len(candles) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	conn, err := db.AcquireWithMetrics(ctx, r.db)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	transaction, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // deferred rollback; error is intentionally discarded in favor of the commit/return error path

	// Create the per-transaction staging table.
	if _, err := transaction.Exec(ctx, createStageSQL); err != nil {
		return fmt.Errorf("create stage: %w", err)
	}

	// Bulk-copy all rows into staging in one COPY FROM STDIN round-trip.
	_, err = transaction.CopyFrom(
		ctx,
		pgx.Identifier{"candles_stage"},
		stageColumns,
		pgx.CopyFromSlice(len(candles), func(i int) ([]any, error) {
			candle := candles[i]
			return []any{
				candle.Ticker, candle.Date,
				sanitizeF64(candle.Open), sanitizeF64(candle.High),
				sanitizeF64(candle.Low), sanitizeF64(candle.Close),
				sanitizeF64Ptr(candle.AdjustedClose),
				candle.Volume, candle.Provider,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy to stage: %w", err)
	}

	// Merge staging into the live table (handles ON CONFLICT).
	if _, err := transaction.Exec(ctx, mergeSQL); err != nil {
		return fmt.Errorf("merge to candles_daily: %w", err)
	}

	return transaction.Commit(ctx)
}

// LatestDate returns the most recent date stored for ticker,
// or time.Time{} (zero) if no rows exist yet.
func (r *CandlesDailyRepo) LatestDate(ctx context.Context, ticker string) (time.Time, error) {
	var latestDate time.Time
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(date), '1970-01-01'::date) FROM candles_daily WHERE ticker = $1`,
		ticker,
	).Scan(&latestDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("latest date %s: %w", ticker, err)
	}
	return latestDate, nil
}

// LatestDates returns the latest stored date for every ticker in the table.
// Useful for computing incremental fetch windows in bulk.
func (r *CandlesDailyRepo) LatestDates(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.db.Query(ctx,
		`SELECT ticker, MAX(date) FROM candles_daily GROUP BY ticker`,
	)
	if err != nil {
		return nil, fmt.Errorf("latest dates: %w", err)
	}
	defer rows.Close()

	out := make(map[string]time.Time)
	for rows.Next() {
		var ticker string
		var d time.Time
		if err := rows.Scan(&ticker, &d); err != nil {
			return nil, fmt.Errorf("scan latest date: %w", err)
		}
		out[strings.Clone(ticker)] = d
	}
	return out, rows.Err()
}

// GetCandles returns all OHLCV rows for ticker in the closed interval [from, toTime],
// ordered ascending by date.
func (r *CandlesDailyRepo) GetCandles(ctx context.Context, ticker string, from, toTime time.Time) ([]models.CandleDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, ticker, date, open, high, low, close, adjusted_close, volume, provider, created_at
		FROM   candles_daily
		WHERE  ticker = $1
		  AND  date BETWEEN $2 AND $3
		ORDER  BY date ASC
	`, ticker, from, toTime)
	if err != nil {
		return nil, fmt.Errorf("query candles %s: %w", ticker, err)
	}
	defer rows.Close()

	var out []models.CandleDaily
	for rows.Next() {
		var candle models.CandleDaily
		if err := rows.Scan(
			&candle.ID, &candle.Ticker, &candle.Date,
			&candle.Open, &candle.High, &candle.Low, &candle.Close, &candle.AdjustedClose,
			&candle.Volume, &candle.Provider, &candle.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan candle: %w", err)
		}
		candle.Ticker = strings.Clone(candle.Ticker)
		candle.Provider = strings.Clone(candle.Provider)
		out = append(out, candle)
	}
	return out, rows.Err()
}

// CountByTicker returns the number of stored candles per ticker.
// Handy for health-check and monitoring endpoints.
func (r *CandlesDailyRepo) CountByTicker(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.Query(ctx,
		`SELECT ticker, COUNT(*) FROM candles_daily GROUP BY ticker ORDER BY ticker`,
	)
	if err != nil {
		return nil, fmt.Errorf("count by ticker: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var ticker string
		var cnt int64
		if err := rows.Scan(&ticker, &cnt); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		out[strings.Clone(ticker)] = cnt
	}
	return out, rows.Err()
}

// GetAvgVolumes returns the 20-day average daily volume for each of the given
// tickers in a single query. This is optimised for the premarket universe
// builder which needs avg volumes for potentially hundreds of tickers at once.
func (r *CandlesDailyRepo) GetAvgVolumes(ctx context.Context, tickers []string, lookbackDays int) (map[string]int64, error) {
	if len(tickers) == 0 {
		return nil, nil //nolint:nilnil // empty input slice returns nil map, valid Go idiom
	}
	if lookbackDays <= 0 {
		lookbackDays = 20
	}

	// Use a lateral join to compute the average of the most recent N rows per ticker.
	rows, err := r.db.Query(ctx, `
		SELECT t.ticker, COALESCE(AVG(c.volume), 0)::BIGINT AS avg_vol
		FROM   unnest($1::TEXT[]) AS t(ticker)
		LEFT JOIN LATERAL (
			SELECT volume
			FROM   candles_daily
			WHERE  ticker = t.ticker
			ORDER  BY date DESC
			LIMIT  $2
		) c ON TRUE
		GROUP BY t.ticker
		HAVING COUNT(c.volume) >= 5
	`, tickers, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("GetAvgVolumes: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64, len(tickers))
	for rows.Next() {
		var ticker string
		var avgVol int64
		if err := rows.Scan(&ticker, &avgVol); err != nil {
			return nil, fmt.Errorf("scan avg volume: %w", err)
		}
		if avgVol > 0 {
			result[strings.Clone(ticker)] = avgVol
		}
	}
	return result, rows.Err()
}
