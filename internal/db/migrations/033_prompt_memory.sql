-- +goose Up
CREATE TABLE IF NOT EXISTS prompt_memory (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date            DATE NOT NULL,
    list_type       TEXT NOT NULL,
    ticker          TEXT NOT NULL,
    prompt_version  TEXT NOT NULL,

    -- Context for similarity search
    context_summary TEXT NOT NULL,
    embedding       vector(1536),

    -- What the LLM recommended
    llm_setup       TEXT,
    llm_conviction  TEXT,
    llm_entry       TEXT,
    llm_stop        TEXT,

    -- After T+5
    outcome_status     TEXT NOT NULL DEFAULT 'pending',
    outcome_return_5d  DOUBLE PRECISION,
    outcome_stop_hit   BOOLEAN,
    outcome_target_hit BOOLEAN,
    outcome_summary    TEXT,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (date, list_type, ticker)
);

CREATE INDEX idx_prompt_memory_embedding ON prompt_memory USING ivfflat (embedding vector_cosine_ops) WITH (lists = 10);
CREATE INDEX idx_prompt_memory_status ON prompt_memory (outcome_status);
CREATE INDEX idx_prompt_memory_list_type ON prompt_memory (list_type);

-- +goose Down
DROP TABLE IF EXISTS prompt_memory;

