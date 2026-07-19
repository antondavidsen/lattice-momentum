-- +goose Up
-- +goose StatementBegin
-- Add market-index OHLC columns (e.g. SPY/SPX for the day) to market_snapshots.
-- DEFAULT 0 keeps existing rows valid without a backfill.
ALTER TABLE market_snapshots
    ADD COLUMN IF NOT EXISTS open DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS high DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS low  DOUBLE PRECISION NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE market_snapshots
    DROP COLUMN IF EXISTS open,
    DROP COLUMN IF EXISTS high,
    DROP COLUMN IF EXISTS low;
-- +goose StatementEnd

