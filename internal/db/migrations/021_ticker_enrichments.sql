-- +goose Up
CREATE TABLE ticker_enrichments (
    ticker           TEXT             NOT NULL REFERENCES tickers(ticker),
    enrichment_date  DATE             NOT NULL,

    -- Company profile
    company_name     TEXT             NOT NULL DEFAULT '',
    description      TEXT             NOT NULL DEFAULT '',
    market_cap_usd   DOUBLE PRECISION NOT NULL DEFAULT 0,
    industry         TEXT             NOT NULL DEFAULT '',
    sector           TEXT             NOT NULL DEFAULT '',
    country          TEXT             NOT NULL DEFAULT 'US',

    -- Price context
    current_price    DOUBLE PRECISION NOT NULL DEFAULT 0,
    perf_30d         DOUBLE PRECISION,
    perf_90d         DOUBLE PRECISION,
    rs_vs_spy        DOUBLE PRECISION,

    -- News summary (0–3 bullet points as JSONB array)
    news_summary_json JSONB           NOT NULL DEFAULT '[]'::jsonb,

    -- Cache management
    cache_ttl_hours  INT              NOT NULL DEFAULT 24,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

    PRIMARY KEY (ticker, enrichment_date)
);

CREATE INDEX idx_ticker_enrichments_date ON ticker_enrichments (enrichment_date);

-- +goose Down
DROP TABLE IF EXISTS ticker_enrichments;

