-- +goose Up
-- Migration 057: Corporate Actions + Trade Outcomes Quarantine
--
-- corporate_actions stores split/reverse-split events fetched from the
-- Polygon Reference API (/v3/reference/splits). Used to adjust return
-- calculations in trade_outcome_service.go.
--
-- trade_outcomes_quarantine mirrors trade_outcomes_daily columns plus
-- quarantine metadata. It acts as a backstop for implausible returns
-- that slip through corporate-action adjustment.

-- Corporate actions calendar ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS corporate_actions (
    id             BIGSERIAL PRIMARY KEY,
    ticker         TEXT NOT NULL,
    ex_date        DATE NOT NULL,
    action_type    TEXT NOT NULL,               -- 'split', 'reverse_split', 'dividend', 'spinoff'
    ratio          DOUBLE PRECISION,            -- e.g., 4.0 for 4:1 split
    dividend_amt   DOUBLE PRECISION,
    source         TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (ticker, ex_date, action_type)
);

CREATE INDEX IF NOT EXISTS dx_corporate_actions_ticker_date ON corporate_actions (ticker, ex_date);

COMMENT ON TABLE corporate_actions IS 'Corporate actions (splits, dividends, spin-offs) from Polygon reference API';
COMMENT ON COLUMN corporate_actions.action_type IS 'split, reverse_split, dividend, spinoff';
COMMENT ON COLUMN corporate_actions.ratio IS 'split ratio: 4.0 for 4:1 split, 0.1 for 1:10 reverse split';
COMMENT ON COLUMN corporate_actions.source IS 'provider name, e.g. polygon';

-- ── Trade outcomes quarantine (backstop) ────────────────────────────────────
CREATE TABLE IF NOT EXISTS trade_outcomes_quarantine (
    entry_date       DATE NOT NULL,
    list_type        TEXT NOT NULL,
    ticker           TEXT NOT NULL,
    rank             INT NOT NULL,
    entry_price      DOUBLE PRECISION NOT NULL,
    return_5d        DOUBLE PRECISION,
    return_10d       DOUBLE PRECISION,
    return_20d       DOUBLE PRECISION,
    return_1d        DOUBLE PRECISION,
    return_2d        DOUBLE PRECISION,
    return_3d        DOUBLE PRECISION,
    return_4d        DOUBLE PRECISION,
    max_runup_20d    DOUBLE PRECISION,
    max_drawdown_20d DOUBLE PRECISION,
    evaluated_days   INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    quarantine_reason TEXT NOT NULL,
    detected_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at      TIMESTAMPTZ,
    resolution       TEXT,
    CHECK (resolution IN ('confirmed_genuine', 'corporate_action_adjusted', 'deleted_bad_data', NULL))
);

CREATE INDEX IF NOT EXISTS idx_quarantine_ticker_date ON trade_outcomes_quarantine (ticker, entry_date);
CREATE INDEX IF NOT EXISTS idx_quarantine_detected ON trade_outcomes_quarantine (detected_at);

COMMENT ON TABLE trade_outcomes_quarantine IS 'Implausible outcome rows flagged by plausibility backstop filter';
COMMENT ON COLUMN trade_outcomes_quarantine.quarantine_reason IS 'one_day_return_exceeded, twenty_day_return_high, twenty_day_return_low';
COMMENT ON COLUMN trade_outcomes_quarantine.resolution IS 'confirmed_genuine, corporate_action_adjusted, deleted_bad_data';

-- +goose Down
DROP TABLE IF EXISTS trade_outcomes_quarantine;
DROP TABLE IF EXISTS corporate_actions;
