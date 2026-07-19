// Package db provides the PostgreSQL connection pool and migration runner.
package db

import (
	"ai-stock-service/internal/config"
	"ai-stock-service/internal/metrics"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for database/sql (goose)
	pgvector "github.com/pgvector/pgvector-go"
	"github.com/pressly/goose/v3"
	"github.com/sethvargo/go-retry"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// NewPool creates and validates a pgxpool connection pool.
// pgvector types are registered on every new connection so that
// the vector(1536) columns can be read/written transparently.
// Retries up to 5 times with exponential backoff (2s → 32s) so that a cold
// Docker start where the Go binary is ready before pg_isready passes does not
// cause an immediate fatal exit.
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	// Explicit pool sizing — default of 4 is too small for concurrent HTTP requests.
	poolCfg.MaxConns = 20
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	// ── WORKAROUND: pgx v5.9.1 / Go 1.26 statement-cache race ────────────────
	// The default QueryExecModeCacheStatement has a data race in
	// ExtendedQueryBuilder.Build that corrupts StatementDescription pointers
	// when multiple goroutines execute queries through the same pool.
	// This manifests as "runtime: marked free object in span …" panics during
	// GC (see candles_daily_repo.go for the original diagnosis).
	//
	// CopyFrom (used by CandlesDailyRepo.UpsertBatch) already bypasses this
	// via the COPY wire protocol, but all regular Query/QueryRow/Exec calls
	// still hit the cache.  Switching the pool default to SimpleProtocol
	// avoids the extended-protocol statement cache entirely.  Parameters are
	// safely escaped client-side by pgx; the only cost is the loss of
	// server-side prepared-statement plan caching — negligible for a batch
	// pipeline that runs once per day.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// Register pgvector types on every fresh connection in the pool.
	poolCfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterDefaultPgType(pgvector.Vector{}, "vector")
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	// Retry Ping with exponential backoff — handles the race between the Go
	// binary starting and PostgreSQL becoming ready to accept connections.
	backoff := retry.NewExponential(2 * time.Second)
	backoff = retry.WithMaxRetries(5, backoff)
	backoff = retry.WithCappedDuration(32*time.Second, backoff)

	if err := retry.Do(ctx, backoff, func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return retry.RetryableError(fmt.Errorf("ping postgres: %w", err))
		}
		return nil
	}); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// Migrate runs all pending goose migrations embedded in the binary.
// Safe to call on every startup – goose is idempotent.
func Migrate(cfg *config.Config) error {
	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db for migrations: %w", err)
	}
	defer func() { _ = database.Close() }()

	goose.SetBaseFS(migrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.Up(database, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// MigrateDown rolls back all applied migrations (useful in tests).
func MigrateDown(cfg *config.Config) error {
	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db for migrations: %w", err)
	}
	defer func() { _ = database.Close() }()

	goose.SetBaseFS(migrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	return goose.DownTo(database, "migrations", 0)
}

// AcquireWithMetrics acquires a connection from the pool and records the
// outcome on the db_pool_acquired_total / db_pool_errors_total counters.
// Use this instead of pool.Acquire(ctx) anywhere that needs instrumentation.
func AcquireWithMetrics(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		metrics.DBPoolErrorsTotal.Inc()
		return nil, err
	}
	metrics.DBPoolAcquiredTotal.Inc()
	return conn, nil
}
