// Package bootstrap provides a single-call initialiser that standardises the
// repetitive startup sequence shared by every CLI entrypoint in cmd/.
//
// Usage:
//
//	b := bootstrap.MustStartup("my-service")
//	defer b.Pool.Close()
//	// … use b.Cfg, b.Pool, b.Logger, b.Ctx …
//
// Optional behaviour modifiers:
//
//	b := bootstrap.MustStartup("my-service",
//	    bootstrap.WithoutDotenv(),
//	    bootstrap.WithoutSetDefault(),
//	    bootstrap.WithoutMigrations(),
//	    bootstrap.WithHandlerOptions(&slog.HandlerOptions{Level: slog.LevelInfo}),
//	)
package bootstrap

import (
	"context"
	"log/slog"
	"os"

	"ai-stock-service/internal/config"
	"ai-stock-service/internal/db"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// Bootstrapper holds the values produced by MustStartup so callers can
// access them without declaring their own local variables.
type Bootstrapper struct {
	Cfg    *config.Config
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Ctx    context.Context
}

// StartupOption configures the behaviour of MustStartup.
type StartupOption func(*startupConfig)

type startupConfig struct {
	skipDotenv     bool
	skipSetDefault bool
	skipMigrations bool
	handlerOptions *slog.HandlerOptions
}

// WithoutDotenv skips the [godotenv.Load] call.  Use when .env files are not
// expected (e.g. inside Docker containers where configuration comes from
// environment variables directly).
func WithoutDotenv() StartupOption {
	return func(c *startupConfig) { c.skipDotenv = true }
}

// WithoutSetDefault skips calling [slog.SetDefault] with the created logger.
// Use for lightweight CLI tools that do not want to replace the process-wide
// default logger.
func WithoutSetDefault() StartupOption {
	return func(c *startupConfig) { c.skipSetDefault = true }
}

// WithoutMigrations skips the [db.Migrate] call.  Use for read-only or
// diagnostic entrypoints that should not mutate the schema.
func WithoutMigrations() StartupOption {
	return func(c *startupConfig) { c.skipMigrations = true }
}

// WithHandlerOptions sets [slog.HandlerOptions] on the JSON handler (for
// example to control the minimum log level).  When nil (the default) the
// JSON handler is created with nil options, which means all levels are
// output.
func WithHandlerOptions(opts *slog.HandlerOptions) StartupOption {
	return func(c *startupConfig) { c.handlerOptions = opts }
}

// MustStartup performs the standard service initialisation sequence:
//
//  1. Load .env via [godotenv.Load] (errors are silently ignored — the file
//     is optional, e.g. absent inside Docker containers).  Skipped when the
//     [WithoutDotenv] option is supplied.
//  2. Create a JSON-based [slog.Logger] with a service_name attribute set to
//     the provided serviceName.
//  3. Set the logger as the process-wide default via [slog.SetDefault].
//     Skipped when the [WithoutSetDefault] option is supplied.
//  4. Load application config via [config.Load].
//  5. Create a [pgxpool.Pool] via [db.NewPool].
//  6. Run pending database migrations via [db.Migrate].
//     Skipped when the [WithoutMigrations] option is supplied.
//  7. Return a [Bootstrapper] holding all created values.
//
// On any fatal step it logs a structured error and calls [os.Exit](1). The
// caller should still defer b.Pool.Close().
func MustStartup(serviceName string, opts ...StartupOption) *Bootstrapper {
	var cfg startupConfig
	for _, o := range opts {
		o(&cfg)
	}

	// Step 1 — .env (optional, silently ignored when absent).
	if !cfg.skipDotenv {
		_ = godotenv.Load()
	}

	// Step 2+3 — structured JSON logger with service identity.
	baseHandler := slog.NewJSONHandler(os.Stdout, cfg.handlerOptions)
	logger := slog.New(baseHandler).With("service_name", serviceName)
	if !cfg.skipSetDefault {
		slog.SetDefault(logger)
	}

	// Step 4 — application configuration.
	appCfg, err := config.Load()
	if err != nil {
		slog.Error("bootstrap: config", "error", err)
		os.Exit(1)
	}

	// Step 5 — database connection pool (retries internally).
	ctx := context.Background()
	pool, err := db.NewPool(ctx, appCfg)
	if err != nil {
		slog.Error("bootstrap: pool", "error", err)
		os.Exit(1)
	}

	// Step 6 — schema migrations (idempotent, safe to run every startup).
	if !cfg.skipMigrations {
		if err := db.Migrate(appCfg); err != nil {
			slog.Error("bootstrap: migrations", "error", err)
			os.Exit(1)
		}
	}

	return &Bootstrapper{
		Cfg:    appCfg,
		Pool:   pool,
		Logger: logger,
		Ctx:    ctx,
	}
}
