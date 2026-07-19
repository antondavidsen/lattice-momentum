-- +goose Up
CREATE TABLE daily_rank_lists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date        DATE        NOT NULL,
    list_type   TEXT        NOT NULL CHECK (list_type IN ('ep', 'momentum', 'leaders')),
    rank        INT         NOT NULL CHECK (rank BETWEEN 1 AND 5),
    ticker      TEXT        NOT NULL,
    score       DOUBLE PRECISION NOT NULL,
    reason      JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (date, list_type, ticker)
);

CREATE INDEX idx_daily_rank_lists_date ON daily_rank_lists (date);
CREATE INDEX idx_daily_rank_lists_date_type ON daily_rank_lists (date, list_type);

-- +goose Down
DROP TABLE IF EXISTS daily_rank_lists;

