-- +goose Up
-- +goose StatementBegin

-- ── STORY-R04: Deduplication columns for trade_outcomes_daily ────────────────

-- is_unremarkable flag for historical_runners (middle-ground seeding)
ALTER TABLE historical_runners
    ADD COLUMN IF NOT EXISTS is_unremarkable BOOLEAN NOT NULL DEFAULT false;

-- is_primary_observation: at most one row per (ticker, list_type) within any
-- rolling 20-trading-day window can have this = true.  Re-listings within the
-- window are kept for audit but flagged false.
ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS is_primary_observation BOOLEAN NOT NULL DEFAULT false;

-- cross_list_duplicate: when the same ticker appears on multiple lists for the
-- same entry date, only the highest-conviction list (leaders > momentum > ep)
-- gets is_primary_observation = true.  Other rows are flagged true here.
ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS cross_list_duplicate BOOLEAN NOT NULL DEFAULT false;

-- cluster_id: groups rows whose 20d outcome windows overlap by ≥5 trading days.
-- Used for cluster-robust standard errors (R05).
ALTER TABLE trade_outcomes_daily
    ADD COLUMN IF NOT EXISTS cluster_id BIGINT;

-- Index to support the 20d-window lookup for is_primary_observation.
CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_primary
    ON trade_outcomes_daily (ticker, list_type, entry_date)
    WHERE is_primary_observation = true;

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_cluster
    ON trade_outcomes_daily (cluster_id)
    WHERE cluster_id IS NOT NULL;

-- ── universe_snapshot_daily: point-in-time evaluable universe ─────────────────
CREATE TABLE IF NOT EXISTS universe_snapshot_daily (
    date       DATE             NOT NULL,
    ticker     VARCHAR(10)      NOT NULL,
    is_tradeable BOOLEAN        NOT NULL,
    exchange   VARCHAR(10),
    price_at_close DECIMAL(10,2),
    PRIMARY KEY (date, ticker)
);

CREATE INDEX IF NOT EXISTS idx_universe_snapshot_daily_date
    ON universe_snapshot_daily (date DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_trade_outcomes_daily_cluster;
DROP INDEX IF EXISTS idx_trade_outcomes_daily_primary;
ALTER TABLE trade_outcomes_daily DROP COLUMN IF EXISTS cluster_id;
ALTER TABLE trade_outcomes_daily DROP COLUMN IF EXISTS cross_list_duplicate;
ALTER TABLE trade_outcomes_daily DROP COLUMN IF EXISTS is_primary_observation;
DROP TABLE IF EXISTS universe_snapshot_daily;
ALTER TABLE historical_runners DROP COLUMN IF EXISTS is_unremarkable;

-- +goose StatementEnd