
-- +goose Up
-- +goose StatementBegin
-- Add daily return granularity columns (days 1-4) to trade_outcomes_daily.
-- These complement the existing 5d/10d/20d returns for finer early tracking.
ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS return_1d DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS return_2d DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS return_3d DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS return_4d DOUBLE PRECISION;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trade_outcomes_daily
DROP COLUMN IF EXISTS return_1d,
  DROP COLUMN IF EXISTS return_2d,
  DROP COLUMN IF EXISTS return_3d,
  DROP COLUMN IF EXISTS return_4d;
-- +goose StatementEnd