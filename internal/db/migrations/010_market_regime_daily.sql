-- +goose Up
-- +goose StatementBegin

-- ── market_inputs_daily amendments ──────────────────────────────────────────

-- Add updated_at so monitoring tooling and the classifier can distinguish a
-- freshly computed row from a stale recomputed one.
-- created_at is intentionally left immutable after this migration.
ALTER TABLE market_inputs_daily
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Drop the redundant explicit index on the primary key column.
-- PostgreSQL automatically creates a B-tree index on every PRIMARY KEY;
-- the explicit (date DESC) index is dead weight.
DROP INDEX IF EXISTS idx_market_inputs_daily_date;

-- ── market_regime_daily ──────────────────────────────────────────────────────
-- Stores the nightly classifier output — one row per trading session.
-- Keyed on date (PRIMARY KEY) so re-running the classifier for the same date
-- simply overwrites the previous result (idempotent via UPSERT).
CREATE TABLE IF NOT EXISTS market_regime_daily (
    date        DATE        PRIMARY KEY,

    -- Classifier output label.
    -- CHECK constraint mirrors the RegimeLabel constants in
    -- internal/services/regime/labels.go — both must stay in sync.
    regime      TEXT        NOT NULL
                CHECK (regime IN ('strong_bull', 'bull', 'neutral', 'correction', 'bear')),

    -- Optional 0–100 confidence score assigned by the classifier.
    confidence  NUMERIC(5,2),

    -- Human-readable notes for manual overrides or post-hoc debugging.
    notes       TEXT,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS market_regime_daily;
ALTER TABLE market_inputs_daily DROP COLUMN IF EXISTS updated_at;
-- The redundant index is intentionally not recreated on rollback.
-- +goose StatementEnd

