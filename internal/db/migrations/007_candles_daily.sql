-- +goose Up
-- +goose StatementBegin
-- candles_daily: unified OHLCV time-series for equities, benchmark indices, and sector ETFs.
-- Supersedes the split daily_stocks / daily_indices / daily_sectors tables.
-- All symbols (including SPY, QQQ, XLK, …) must exist in tickers before insertion.
CREATE TABLE IF NOT EXISTS candles_daily (
    id              BIGSERIAL PRIMARY KEY,
    ticker          TEXT           NOT NULL REFERENCES tickers (ticker) ON DELETE CASCADE,
    date            DATE           NOT NULL,

    -- DOUBLE PRECISION (float8) — avoids pgx's math/big NUMERIC binary encoding
    -- which corrupts the Go heap under concurrent batch inserts.
    open            DOUBLE PRECISION NOT NULL,
    high            DOUBLE PRECISION NOT NULL,
    low             DOUBLE PRECISION NOT NULL,
    close           DOUBLE PRECISION NOT NULL,
    adjusted_close  DOUBLE PRECISION,         -- NULL when provider does not supply it

    volume          BIGINT         NOT NULL,

    provider        TEXT           NOT NULL,   -- "polygon" | "twelvedata"
    created_at      TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    UNIQUE (ticker, date)
);

-- Primary access pattern: latest N bars for a given ticker.
CREATE INDEX IF NOT EXISTS idx_candles_ticker_date
    ON candles_daily (ticker, date DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS candles_daily;
-- +goose StatementEnd

