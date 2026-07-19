# Makefile — Momentum AI
# ──────────────────────────────────────────────────────────────────────────────
# Usage:
#   make build          Build all Go binaries locally
#   make test           Run all Go tests
#   make test-cover     Run tests with coverage (with .coverageignore filtering)
#   make lint           Run golangci-lint (install: brew install golangci-lint)
#   make run            Start the full stack with Docker Compose (uses layer cache)
#   make rebuild        Force full rebuild with --no-cache --pull, then start
#   make down           Stop and remove containers
#   make clean-docker   Stop containers, remove images, prune dangling layers
#   make migrate        Run pending DB migrations (against local Compose DB)
#   make migrate-down   Roll back all migrations
#   make migrate-status Show applied / pending migrations
#   make backfill       Run the one-shot historical backfill via Docker Compose
#   make logs           Tail all service logs
#   make clean          Remove local build artefacts

COVERAGE_THRESHOLD := 50
# Build the comma-separated -coverpkg argument from COVER_PKGS (the
# space-separated list immediately below). Avoids hand-maintaining two lists.
comma := ,
empty :=
space := $(empty) $(empty)
COVER_PKG := $(subst $(space),$(comma),$(COVER_PKGS))
# Same list, space-separated for the test targets.
COVER_PKGS := ./internal/config/... ./internal/indicators/... ./internal/api/... ./internal/llm/... ./internal/jobs/... ./internal/models/... ./internal/services/ranking/... ./internal/services/outcomes/... ./internal/services/regime/... ./internal/services/scoring/... ./internal/services/sector/...

.PHONY: dev dev-frontend build test test-cover test-verbose lint run rebuild down clean-docker migrate migrate-down migrate-status backfill logs clean \
        run-tradingview-imports run-market-inputs run-regime-classification run-regime-steps run-sector-scoring run-llm-evaluation \
        run-corporate-backfill run-commercial-report web-build web-dev \
        run-seed-runners run-premarket build-premarket \
        run-monthly-learning run-prompt-experiments run-weight-refit \
        run-followthrough build-followthrough \
        run-momentum build-momentum \
        run-preopen build-preopen \
        test-tv-collector test-premonition

# ── Go binaries ────────────────────────────────────────────────────────────────
BINARIES := api nightly backfill
BUILD_FLAGS := -trimpath -ldflags="-s -w"

dev:
	@echo "Starting Go API server (port 8080)..."
	@cd cmd/api && go run . &
	@echo "Starting Vite dev server (port 5173)..."
	@cd web && npx vite --host

build:
	@echo "→ Building all binaries…"
	@mkdir -p bin
	@for cmd in $(BINARIES); do \
		echo "  go build ./cmd/$$cmd → bin/$$cmd"; \
		CGO_ENABLED=0 go build $(BUILD_FLAGS) -o bin/$$cmd ./cmd/$$cmd; \
	done
	@echo "✓ Build complete"

# ── Tests ──────────────────────────────────────────────────────────────────────
test:
	go test -race -count=1 ./...

test-verbose:
	go test -race -count=1 -v ./...

test-cover:
	go test -race -coverprofile=coverage.out -covermode=atomic -coverpkg=$(COVER_PKG) $(COVER_PKGS)
	@go tool cover -func=coverage.out | tee cover-report.txt
	@t=$$(grep '^total:' cover-report.txt | awk '{print $$NF}' | tr -d '%'); \
	if awk -v t="$$t" -v th=$(COVERAGE_THRESHOLD) 'BEGIN{exit !(t+0 < th)}'; then \
	    echo "::error::Coverage $$t% below $(COVERAGE_THRESHOLD)% threshold"; exit 1; \
	else \
	    echo "✅ Coverage $$t% (threshold: $(COVERAGE_THRESHOLD)%)"; \
	fi
# ── Lint ───────────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

# ── Docker Compose ────────────────────────────────────────────────────────────
run: web-build
	docker compose up --build

# Force a full rebuild with no Docker layer cache, then start.
rebuild:
	docker compose build --no-cache --pull
	docker compose up

down:
	docker compose down

# Stop containers, remove their images, and prune dangling layers.
clean-docker:
	docker compose down --rmi local --remove-orphans
	docker image prune -f

logs:
	docker compose logs -f

# ── Migrations (requires postgres container to be running) ────────────────────
# DATABASE_URL is read from .env by godotenv inside the Go binary; for goose
# CLI we load it explicitly from the env file.
MIGRATE_DSN ?= $(shell grep '^DATABASE_URL=' .env 2>/dev/null | cut -d= -f2-)

migrate:
	@test -n "$(MIGRATE_DSN)" || (echo "ERROR: DATABASE_URL not found in .env" && exit 1)
	goose -dir internal/db/migrations postgres "$(MIGRATE_DSN)" up

migrate-down:
	@test -n "$(MIGRATE_DSN)" || (echo "ERROR: DATABASE_URL not found in .env" && exit 1)
	goose -dir internal/db/migrations postgres "$(MIGRATE_DSN)" down-to 0

migrate-status:
	@test -n "$(MIGRATE_DSN)" || (echo "ERROR: DATABASE_URL not found in .env" && exit 1)
	goose -dir internal/db/migrations postgres "$(MIGRATE_DSN)" status

# ── Backfill (one-shot, Docker profile) ───────────────────────────────────────
backfill:
	docker compose --profile backfill run --rm backfill

backfill-ticker:
	@test -n "$(TICKER)" || (echo "Usage: make backfill-ticker TICKER=AAPL [YEARS=3]" && exit 1)
	docker compose --profile backfill run --rm backfill -ticker $(TICKER) -years $(or $(YEARS),2)

# ── Standalone job runners ────────────────────────────────────────────────────
# DATE is optional: make run-market-inputs DATE=2026-04-13
# When omitted the runner defaults to yesterday.

run-tradingview-imports:
	go run ./cmd/jobs/run_tradingview_imports.go $(if $(DATE),--date=$(DATE),)

# Run Steps 4 + 5 back-to-back (inputs must succeed before classification).
run-regime-steps:
	go run ./cmd/jobs/run_market_inputs.go $(if $(DATE),--date=$(DATE),) && \
	go run ./cmd/jobs/run_market_regime_classification.go $(if $(DATE),--date=$(DATE),)

run-sector-scoring:
	go run ./cmd/jobs/run_sector_scoring.go $(if $(DATE),--date=$(DATE),)

run-llm-evaluation:
	go run ./cmd/jobs/run_llm_list_evaluation.go $(if $(DATE),--date=$(DATE),)

run-commercial-report:
	go run ./cmd/jobs/run_commercial_report.go $(if $(DATE),--date=$(DATE),)

run-seed-runners:
	go run ./cmd/jobs/run_historical_runners_seed.go

run-catalyst-backfill:
	go run ./cmd/jobs/run_catalyst_backfill.go

run-corporate-backfill:
	go run ./cmd/jobs/run_corporate_action_backfill.go --dry-run=false

run-enrichment:
	go run ./cmd/jobs/run_enrichment.go $(if $(DATE),--date=$(DATE),) $(if $(FORCE),--force,)
run-outcome-harvest:
	go run ./cmd/jobs/run_outcome_harvest.go

run-performance-monitor:
	go run ./cmd/jobs/run_performance_monitor.go

run-prompt-experiments:
	go run ./cmd/jobs/run_prompt_experiments.go

run-weight-refit:
	go run ./cmd/jobs/run_weight_refit.go

# ── TV Collector ──────────────────────────────────────────────
test-tv-collector:
	docker compose run --rm --no-deps tv-collector poetry run pytest

# ── Clean ─────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/