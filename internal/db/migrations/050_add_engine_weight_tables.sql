-- +goose Up
-- Weight tables for all three ranking engines.
-- Each stores learned logistic regression coefficients for the 7 component
-- scores used by the respective engine, plus regime_multiplier and
-- sector_multiplier as features 8 and 9.

CREATE TABLE ep_score_weights (
    id                      SERIAL PRIMARY KEY,
    version                 INT NOT NULL DEFAULT 0,
    active                  BOOLEAN NOT NULL DEFAULT FALSE,

    -- Feature weights (7 component scores + 2 multipliers = 9 features)
    event_quality_score     DOUBLE PRECISION NOT NULL,
    volume_spike_score      DOUBLE PRECISION NOT NULL,
    follow_through_score    DOUBLE PRECISION NOT NULL,
    trend_alignment_score   DOUBLE PRECISION NOT NULL,
    earnings_quality_score  DOUBLE PRECISION NOT NULL,
    options_flow_score      DOUBLE PRECISION NOT NULL,
    float_rotation_score    DOUBLE PRECISION NOT NULL,
    regime_multiplier       DOUBLE PRECISION NOT NULL,
    sector_multiplier       DOUBLE PRECISION NOT NULL,

    -- Metadata
    training_samples        INT NOT NULL,
    test_auc                DOUBLE PRECISION NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (version)
);

CREATE TABLE momentum_score_weights (
    id                          SERIAL PRIMARY KEY,
    version                     INT NOT NULL DEFAULT 0,
    active                      BOOLEAN NOT NULL DEFAULT FALSE,

    -- Feature weights (5 component scores + 2 multipliers = 7 features)
    breakout_strength           DOUBLE PRECISION NOT NULL,
    relative_strength           DOUBLE PRECISION NOT NULL,
    volume_expansion            DOUBLE PRECISION NOT NULL,
    volume_price_confirmation   DOUBLE PRECISION NOT NULL,
    trend_consistency           DOUBLE PRECISION NOT NULL,
    regime_multiplier           DOUBLE PRECISION NOT NULL,
    sector_multiplier           DOUBLE PRECISION NOT NULL,

    -- Metadata
    training_samples            INT NOT NULL,
    test_auc                    DOUBLE PRECISION NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (version)
);

CREATE TABLE leaders_score_weights (
    id                          SERIAL PRIMARY KEY,
    version                     INT NOT NULL DEFAULT 0,
    active                      BOOLEAN NOT NULL DEFAULT FALSE,

    -- Feature weights (4 component scores + 2 multipliers = 6 features)
    fundamentals_strength       DOUBLE PRECISION NOT NULL,
    eps_growth_stability        DOUBLE PRECISION NOT NULL,
    relative_strength_3m        DOUBLE PRECISION NOT NULL,
    trend_alignment             DOUBLE PRECISION NOT NULL,
    regime_multiplier           DOUBLE PRECISION NOT NULL,
    sector_multiplier           DOUBLE PRECISION NOT NULL,

    -- Metadata
    training_samples            INT NOT NULL,
    test_auc                    DOUBLE PRECISION NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (version)
);

-- Indexes for fast active-version lookup
CREATE INDEX idx_ep_weights_active ON ep_score_weights(active, version DESC);
CREATE INDEX idx_momentum_weights_active ON momentum_score_weights(active, version DESC);
CREATE INDEX idx_leaders_weights_active ON leaders_score_weights(active, version DESC);

-- ── Seed default weights (version=0, active=TRUE) ──────────────────────────────

-- EP Engine defaults (matching current hardcoded weights in ep_engine.go)
INSERT INTO ep_score_weights (
    version, active,
    event_quality_score, volume_spike_score, follow_through_score,
    trend_alignment_score, earnings_quality_score, options_flow_score,
    float_rotation_score, regime_multiplier, sector_multiplier,
    training_samples, test_auc
) VALUES (
    0, TRUE,
    0.30, 0.20, 0.15,
    0.15, 0.08, 0.07,
    0.05, 1.0, 1.0,
    0, 0.5
);

-- Momentum Engine defaults (matching current hardcoded weights in momentum_engine.go)
INSERT INTO momentum_score_weights (
    version, active,
    breakout_strength, relative_strength, volume_expansion,
    volume_price_confirmation, trend_consistency,
    regime_multiplier, sector_multiplier,
    training_samples, test_auc
) VALUES (
    0, TRUE,
    0.30, 0.25, 0.10,
    0.15, 0.20,
    1.0, 1.0,
    0, 0.5
);

-- Leaders Engine defaults (matching current hardcoded weights in leaders_engine.go)
INSERT INTO leaders_score_weights (
    version, active,
    fundamentals_strength, eps_growth_stability, relative_strength_3m,
    trend_alignment,
    regime_multiplier, sector_multiplier,
    training_samples, test_auc
) VALUES (
    0, TRUE,
    0.40, 0.25, 0.20,
    0.15,
    1.0, 1.0,
    0, 0.5
);

-- +goose Down
DROP TABLE IF EXISTS leaders_score_weights;
DROP TABLE IF EXISTS momentum_score_weights;
DROP TABLE IF EXISTS ep_score_weights;
