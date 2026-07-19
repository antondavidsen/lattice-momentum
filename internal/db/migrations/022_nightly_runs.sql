-- +goose Up
CREATE TABLE nightly_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_date        DATE        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'running',  -- running | completed | failed
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    duration_ms     BIGINT,
    steps_total     INT         NOT NULL DEFAULT 0,
    steps_completed INT         NOT NULL DEFAULT 0,
    failed_step     TEXT,
    error_message   TEXT,
    step_durations  JSONB       NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_nightly_runs_date ON nightly_runs (run_date DESC);

-- +goose Down
DROP TABLE IF EXISTS nightly_runs;

