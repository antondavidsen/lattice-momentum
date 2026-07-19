-- +goose Up
CREATE TABLE historical_runners (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticker              TEXT NOT NULL,
    date                DATE NOT NULL,

    -- Catalyst
    catalyst_category   TEXT NOT NULL DEFAULT 'unknown',
    catalyst_headline   TEXT,
    catalyst_score      INT NOT NULL DEFAULT 0,
    catalyst_confidence DOUBLE PRECISION,

    -- Price action (signal day)
    prev_close          DOUBLE PRECISION NOT NULL,
    open_price          DOUBLE PRECISION NOT NULL,
    high_price          DOUBLE PRECISION NOT NULL,
    low_price           DOUBLE PRECISION NOT NULL,
    close_price         DOUBLE PRECISION NOT NULL,
    volume              BIGINT NOT NULL,
    avg_volume_20d      BIGINT NOT NULL,

    -- Computed signal-day metrics
    gap_pct             DOUBLE PRECISION NOT NULL,
    rel_volume          DOUBLE PRECISION NOT NULL,
    intraday_return     DOUBLE PRECISION NOT NULL,
    intraday_range_pct  DOUBLE PRECISION NOT NULL,
    max_intraday_runup  DOUBLE PRECISION NOT NULL,
    close_vs_range      DOUBLE PRECISION,

    -- Supply dynamics
    float_shares        BIGINT,
    market_cap          DOUBLE PRECISION,
    sector              TEXT,

    -- Forward performance
    day2_open           DOUBLE PRECISION,
    day2_close          DOUBLE PRECISION,
    day2_return         DOUBLE PRECISION,
    held_gains_d2       BOOLEAN,
    day5_return         DOUBLE PRECISION,

    -- Metadata
    source              TEXT NOT NULL DEFAULT 'candle_scan',
    feature_vector      vector(6),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (ticker, date)
);

CREATE INDEX idx_historical_runners_date ON historical_runners (date DESC);
CREATE INDEX idx_historical_runners_catalyst ON historical_runners (catalyst_category);
CREATE INDEX idx_historical_runners_source ON historical_runners (source);
CREATE INDEX idx_historical_runners_feature_vector ON historical_runners
    USING ivfflat (feature_vector vector_cosine_ops) WITH (lists = 10);

-- +goose Down
DROP TABLE IF EXISTS historical_runners;

