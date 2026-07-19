-- +goose Up
CREATE TABLE llm_list_evaluations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date            DATE        NOT NULL,
    list_type       TEXT        NOT NULL CHECK (list_type IN ('ep', 'momentum', 'leaders')),
    provider        TEXT        NOT NULL,
    model           TEXT        NOT NULL,
    prompt_version  TEXT        NOT NULL DEFAULT 'v1',
    system_prompt   TEXT        NOT NULL,
    user_prompt     TEXT        NOT NULL,
    raw_response    TEXT        NOT NULL,
    parsed_json     JSONB       NOT NULL DEFAULT '{}',
    input_tickers   TEXT[]      NOT NULL,
    output_tickers  TEXT[]      NOT NULL DEFAULT '{}',
    input_tokens    INT,
    output_tokens   INT,
    duration_ms     INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (date, list_type)
);

CREATE INDEX idx_llm_list_evaluations_date ON llm_list_evaluations (date);
CREATE INDEX idx_llm_list_evaluations_date_type ON llm_list_evaluations (date, list_type);

-- +goose Down
DROP TABLE IF EXISTS llm_list_evaluations;

