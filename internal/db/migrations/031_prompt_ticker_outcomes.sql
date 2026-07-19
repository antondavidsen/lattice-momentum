-- +goose Up
CREATE TABLE IF NOT EXISTS prompt_ticker_outcomes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date            DATE NOT NULL,
    list_type       TEXT NOT NULL,
    ticker          TEXT NOT NULL,
    prompt_version  TEXT NOT NULL,

    -- TRUE = LLM included it in final picks, FALSE = LLM received it but did NOT recommend it
    llm_recommended BOOLEAN NOT NULL DEFAULT TRUE,

    -- Parsed LLM recommendation (NULL when llm_recommended = false)
    recommended_setup       TEXT,
    recommended_entry_low   DOUBLE PRECISION,
    recommended_entry_high  DOUBLE PRECISION,
    recommended_stop        DOUBLE PRECISION,
    recommended_target_1    DOUBLE PRECISION,
    recommended_target_2    DOUBLE PRECISION,
    recommended_rr          DOUBLE PRECISION,
    recommended_size        TEXT,
    recommended_conviction  TEXT,

    -- Actual outcomes (COPIED from trade_outcomes_daily)
    actual_entry_price      DOUBLE PRECISION,
    actual_return_5d        DOUBLE PRECISION,
    actual_return_10d       DOUBLE PRECISION,
    actual_return_20d       DOUBLE PRECISION,
    actual_max_runup        DOUBLE PRECISION,
    actual_max_drawdown     DOUBLE PRECISION,

    -- Derived success metrics
    stop_hit               BOOLEAN,
    target_1_hit           BOOLEAN,
    target_2_hit           BOOLEAN,
    actual_rr_achieved     DOUBLE PRECISION,

    evaluated_days          INT NOT NULL DEFAULT 0,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (date, list_type, ticker)
);

CREATE INDEX idx_prompt_ticker_outcomes_version ON prompt_ticker_outcomes (prompt_version);
CREATE INDEX idx_prompt_ticker_outcomes_date ON prompt_ticker_outcomes (date);
CREATE INDEX idx_prompt_ticker_outcomes_recommended ON prompt_ticker_outcomes (llm_recommended);

-- +goose Down
DROP TABLE IF EXISTS prompt_ticker_outcomes;

