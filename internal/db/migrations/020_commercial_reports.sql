-- +goose Up
CREATE TABLE commercial_reports (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_date           DATE        NOT NULL UNIQUE,
    regime                TEXT        NOT NULL DEFAULT 'neutral',
    headline              TEXT        NOT NULL DEFAULT '',
    market_summary        TEXT        NOT NULL DEFAULT '',
    sector_summary        TEXT        NOT NULL DEFAULT '',
    trade_cards_json      JSONB       NOT NULL DEFAULT '[]',
    risk_note             TEXT        NOT NULL DEFAULT '',
    closing_summary       TEXT        NOT NULL DEFAULT '',
    full_report_markdown  TEXT        NOT NULL DEFAULT '',
    performance_blurb     TEXT        NOT NULL DEFAULT '',
    source_list_types     TEXT[]      NOT NULL DEFAULT '{}',
    provider              TEXT        NOT NULL DEFAULT '',
    model                 TEXT        NOT NULL DEFAULT '',
    prompt_version        TEXT        NOT NULL DEFAULT 'v1',
    input_tokens          INT,
    output_tokens         INT,
    duration_ms           INT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_commercial_reports_date ON commercial_reports (report_date DESC);

-- +goose Down
DROP TABLE IF EXISTS commercial_reports;

