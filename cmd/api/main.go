// cmd/api: HTTP service entry-point.
// On startup it runs all pending DB migrations, then serves a /health endpoint.
package main

import (
	"ai-stock-service/internal/bootstrap"
	"ai-stock-service/internal/metrics"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-stock-service/internal/api"
	"ai-stock-service/internal/repository"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	b := bootstrap.MustStartup("momentum-api",
		bootstrap.WithoutDotenv(),
		bootstrap.WithHandlerOptions(&slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	defer b.Pool.Close()

	// Pre-create all metric label combinations so /metrics always exports them.
	metrics.TouchPipelineMetrics()

	cfg := b.Cfg
	pool := b.Pool

	// ── HTTP routes ───────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// Prometheus metrics — scraped by Grafana Alloy; no auth (nginx blocks external access).
	mux.Handle("GET /metrics", promhttp.Handler())

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			slog.Error("health check db ping failed", "error", err)
			http.Error(w, `{"status":"degraded"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Runtime config endpoint — public, returns the API key for the embedded
	// frontend so it doesn't need to be baked in at build time.
	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Only expose the API key; never leak other secrets.
		_ = json.NewEncoder(w).Encode(map[string]string{"apiKey": cfg.APIKey})
	})

	// ── API v1 ────────────────────────────────────────────────────────────────
	snapshotRepo := repository.NewSnapshotRepo(pool)
	tickerRepo := repository.NewTickerRepo(pool)
	tvSnapRepo := repository.NewTVSnapshotRepo(pool)
	apiHandler := api.NewHandler(snapshotRepo, tickerRepo, tvSnapRepo)
	apiHandler.RegisterRoutes(mux)

	// ── Embedded frontend (Alpha Engine MVP) ──────────────────────────────────
	// mux.Handle("/", http.FileServer(http.Dir("web/dist")))

	srv := &http.Server{
		Addr:    ":8080",
		Handler: api.MetricsMiddleware(api.CORSMiddleware(nil)(api.KeyMiddleware(cfg.APIKey)(api.RecoveryMiddleware(mux)))),
		// ReadTimeout covers the entire request read (headers + body).
		// Must be ≥ WriteTimeout or the connection is closed before the handler runs.
		ReadTimeout: 60 * time.Second,
		// ReadHeaderTimeout guards against slowloris attacks without
		// constraining legitimate large-body requests.
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout covers the full handler execution + response write.
		// The market-snapshot handler upserts up to ~600 tickers; give it
		// enough headroom even under DB load.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	// Use signal.NotifyContext for graceful shutdown on SIGINT or SIGTERM.
	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownCtx.Done()
	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
