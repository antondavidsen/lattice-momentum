-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tickers (
    ticker      TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL DEFAULT '',
    sector      TEXT        NOT NULL DEFAULT '',
    industry    TEXT        NOT NULL DEFAULT '',
    exchange    TEXT        NOT NULL DEFAULT '',
    country     TEXT        NOT NULL DEFAULT 'US',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tickers_sector   ON tickers (sector);
CREATE INDEX IF NOT EXISTS idx_tickers_industry ON tickers (industry);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tickers;
-- +goose StatementEnd

