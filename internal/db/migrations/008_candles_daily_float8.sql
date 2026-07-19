-- +goose Up
-- +goose StatementBegin
-- Change OHLCV price columns from NUMERIC(14,4) to DOUBLE PRECISION (float8).
--
-- WHY: pgx v5 encodes float64 → NUMERIC via math/big.Int (binary NUMERIC format).
-- Under concurrent batch inserts this triggers heap corruption in the Go runtime
-- (fatal: s.allocCount != s.nelems) because the GC cannot keep up with the rapid
-- allocation/deallocation of math/big.nat backing slices across goroutines.
--
-- DOUBLE PRECISION uses a direct 8-byte IEEE 754 wire encoding — no math/big
-- involved. For OHLCV data float8 gives 15–17 significant decimal digits, which
-- is more than sufficient.
ALTER TABLE candles_daily
    ALTER COLUMN open           TYPE DOUBLE PRECISION,
    ALTER COLUMN high           TYPE DOUBLE PRECISION,
    ALTER COLUMN low            TYPE DOUBLE PRECISION,
    ALTER COLUMN close          TYPE DOUBLE PRECISION,
    ALTER COLUMN adjusted_close TYPE DOUBLE PRECISION;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE candles_daily
    ALTER COLUMN open           TYPE NUMERIC(14,4),
    ALTER COLUMN high           TYPE NUMERIC(14,4),
    ALTER COLUMN low            TYPE NUMERIC(14,4),
    ALTER COLUMN close          TYPE NUMERIC(14,4),
    ALTER COLUMN adjusted_close TYPE NUMERIC(14,4);
-- +goose StatementEnd

