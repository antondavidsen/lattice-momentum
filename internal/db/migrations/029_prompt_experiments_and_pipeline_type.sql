-- +goose Up
CREATE TABLE prompt_experiment_results (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_version              TEXT NOT NULL,
    pipeline_type               TEXT NOT NULL,
    evaluation_date             DATE NOT NULL,
    total_picks                 INT NOT NULL,
    avg_intraday_return         DOUBLE PRECISION,
    median_intraday_return      DOUBLE PRECISION,
    win_rate                    DOUBLE PRECISION,
    avg_llm_confidence          DOUBLE PRECISION,
    confidence_calibration      DOUBLE PRECISION,
    trade_start_date            DATE NOT NULL,
    trade_end_date              DATE NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (prompt_version, pipeline_type, evaluation_date)
);

-- Add pipeline_type to nightly_runs for multi-pipeline support.
ALTER TABLE nightly_runs ADD COLUMN IF NOT EXISTS pipeline_type TEXT NOT NULL DEFAULT 'nightly';

-- +goose Down
ALTER TABLE nightly_runs DROP COLUMN IF EXISTS pipeline_type;
DROP TABLE IF EXISTS prompt_experiment_results;

