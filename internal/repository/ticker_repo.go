package repository

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// TickerRepo handles persistence for the tickers table.
type TickerRepo struct {
	db *pgxpool.Pool
	// A mutex to serialize write operations to the tickers table.
	// This prevents potential race conditions in pgx's batching or concurrent
	// exec operations that can lead to memory corruption.
	mu sync.Mutex
}

// NewTickerRepo creates a new TickerRepo backed by a live pool.
func NewTickerRepo(db *pgxpool.Pool) *TickerRepo {
	return &TickerRepo{db: db}
}

// Upsert inserts or updates a ticker row (keyed on ticker symbol).
func (r *TickerRepo) Upsert(ctx context.Context, t *models.Ticker) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.Exec(ctx, `
		INSERT INTO tickers (ticker, name, sector, industry, exchange, country, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (ticker) DO UPDATE SET
			name      = EXCLUDED.name,
			sector    = EXCLUDED.sector,
			industry  = EXCLUDED.industry,
			exchange  = EXCLUDED.exchange,
			country   = EXCLUDED.country,
			updated_at = NOW()
	`, t.Ticker, t.Name, t.Sector, t.Industry, t.Exchange, t.Country)
	if err != nil {
		return fmt.Errorf("upsert ticker %s: %w", t.Ticker, err)
	}
	return nil
}

// UpsertBatch upserts a slice of tickers using a pgx pipeline batch.
//
// All statements are sent to Postgres in a single round-trip, which is
// dramatically faster than N individual Exec calls and avoids holding an
// explicit transaction open for an extended period (which can leave an
// "unexpected EOF on client connection with an open transaction" in the
// Postgres logs when the request context is cancelled).
//
// The upserts are idempotent (ON CONFLICT DO UPDATE), so partial delivery
// on context cancellation is acceptable – missing tickers will be picked
// up on the next run.
func (r *TickerRepo) UpsertBatch(ctx context.Context, tickers []models.Ticker) error {
	if len(tickers) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	const upsertSQL = `
		INSERT INTO tickers (ticker, name, sector, industry, exchange, country, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (ticker) DO UPDATE SET
			name       = EXCLUDED.name,
			sector     = EXCLUDED.sector,
			industry   = EXCLUDED.industry,
			exchange   = EXCLUDED.exchange,
			country    = EXCLUDED.country,
			updated_at = NOW()
	`

	batch := &pgx.Batch{}
	for i := range tickers {
		t := &tickers[i]
		batch.Queue(upsertSQL, t.Ticker, t.Name, t.Sector, t.Industry, t.Exchange, t.Country)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for i := range tickers {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert ticker[%d]: %w", i, err)
		}
	}
	return nil
}

// GetBySymbol fetches a single ticker by its symbol.
func (r *TickerRepo) GetBySymbol(ctx context.Context, ticker string) (*models.Ticker, error) {
	row := r.db.QueryRow(ctx, `
		SELECT ticker, name, sector, industry, exchange, country, priority, created_at, updated_at
		FROM tickers WHERE ticker = $1
	`, ticker)

	var t models.Ticker
	if err := row.Scan(
		&t.Ticker, &t.Name, &t.Sector, &t.Industry,
		&t.Exchange, &t.Country, &t.Priority, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("get ticker %s: %w", ticker, err)
	}
	return &t, nil
}

// EnsureExists inserts a placeholder row for symbol if it does not already exist.
// This is used before inserting candles for benchmark indices and sector ETFs
// that may not have full metadata populated yet.
// It uses ON CONFLICT DO NOTHING so it never overwrites existing ticker metadata.
// The priority is automatically set to CRITICAL for known benchmark / ETF tickers
// and NORMAL for all others.
func (r *TickerRepo) EnsureExists(ctx context.Context, symbol string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	priority := models.TickerPriorityNormal
	if models.IsCriticalTicker(symbol) {
		priority = models.TickerPriorityCritical
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO tickers (ticker, name, sector, industry, exchange, country, priority)
		VALUES ($1, $1, '', '', '', 'US', $2)
		ON CONFLICT (ticker) DO NOTHING
	`, symbol, priority)
	if err != nil {
		return fmt.Errorf("ensure ticker %s: %w", symbol, err)
	}
	return nil
}

// ListAll returns every ticker in the database.
func (r *TickerRepo) ListAll(ctx context.Context) ([]models.Ticker, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ticker, name, sector, industry, exchange, country, priority, created_at, updated_at
		FROM tickers ORDER BY ticker
	`)
	if err != nil {
		return nil, fmt.Errorf("list tickers: %w", err)
	}
	defer rows.Close()

	var out []models.Ticker
	for rows.Next() {
		var t models.Ticker
		if err := rows.Scan(
			&t.Ticker, &t.Name, &t.Sector, &t.Industry,
			&t.Exchange, &t.Country, &t.Priority, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan ticker: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
