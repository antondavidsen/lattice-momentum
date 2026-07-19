-- +goose Up
-- +goose StatementBegin

-- ── market_inputs_daily: v2 continuous SMA-distance + drawdown signals ─────────
--
-- The binary spy_above_50 / spy_above_200 booleans are kept for backward
-- compatibility but the classifier now uses the continuous percentage-distance
-- fields as the primary scoring input.  DEFAULT 0 ensures rows inserted before
-- this migration (if any exist) don't break scans into non-nullable Go fields.

ALTER TABLE market_inputs_daily
    ADD COLUMN IF NOT EXISTS spy_pct_from_sma50     NUMERIC(8,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS spy_pct_from_sma200    NUMERIC(8,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS qqq_pct_from_sma50     NUMERIC(8,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS qqq_pct_from_sma200    NUMERIC(8,4) NOT NULL DEFAULT 0,

    -- TRUE when SMA-50 is above SMA-200 (golden cross); FALSE = death cross.
    ADD COLUMN IF NOT EXISTS spy_sma50_above_sma200 BOOLEAN      NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS qqq_sma50_above_sma200 BOOLEAN      NOT NULL DEFAULT false,

    -- Trailing drawdown from the 252-session (≈ 1 trading year) high.
    -- Stored as a negative percentage: −15.0 means 15 % below the 52-week high.
    ADD COLUMN IF NOT EXISTS spy_drawdown_pct       NUMERIC(8,4) NOT NULL DEFAULT 0;

-- ── market_regime_daily: raw + EMA-smoothed bull-strength columns ──────────────
--
-- raw_bull_strength      — today's unsmoothed classifier score normalised 0–1.
-- smoothed_bull_strength — α = 0.5 EMA applied over raw scores; used for the
--                          actual regime label to suppress single-session noise.
-- Both are nullable so pre-v2 rows (written by the v1 classifier) remain valid.

ALTER TABLE market_regime_daily
    ADD COLUMN IF NOT EXISTS raw_bull_strength      NUMERIC(6,4),
    ADD COLUMN IF NOT EXISTS smoothed_bull_strength NUMERIC(6,4);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE market_inputs_daily
    DROP COLUMN IF EXISTS spy_pct_from_sma50,
    DROP COLUMN IF EXISTS spy_pct_from_sma200,
    DROP COLUMN IF EXISTS qqq_pct_from_sma50,
    DROP COLUMN IF EXISTS qqq_pct_from_sma200,
    DROP COLUMN IF EXISTS spy_sma50_above_sma200,
    DROP COLUMN IF EXISTS qqq_sma50_above_sma200,
    DROP COLUMN IF EXISTS spy_drawdown_pct;

ALTER TABLE market_regime_daily
    DROP COLUMN IF EXISTS raw_bull_strength,
    DROP COLUMN IF EXISTS smoothed_bull_strength;

-- +goose StatementEnd

