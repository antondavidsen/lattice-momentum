-- Migration 062: Trade Outcome Net Returns + Regime Segmentation (R06)
-- 
-- Adds columns for regime-segmented performance reporting, tiered slippage
-- cost model, and regime-conditioned sizing for the trade outcome system.
--
-- All new columns are nullable with sensible defaults to maintain backwards
-- compatibility with existing rows.

-- +goose Up
-- +goose StatementBegin

-- ── New columns on trade_outcomes_daily ──────────────────────────────────────

ALTER TABLE trade_outcomes_daily
  ADD COLUMN IF NOT EXISTS regime_label      TEXT    DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS slippage_tier     TEXT    DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS adv_cap_applied   BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS adv_cap_pct       NUMERIC DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS net_return_5d     NUMERIC DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS net_return_10d    NUMERIC DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS net_return_20d    NUMERIC DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS exit_type         TEXT    DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS stop_slippage_bps NUMERIC DEFAULT NULL;

-- Regime_bucket: a GENERATED ALWAYS AS column mapping regime_label to
-- 'risk_on' / 'risk_off' / 'unknown'. PostgreSQL 12+ supports this.
-- We use a CASE expression that covers the five regime labels.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'trade_outcomes_daily' AND column_name = 'regime_bucket'
  ) THEN
    ALTER TABLE trade_outcomes_daily
      ADD COLUMN regime_bucket TEXT GENERATED ALWAYS AS (
        CASE
          WHEN regime_label IN ('strong_bull', 'bull') THEN 'risk_on'
          WHEN regime_label IN ('neutral', 'correction', 'bear') THEN 'risk_off'
          ELSE NULL
        END
      ) STORED;
  END IF;
END $$;

-- ── Indexes for fast regime filtering ───────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_regime_label
  ON trade_outcomes_daily (regime_label);

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_regime_bucket
  ON trade_outcomes_daily (regime_bucket);

CREATE INDEX IF NOT EXISTS idx_trade_outcomes_slippage_tier
  ON trade_outcomes_daily (slippage_tier);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_trade_outcomes_regime_label;
DROP INDEX IF EXISTS idx_trade_outcomes_regime_bucket;
DROP INDEX IF EXISTS idx_trade_outcomes_slippage_tier;

ALTER TABLE trade_outcomes_daily
  DROP COLUMN IF EXISTS regime_bucket,
  DROP COLUMN IF EXISTS stop_slippage_bps,
  DROP COLUMN IF EXISTS exit_type,
  DROP COLUMN IF EXISTS net_return_20d,
  DROP COLUMN IF EXISTS net_return_10d,
  DROP COLUMN IF EXISTS net_return_5d,
  DROP COLUMN IF EXISTS adv_cap_pct,
  DROP COLUMN IF EXISTS adv_cap_applied,
  DROP COLUMN IF EXISTS slippage_tier,
  DROP COLUMN IF EXISTS regime_label;

-- +goose StatementEnd