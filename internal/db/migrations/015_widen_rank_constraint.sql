-- +goose Up
-- Widen the rank column constraint from 1–5 to 1–10 so that ranking engines
-- can output 10 candidates per list (the LLM layer will narrow to 5).
ALTER TABLE daily_rank_lists DROP CONSTRAINT IF EXISTS daily_rank_lists_rank_check;
ALTER TABLE daily_rank_lists ADD CONSTRAINT daily_rank_lists_rank_check CHECK (rank BETWEEN 1 AND 10);

-- +goose Down
ALTER TABLE daily_rank_lists DROP CONSTRAINT IF EXISTS daily_rank_lists_rank_check;
ALTER TABLE daily_rank_lists ADD CONSTRAINT daily_rank_lists_rank_check CHECK (rank BETWEEN 1 AND 5);

