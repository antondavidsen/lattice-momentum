-- +goose Up
-- +goose StatementBegin
-- market_regime: one row per trading day — bull / bear / neutral / rotation.
-- Computed nightly from SPY + QQQ SMA relationships.
CREATE TABLE IF NOT EXISTS market_regime (
    date                  DATE           PRIMARY KEY,
    regime                TEXT           NOT NULL,    -- 'bull' | 'bear' | 'neutral' | 'rotation'
    spy_close             NUMERIC(14, 4),
    spy_sma50             NUMERIC(14, 4),
    spy_sma200            NUMERIC(14, 4),
    spy_above_sma50       BOOLEAN,
    spy_above_sma200      BOOLEAN,
    qqq_close             NUMERIC(14, 4),
    qqq_sma50             NUMERIC(14, 4),
    qqq_sma200            NUMERIC(14, 4),
    qqq_above_sma50       BOOLEAN,
    qqq_above_sma200      BOOLEAN,
    advance_decline_ratio NUMERIC(8, 4),
    notes                 TEXT,
    created_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS market_regime;
-- +goose StatementEnd

