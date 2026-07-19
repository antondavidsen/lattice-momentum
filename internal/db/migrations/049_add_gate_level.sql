-- +goose Up
-- +goose StatementBegin
-- Migration: 049
-- Adds gate_level column to nightly_runs for R-09 regime-aware circuit breaker.
-- Records which gate level was applied on each pipeline run:
--   'full'    — all three ranking lists executed
--   'ep_only' — only catalyst-driven (EP) list executed
--   'halt'    — no ranking lists executed; report says "sit out"

ALTER TABLE nightly_runs
    ADD COLUMN IF NOT EXISTS gate_level TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE nightly_runs
    DROP COLUMN IF EXISTS gate_level;
-- +goose StatementEnd
