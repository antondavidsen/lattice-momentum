-- +goose Up
-- +goose StatementBegin
-- Migration: 043
-- Adds VIX, TICK, and breadth velocity fields for R-02 regime signal enrichment.

ALTER TABLE market_inputs_daily
    ADD COLUMN IF NOT EXISTS vix_level           DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS vix_roc_pct         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS tick_min_daily      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS breadth_velocity_5d DOUBLE PRECISION;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE market_inputs_daily
    DROP COLUMN IF EXISTS vix_level,
    DROP COLUMN IF EXISTS vix_roc_pct,
    DROP COLUMN IF EXISTS tick_min_daily,
    DROP COLUMN IF EXISTS breadth_velocity_5d;
-- +goose StatementEnd
