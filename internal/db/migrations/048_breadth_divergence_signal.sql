-- +goose Up
-- +goose StatementBegin
-- Migration: 048
-- Adds breadth_divergence_signal column to market_regime_daily for R-11.
-- Detects when SPY is up but NYSE A/D line is flat/falling (distribution warning).

ALTER TABLE market_regime_daily
    ADD COLUMN IF NOT EXISTS breadth_divergence_signal DOUBLE PRECISION;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE market_regime_daily
    DROP COLUMN IF EXISTS breadth_divergence_signal;
-- +goose StatementEnd
