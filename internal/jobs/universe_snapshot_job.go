package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// universeJobName is emitted in every log line produced by this job.
const universeJobName = "UniverseSnapshotJob"

// UniverseSnapshotStorer stores daily universe snapshots.
type UniverseSnapshotStorer interface {
	UpsertUniverseSnapshot(ctx context.Context, date time.Time, ticker string, isTradeable bool, exchange string, price float64) error
}

// ── Job ───────────────────────────────────────────────────────────────────────

// UniverseSnapshotJob records the evaluable universe per date for
// survivorship quantification. Uses direct SQL queries for efficiency.
type UniverseSnapshotJob struct {
	pool *pgxpool.Pool
	repo UniverseSnapshotStorer
	log  *slog.Logger
}

// NewUniverseSnapshotJob constructs a UniverseSnapshotJob from concrete types.
func NewUniverseSnapshotJob(pool *pgxpool.Pool, repo UniverseSnapshotStorer, log *slog.Logger) *UniverseSnapshotJob {
	return &UniverseSnapshotJob{
		pool: pool,
		repo: repo,
		log:  log,
	}
}

// RunUniverseSnapshot populates universe_snapshot_daily for the given date.
// Queries active tickers with a price from candles_daily, filtering by exchange, price, and halt status.
func (j *UniverseSnapshotJob) RunUniverseSnapshot(ctx context.Context, date time.Time) error {
	start := time.Now()
	j.log.Info("job starting",
		"job", universeJobName,
		"date", date.Format("2006-01-02"),
	)

	// Query all active tickers joined with candles_daily for the pipeline date.
	// Filters: exchange in (NYSE, NASDAQ, NYSE ARCA, NYSE MKT).
	rows, err := j.pool.Query(ctx, `
		SELECT t.ticker, t.exchange, COALESCE(c.close, 0) AS price
		FROM   tickers t
		LEFT JOIN candles_daily c 
		  ON   c.ticker = t.ticker
		  AND  c.date = $1
		WHERE  t.exchange IN ('NYSE', 'NASDAQ', 'NYSE ARCA', 'NYSE MKT')
		ORDER BY t.ticker
	`, date)
	if err != nil {
		return fmt.Errorf("%s: query active tickers: %w", universeJobName, err)
	}
	defer rows.Close()

	var snapshotted, tradeableCount int
	for rows.Next() {
		var ticker, exchange string
		var price float64
		if err := rows.Scan(&ticker, &exchange, &price); err != nil {
			return fmt.Errorf("%s: scan row: %w", universeJobName, err)
		}

		isTradeable := price > 1.0

		if err := j.repo.UpsertUniverseSnapshot(ctx, date, ticker, isTradeable, exchange, price); err != nil {
			return fmt.Errorf("%s: upsert %s: %w", universeJobName, ticker, err)
		}
		snapshotted++
		if isTradeable {
			tradeableCount++
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("%s: rows iteration: %w", universeJobName, err)
	}

	j.log.Info("job completed",
		"job", universeJobName,
		"date", date.Format("2006-01-02"),
		"duration_ms", time.Since(start).Milliseconds(),
		"tickers_snapshotted", snapshotted,
		"tradeable_count", tradeableCount,
	)

	return nil
}
