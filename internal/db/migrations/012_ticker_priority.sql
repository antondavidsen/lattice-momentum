-- 021_ticker_priority.sql
-- Adds a priority column to the tickers table so the nightly candle ingestion
-- worker pool can drain CRITICAL tickers before processing the normal universe.
--
-- CRITICAL = benchmark indices (SPY, QQQ, IWM, DIA) + all sector ETFs.
-- These tickers must have fresh candles before the regime engine can run.
--
-- All existing rows are seeded with 'NORMAL'; the UPDATE below promotes the
-- known critical tickers.  New tickers inserted via tv-collector default to
-- 'NORMAL'; the IngestionService.EnsureExists call sets 'CRITICAL' explicitly
-- for any benchmark / ETF that is first seen at ingest time.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE ticker_priority AS ENUM ('CRITICAL', 'NORMAL');

ALTER TABLE tickers
    ADD COLUMN priority ticker_priority NOT NULL DEFAULT 'NORMAL';

-- Promote known critical tickers (benchmarks + sector ETFs).
UPDATE tickers
SET priority = 'CRITICAL'
WHERE ticker IN (
    'SPY', 'QQQ', 'IWM', 'DIA',
    'XLK', 'XLV', 'XLF', 'XLY', 'XLI', 'XLE', 'XLB', 'XLU', 'XLP', 'XLRE',
    'SMH', 'IGV'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tickers DROP COLUMN IF EXISTS priority;
DROP TYPE IF EXISTS ticker_priority;
-- +goose StatementEnd

