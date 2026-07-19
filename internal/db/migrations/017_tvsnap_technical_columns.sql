-- +goose Up
-- +goose StatementBegin
-- Add price structure, moving averages, and float data to
-- tradingview_snapshot_daily so the LLM prompt renderer can provide
-- complete data to the evaluation prompts.

-- ── Price structure (OHLC + event context) ──────────────────────────────────
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS open           NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS high           NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS low            NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS gap_pct        NUMERIC(10,4);   -- opening gap % vs prior close
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS change_pct     NUMERIC(10,4);   -- intraday % change

-- ── Moving averages (Stage 2 structure) ─────────────────────────────────────
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS sma_20         NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS sma_50         NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS sma_150        NUMERIC(14,4);
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS sma_200        NUMERIC(14,4);

-- ── Volatility ──────────────────────────────────────────────────────────────
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS atr_14         NUMERIC(14,4);   -- 14-day ATR

-- ── Share structure ─────────────────────────────────────────────────────────
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS float_shares   BIGINT;          -- float shares outstanding

-- ── Liquidity ───────────────────────────────────────────────────────────────
ALTER TABLE tradingview_snapshot_daily ADD COLUMN IF NOT EXISTS avg_volume_10d BIGINT;          -- 10-day average volume
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS open;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS high;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS low;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS gap_pct;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS change_pct;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS sma_20;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS sma_50;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS sma_150;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS sma_200;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS atr_14;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS float_shares;
ALTER TABLE tradingview_snapshot_daily DROP COLUMN IF EXISTS avg_volume_10d;
-- +goose StatementEnd

