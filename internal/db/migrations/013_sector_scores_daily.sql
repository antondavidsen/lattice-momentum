-- +goose Up
-- +goose StatementBegin

-- sector_scores_daily: daily sector momentum scoring output.
-- One row per (date, etf). Computed by the Sector Momentum Scoring job from
-- candles_daily data (sector ETFs + SPY benchmark).
CREATE TABLE IF NOT EXISTS sector_scores_daily (
    date              DATE             NOT NULL,
    etf               TEXT             NOT NULL,

    -- Raw performance metrics
    perf_1m           DOUBLE PRECISION NOT NULL,  -- 21-session return
    perf_3m           DOUBLE PRECISION NOT NULL,  -- 63-session return
    rs_vs_spy_3m      DOUBLE PRECISION NOT NULL,  -- perf_3m minus SPY perf_3m

    -- Trend position
    above_sma50       BOOLEAN          NOT NULL,
    above_sma200      BOOLEAN          NOT NULL,

    -- Computed scores
    trend_score       DOUBLE PRECISION NOT NULL,  -- 0.0 | 0.6 | 1.0
    score             DOUBLE PRECISION NOT NULL,  -- weighted composite [0,1]
    label             TEXT             NOT NULL    -- LEADING | STRONG | NEUTRAL | WEAK | LAGGING
                      CHECK (label IN ('LEADING','STRONG','NEUTRAL','WEAK','LAGGING')),

    created_at        TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

    PRIMARY KEY (date, etf)
);

CREATE INDEX IF NOT EXISTS idx_sector_scores_daily_date ON sector_scores_daily (date DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sector_scores_daily;
-- +goose StatementEnd

