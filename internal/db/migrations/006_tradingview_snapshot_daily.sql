-- +goose Up
-- +goose StatementBegin
-- tradingview_snapshot_daily: per-ticker per-screener daily snapshots with rich fundamentals.
-- One row per (ticker, snapshot_date, screener_source) — upserted on every run.

CREATE TABLE IF NOT EXISTS tradingview_snapshot_daily (
    id                 UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),

    ticker             TEXT        NOT NULL REFERENCES tickers(ticker) ON DELETE CASCADE,
    snapshot_date      DATE        NOT NULL,
    screener_source    TEXT        NOT NULL,   -- 'ep' | 'momentum' | 'market_leaders'

    -- ── PRICE / LIQUIDITY ─────────────────────────────────────────────────
    close              NUMERIC(14,4),
    volume             BIGINT,
    relative_volume    NUMERIC(10,4),
    price_x_volume_10d NUMERIC(18,2),          -- close × avg_volume_10d
    market_cap         NUMERIC(18,2),

    -- ── TECHNICAL SNAPSHOT ────────────────────────────────────────────────
    rsi_14             NUMERIC(10,4),
    perf_3m            NUMERIC(10,4),
    perf_6m            NUMERIC(10,4),
    distance_52w_high  NUMERIC(10,4),          -- (close / price_52w_high) × 100
    price_52w_high     NUMERIC(14,4),
    price_52w_low      NUMERIC(14,4),

    -- ── FUNDAMENTALS ──────────────────────────────────────────────────────
    eps_ttm            NUMERIC(14,4),
    eps_growth_yoy     NUMERIC(10,4),
    revenue_ttm        NUMERIC(18,2),
    revenue_growth_yoy NUMERIC(10,4),
    roe                NUMERIC(10,4),
    gross_margin       NUMERIC(10,4),
    net_margin         NUMERIC(10,4),
    operating_margin   NUMERIC(10,4),
    earnings_date      DATE,

    -- ── METADATA ──────────────────────────────────────────────────────────
    sector             TEXT,
    exchange           TEXT,

    raw_json           JSONB        NOT NULL,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    UNIQUE (ticker, snapshot_date, screener_source)
);

CREATE INDEX IF NOT EXISTS idx_tvsnap_ticker  ON tradingview_snapshot_daily (ticker);
CREATE INDEX IF NOT EXISTS idx_tvsnap_date    ON tradingview_snapshot_daily (snapshot_date DESC);
CREATE INDEX IF NOT EXISTS idx_tvsnap_source  ON tradingview_snapshot_daily (screener_source);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tradingview_snapshot_daily;
-- Restore prior table shell (data is not recoverable without a backup).
CREATE TABLE IF NOT EXISTS fundamentals_snapshot (
    id               UUID           PRIMARY KEY DEFAULT uuid_generate_v4(),
    ticker           TEXT           NOT NULL REFERENCES tickers(ticker) ON DELETE CASCADE,
    snapshot_date    DATE           NOT NULL,
    eps_growth       NUMERIC(10,4),
    revenue_growth   NUMERIC(10,4),
    roe              NUMERIC(10,4),
    gross_margin     NUMERIC(10,4),
    relative_volume  NUMERIC(10,4),
    earnings_date    DATE,
    raw_json         JSONB,
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE (ticker, snapshot_date)
);
-- +goose StatementEnd

