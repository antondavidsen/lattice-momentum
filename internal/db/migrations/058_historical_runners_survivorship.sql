-- +goose Up
-- Migration 058: Survivorship bias flag for historical_runners
--
-- historical_runners was seeded from a curated CSV of big movers from
-- a specific bull period — this is a survivorship-biased starting universe.
-- This migration adds a boolean flag so consumers can filter or warn.

ALTER TABLE historical_runners
ADD COLUMN has_survivorship_flag BOOLEAN NOT NULL DEFAULT false;

UPDATE historical_runners
SET has_survivorship_flag = true
WHERE source = 'curated';

COMMENT ON COLUMN historical_runners.has_survivorship_flag IS 'True for rows seeded from survivorship-biased sources (e.g. curated CSV)';

-- +goose Down
ALTER TABLE historical_runners
DROP COLUMN IF EXISTS has_survivorship_flag;

UPDATE historical_runners
SET has_survivorship_flag = false
WHERE source = 'curated';

COMMENT ON COLUMN historical_runners.has_survivorship_flag IS NULL;