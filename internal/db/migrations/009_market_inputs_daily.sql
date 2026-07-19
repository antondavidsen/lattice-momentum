-- +goose Up
-- +goose StatementBegin
-- market_inputs_daily: one row per trading session holding every pre-computed
-- signal consumed by the Market Regime Engine.
-- The table is keyed on date (PRIMARY KEY) so that the nightly job is fully
-- idempotent — re-running it for the same date simply overwrites the row.
CREATE TABLE IF NOT EXISTS market_inputs_daily (
    date                DATE PRIMARY KEY,

    -- Benchmark index position relative to moving averages.
    spy_above_50        BOOLEAN       NOT NULL,
    spy_above_200       BOOLEAN       NOT NULL,
    qqq_above_50        BOOLEAN       NOT NULL,
    qqq_above_200       BOOLEAN       NOT NULL,

    -- IBD-style distribution-day count for SPY (20-session rolling window).
    distribution_days   INT           NOT NULL,

    -- Market breadth: percentage of tracked stocks above their SMA (0–100).
    breadth_above_50    NUMERIC(5,2)  NOT NULL,
    breadth_above_200   NUMERIC(5,2)  NOT NULL,

    -- Latest-session relative-strength ratios (dimensionless price ratio).
    qqq_vs_spy_rs       NUMERIC(10,4) NOT NULL,
    iwm_vs_spy_rs       NUMERIC(10,4) NOT NULL,

    created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_market_inputs_daily_date
    ON market_inputs_daily (date DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS market_inputs_daily;
-- +goose StatementEnd

