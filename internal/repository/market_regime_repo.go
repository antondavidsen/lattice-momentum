package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// MarketRegimeRepo handles persistence for two related tables:
//
//   - market_regime_daily  — archives one classifier output row per trading session
//   - market_regime        — legacy singleton; always holds the latest classified row
//
// Both upserts are idempotent: re-running the nightly job for the same date
// simply overwrites the previous result with no manual cleanup required.
type MarketRegimeRepo struct {
	db dbPool // reuse the interface defined in market_inputs_repo.go
}

// NewMarketRegimeRepo constructs a MarketRegimeRepo backed by a live connection
// pool.
func NewMarketRegimeRepo(db *pgxpool.Pool) *MarketRegimeRepo {
	return &MarketRegimeRepo{db: db}
}

// ── market_regime_daily ───────────────────────────────────────────────────────

const upsertMarketRegimeDailySQL = `
	INSERT INTO market_regime_daily (date, regime, confidence, notes, raw_bull_strength, smoothed_bull_strength)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (date) DO UPDATE SET
		regime                = EXCLUDED.regime,
		confidence            = EXCLUDED.confidence,
		notes                 = EXCLUDED.notes,
		raw_bull_strength     = EXCLUDED.raw_bull_strength,
		smoothed_bull_strength = EXCLUDED.smoothed_bull_strength,
		updated_at            = NOW()`

// UpsertMarketRegimeDaily inserts or replaces the classifier output for the
// date in m.  On conflict the regime, confidence, bull-strength, and notes
// columns are overwritten; created_at is preserved.
func (r *MarketRegimeRepo) UpsertMarketRegimeDaily(ctx context.Context, m *models.MarketRegimeDaily) error {
	_, err := r.db.Exec(ctx, upsertMarketRegimeDailySQL,
		m.Date,
		m.Regime,
		m.Confidence,
		m.Notes,
		m.RawBullStrength,
		m.SmoothedBullStrength,
	)
	if err != nil {
		return fmt.Errorf("UpsertMarketRegimeDaily %s: %w", m.Date.Format("2006-01-02"), err)
	}
	return nil
}

const getLatestSmoothedSQL = `
	SELECT smoothed_bull_strength
	FROM   market_regime_daily
	ORDER  BY date DESC
	LIMIT  1`

// GetLatestSmoothedBullStrength returns the smoothed_bull_strength value from
// the most recent market_regime_daily row.
//
// Returns -1 (sentinel "no history") when:
//   - the table is empty (pgx.ErrNoRows), or
//   - the most-recent row has a NULL smoothed_bull_strength (written by the
//     v1 classifier before migration 020).
//
// The classification job uses -1 to detect a first-run condition and skips
// EMA smoothing, seeding the smoothed value with the raw bull strength.
func (r *MarketRegimeRepo) GetLatestSmoothedBullStrength(ctx context.Context) (float64, error) {
	row := r.db.QueryRow(ctx, getLatestSmoothedSQL)
	var v *float64
	if err := row.Scan(&v); err != nil {
		if isNoRows(err) {
			return -1, nil
		}
		return 0, fmt.Errorf("GetLatestSmoothedBullStrength: %w", err)
	}
	if v == nil {
		return -1, nil // pre-v2 row; treat as no history
	}
	return *v, nil
}

// ── breadth_divergence_signal (R-11) ──────────────────────────────────────────

// ErrNoMarketRegimeRow is returned when UpdateBreadthDivergence finds no row
// for the target date. The caller should log a warning and continue — this is
// not a pipeline-fatal error.
var ErrNoMarketRegimeRow = fmt.Errorf("market_regime_daily row does not exist for date")

const updateBreadthDivergenceSQL = `
	UPDATE market_regime_daily
	SET    breadth_divergence_signal = $1,
	       updated_at                = NOW()
	WHERE  date = $2`

// UpdateBreadthDivergence persists the breadth divergence signal for a given
// date. It expects the market_regime_daily row to already exist (created by the
// regime classification step). If the row is missing, it returns
// ErrNoMarketRegimeRow so the caller can log a warning without aborting the
// pipeline.
func (r *MarketRegimeRepo) UpdateBreadthDivergence(ctx context.Context, date time.Time, signal float64) error {
	tag, err := r.db.Exec(ctx, updateBreadthDivergenceSQL, signal, date)
	if err != nil {
		return fmt.Errorf("UpdateBreadthDivergence %s: %w", date.Format("2006-01-02"), err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoMarketRegimeRow
	}
	return nil
}

const selectBreadthDivergenceSQL = `
	SELECT breadth_divergence_signal
	FROM   market_regime_daily
	WHERE  date = $1`

// GetBreadthDivergence returns the breadth divergence signal for a given date.
// Returns 0.0 and nil error when no row exists or the value is NULL.
func (r *MarketRegimeRepo) GetBreadthDivergence(ctx context.Context, date time.Time) (float64, error) {
	row := r.db.QueryRow(ctx, selectBreadthDivergenceSQL, date)
	var v *float64
	if err := row.Scan(&v); err != nil {
		if isNoRows(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("GetBreadthDivergence %s: %w", date.Format("2006-01-02"), err)
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// ── market_regime (legacy singleton) ─────────────────────────────────────────

// GetMarketRegimeDaily returns the classified regime for a specific trading
// session.  Returns pgx.ErrNoRows (wrapped) when no classification exists for
// the given date.
func (r *MarketRegimeRepo) GetMarketRegimeDaily(ctx context.Context, date time.Time) (*models.MarketRegimeDaily, error) {
	row := r.db.QueryRow(ctx, `
		SELECT date, regime, confidence, notes,
		       raw_bull_strength, smoothed_bull_strength,
		       created_at, updated_at
		FROM   market_regime_daily
		WHERE  date = $1
	`, date)

	var m models.MarketRegimeDaily
	if err := row.Scan(
		&m.Date, &m.Regime, &m.Confidence, &m.Notes,
		&m.RawBullStrength, &m.SmoothedBullStrength,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetMarketRegimeDaily %s: %w", date.Format("2006-01-02"), err)
	}
	m.Regime = strings.Clone(m.Regime)
	m.Notes = cloneStringPtr(m.Notes)
	return &m, nil
}

// GetRegimeForDate returns the regime label for a specific date.
// Returns empty string and nil error if no row exists (not an error for analysis context).
func (r *MarketRegimeRepo) GetRegimeForDate(ctx context.Context, date time.Time) (string, error) {
	m, err := r.GetMarketRegimeDaily(ctx, date)
	if err != nil {
		if isNoRows(err) {
			return "", nil
		}
		return "", fmt.Errorf("GetRegimeForDate %s: %w", date.Format("2006-01-02"), err)
	}
	return m.Regime, nil
}

// GetLatestMarketRegimeDaily returns the most recent classified regime from
// market_regime_daily, regardless of date. Used as a fallback when the exact
// date lookup returns nothing (e.g. pre-market before yesterday's regime was
// classified).
func (r *MarketRegimeRepo) GetLatestMarketRegimeDaily(ctx context.Context) (*models.MarketRegimeDaily, error) {
	row := r.db.QueryRow(ctx, `
		SELECT date, regime, confidence, notes,
		       raw_bull_strength, smoothed_bull_strength,
		       created_at, updated_at
		FROM   market_regime_daily
		ORDER  BY date DESC
		LIMIT  1
	`)

	var m models.MarketRegimeDaily
	if err := row.Scan(
		&m.Date, &m.Regime, &m.Confidence, &m.Notes,
		&m.RawBullStrength, &m.SmoothedBullStrength,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetLatestMarketRegimeDaily: %w", err)
	}
	m.Regime = strings.Clone(m.Regime)
	m.Notes = cloneStringPtr(m.Notes)
	return &m, nil
}

const upsertMarketRegimeSQL = `
	INSERT INTO market_regime (
		date, regime,
		spy_above_sma50, spy_above_sma200,
		qqq_above_sma50, qqq_above_sma200,
		notes
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (date) DO UPDATE SET
		regime           = EXCLUDED.regime,
		spy_above_sma50  = EXCLUDED.spy_above_sma50,
		spy_above_sma200 = EXCLUDED.spy_above_sma200,
		qqq_above_sma50  = EXCLUDED.qqq_above_sma50,
		qqq_above_sma200 = EXCLUDED.qqq_above_sma200,
		notes            = EXCLUDED.notes`

// UpsertMarketRegime inserts or updates the legacy market_regime row for the
// date in m.  Trading-logic consumers read the most-recent row via:
//
//	SELECT * FROM market_regime ORDER BY date DESC LIMIT 1
//
// The raw price / SMA columns (spy_close, spy_sma50, spy_sma200, …) from
// migration 007 are intentionally left NULL.  That data lived in the old
// regime pipeline which has been superseded by the market_inputs_daily →
// market_regime_daily flow.  The boolean above-SMA flags are populated from
// market_inputs_daily so the table remains useful for basic queries.
func (r *MarketRegimeRepo) UpsertMarketRegime(ctx context.Context, m *models.MarketRegime) error {
	_, err := r.db.Exec(ctx, upsertMarketRegimeSQL,
		m.Date,
		m.Regime,
		m.SPYAboveSMA50,
		m.SPYAboveSMA200,
		m.QQQAboveSMA50,
		m.QQQAboveSMA200,
		m.Notes,
	)
	if err != nil {
		return fmt.Errorf("UpsertMarketRegime %s: %w", m.Date.Format("2006-01-02"), err)
	}
	return nil
}

// ── GetByDate (R-09 circuit breaker) ──────────────────────────────────────────

const getMarketRegimeByDateSQL = `
	SELECT date, regime, gate_level,
	       spy_close, spy_sma50, spy_sma200,
	       spy_above_sma50, spy_above_sma200,
	       qqq_close, qqq_sma50, qqq_sma200,
	       qqq_above_sma50, qqq_above_sma200,
	       advance_decline_ratio, notes,
	       created_at
	FROM   market_regime
	WHERE  date = $1`

// GetByDate returns the MarketRegime for the given date, including the
// gate_level field used by the R-09 circuit breaker.  Returns pgx.ErrNoRows
// (wrapped) when no row exists.
func (r *MarketRegimeRepo) GetByDate(ctx context.Context, date time.Time) (*models.MarketRegime, error) {
	row := r.db.QueryRow(ctx, getMarketRegimeByDateSQL, date)

	var m models.MarketRegime
	if err := row.Scan(
		&m.Date, &m.Regime, &m.GateLevel,
		&m.SPYClose, &m.SPYSMA50, &m.SPYSMA200,
		&m.SPYAboveSMA50, &m.SPYAboveSMA200,
		&m.QQQClose, &m.QQQSMA50, &m.QQQSMA200,
		&m.QQQAboveSMA50, &m.QQQAboveSMA200,
		&m.AdvanceDeclineRatio, &m.Notes,
		&m.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetByDate %s: %w", date.Format("2006-01-02"), err)
	}
	m.Notes = cloneStringPtr(m.Notes)
	return &m, nil
}
