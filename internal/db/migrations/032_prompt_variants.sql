-- +goose Up
CREATE TABLE IF NOT EXISTS prompt_variants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    list_type       TEXT NOT NULL,
    variant_name    TEXT NOT NULL,
    is_primary      BOOLEAN NOT NULL DEFAULT FALSE,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    system_prompt   TEXT NOT NULL,
    user_template   TEXT NOT NULL,
    prompt_version  TEXT NOT NULL,
    consecutive_outperform_weeks INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes           TEXT,

    UNIQUE (list_type, variant_name)
);

-- Shadow evaluations stored alongside primary in llm_list_evaluations
-- with a different prompt_version. Add variant_name to distinguish:
ALTER TABLE llm_list_evaluations ADD COLUMN IF NOT EXISTS variant_name TEXT NOT NULL DEFAULT 'primary';

-- Update the unique constraint to include variant_name so shadow + primary can coexist.
-- Drop the old constraint first (it was on date, list_type).
ALTER TABLE llm_list_evaluations DROP CONSTRAINT IF EXISTS llm_list_evaluations_date_list_type_key;
ALTER TABLE llm_list_evaluations ADD CONSTRAINT llm_list_evaluations_date_list_type_variant_key UNIQUE (date, list_type, variant_name);

-- Extend prompt_experiment_results for holdout tracking.
ALTER TABLE prompt_experiment_results ADD COLUMN IF NOT EXISTS is_holdout BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE prompt_experiment_results ADD COLUMN IF NOT EXISTS dataset_label TEXT NOT NULL DEFAULT 'full';

-- +goose Down
ALTER TABLE prompt_experiment_results DROP COLUMN IF EXISTS dataset_label;
ALTER TABLE prompt_experiment_results DROP COLUMN IF EXISTS is_holdout;

ALTER TABLE llm_list_evaluations DROP CONSTRAINT IF EXISTS llm_list_evaluations_date_list_type_variant_key;
ALTER TABLE llm_list_evaluations ADD CONSTRAINT llm_list_evaluations_date_list_type_key UNIQUE (date, list_type);
ALTER TABLE llm_list_evaluations DROP COLUMN IF EXISTS variant_name;

DROP TABLE IF EXISTS prompt_variants;

