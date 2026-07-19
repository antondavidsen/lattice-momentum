-- +goose Up
-- Migration 064: Add corporate_action_count to trade_outcomes_daily and
-- trade_outcomes_quarantine. The Go model (TradeOutcomeDaily.CorporateActionCount)
-- and repo queries already reference this column, but no migration ever added it
-- to either table. Default 0 avoids NOT NULL violations on existing rows.

ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS corporate_action_count INT NOT NULL DEFAULT 0;

ALTER TABLE trade_outcomes_quarantine
    ADD COLUMN IF NOT EXISTS corporate_action_count INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE trade_outcomes_daily
    DROP COLUMN IF EXISTS corporate_action_count;

ALTER TABLE trade_outcomes_quarantine
    DROP COLUMN IF EXISTS corporate_action_count;