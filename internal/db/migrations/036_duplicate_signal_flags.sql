-- +goose Up
-- +goose StatementBegin
ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS is_duplicate_signal     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS trading_days_since_prior INT     NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_duplicate
    ON trade_outcomes_daily (ticker, entry_date)
    WHERE is_duplicate_signal = TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_trade_outcomes_daily_duplicate;
ALTER TABLE trade_outcomes_daily
    DROP COLUMN IF EXISTS trading_days_since_prior,
    DROP COLUMN IF EXISTS is_duplicate_signal;
-- +goose StatementEnd

