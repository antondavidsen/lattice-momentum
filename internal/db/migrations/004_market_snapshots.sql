-- +goose Up
-- +goose StatementBegin
-- market_snapshots: raw daily screener payloads received from tv-collector.
-- One row per calendar date. Re-posting the same date performs an upsert.
CREATE TABLE IF NOT EXISTS market_snapshots (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    snapshot_date   DATE        NOT NULL UNIQUE,
    momentum        JSONB,          -- []ScreenerRow from momentum screener
    episodic_pivots JSONB,          -- []ScreenerRow from episodic-pivot screener
    market_leaders  JSONB,          -- []ScreenerRow from market-leaders screener
    row_counts      JSONB,          -- {"momentum":N,"episodic_pivots":N,"market_leaders":N}
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_market_snapshots_date ON market_snapshots (snapshot_date DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS market_snapshots;
-- +goose StatementEnd

