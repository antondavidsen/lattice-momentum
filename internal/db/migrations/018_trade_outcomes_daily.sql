-- +goose Up
-- +goose StatementBegin

-- trade_outcomes_daily: forward performance tracking for rank-list signals.
-- One row per (entry_date, list_type, ticker). Computed by the Trade Outcome
-- Calculator job from candles_daily OHLC data over a 20-trading-day window.
CREATE TABLE IF NOT EXISTS trade_outcomes_daily (
    entry_date        DATE             NOT NULL,
    list_type         TEXT             NOT NULL,
    ticker            TEXT             NOT NULL,
    rank              INT              NOT NULL,

    -- Entry price is the close on entry_date (signal publication day).
    entry_price       DOUBLE PRECISION NOT NULL,

    -- Forward returns (NULL until the required trading days have elapsed).
    return_5d         DOUBLE PRECISION,   -- (close[T+5]  − entry) / entry
    return_10d        DOUBLE PRECISION,   -- (close[T+10] − entry) / entry
    return_20d        DOUBLE PRECISION,   -- (close[T+20] − entry) / entry

    -- Risk / opportunity metrics over the full 20-day window.
    max_runup_20d     DOUBLE PRECISION,   -- max(high[T+1..T+20] − entry) / entry
    max_drawdown_20d  DOUBLE PRECISION,   -- min(low[T+1..T+20]  − entry) / entry

    -- Housekeeping
    evaluated_days    INT              NOT NULL DEFAULT 0,  -- trading days available so far
    created_at        TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

    PRIMARY KEY (entry_date, list_type, ticker)
);

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_date
    ON trade_outcomes_daily (entry_date DESC);

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_date_list
    ON trade_outcomes_daily (entry_date, list_type);

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_daily_ticker
    ON trade_outcomes_daily (ticker);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trade_outcomes_daily;
-- +goose StatementEnd

