package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// TradeOutcomeRepo handles persistence for the trade_outcomes_daily table.
type TradeOutcomeRepo struct {
	db dbPool // reuse the interface defined in market_inputs_repo.go
}

// NewTradeOutcomeRepo creates a new TradeOutcomeRepo backed by a live pool.
func NewTradeOutcomeRepo(db *pgxpool.Pool) *TradeOutcomeRepo {
	return &TradeOutcomeRepo{db: db}
}

const upsertTradeOutcomeSQL = `
INSERT INTO trade_outcomes_daily (
  entry_date, list_type, ticker, rank, entry_price,
  return_1d, return_2d, return_3d, return_4d,
  return_5d, return_10d, return_20d,
  max_runup_20d, max_drawdown_20d, evaluated_days,
  is_primary_observation, cross_list_duplicate, cluster_id
) VALUES (
  $1, $2, $3, $4, $5,
  $6, $7, $8, $9,
  $10, $11, $12,
  $13, $14, $15,
  $16, $17, $18
) ON CONFLICT (entry_date, list_type, ticker) DO UPDATE SET
  rank = EXCLUDED.rank,
  entry_price = EXCLUDED.entry_price,
  return_1d = EXCLUDED.return_1d,
  return_2d = EXCLUDED.return_2d,
  return_3d = EXCLUDED.return_3d,
  return_4d = EXCLUDED.return_4d,
  return_5d = EXCLUDED.return_5d,
  return_10d = EXCLUDED.return_10d,
  return_20d = EXCLUDED.return_20d,
  max_runup_20d = EXCLUDED.max_runup_20d,
  max_drawdown_20d = EXCLUDED.max_drawdown_20d,
  evaluated_days = EXCLUDED.evaluated_days,
  is_primary_observation = EXCLUDED.is_primary_observation,
  cross_list_duplicate = EXCLUDED.cross_list_duplicate,
  cluster_id = EXCLUDED.cluster_id,
  is_duplicate_signal = COALESCE(EXCLUDED.is_duplicate_signal, trade_outcomes_daily.is_duplicate_signal),
  updated_at = NOW()`

// UpsertTradeOutcome inserts or replaces a single trade outcome row.
// Idempotent: re-running for the same (entry_date, list_type, ticker) overwrites.
func (r *TradeOutcomeRepo) UpsertTradeOutcome(ctx context.Context, m *models.TradeOutcomeDaily) error {
	_, err := r.db.Exec(ctx, upsertTradeOutcomeSQL,
		m.EntryDate,
		string(m.ListType),
		m.Ticker,
		m.Rank,
		m.EntryPrice,
		m.Return1D,
		m.Return2D,
		m.Return3D,
		m.Return4D,
		m.Return5D,
		m.Return10D,
		m.Return20D,
		m.MaxRunup20D,
		m.MaxDrawdown20D,
		m.EvaluatedDays,
		m.IsPrimaryObservation,
		m.CrossListDuplicate,
		m.ClusterID,
	)
	if err != nil {
		return fmt.Errorf("UpsertTradeOutcome %s %s %s: %w", m.EntryDate.Format("2006-01-02"), m.ListType, m.Ticker, err)
	}
	return nil
}

// GetPendingSignalDates returns distinct entry_dates from daily_rank_lists that
// either have no trade_outcomes_daily row yet, or whose existing row has
// evaluated_days < 20 (i.e. the 20-day window is not yet complete).
//
// The lookback parameter limits how far back to search (in calendar days) so
// that the first run doesn't attempt to backfill the entire history.
// A lookback of 0 means no limit.
func (r *TradeOutcomeRepo) GetPendingSignalDates(ctx context.Context, today time.Time, lookback int) ([]time.Time, error) {
	var whereClause string
	args := make([]any, 0, 2)

	// Use the caller-supplied today rather than CURRENT_DATE so that
	// standalone re-runs with --date=... filter correctly.
	todayStr := today.Format("2006-01-02")

	if lookback > 0 {
		whereClause = "WHERE rl.date >= $1::date - $2::int AND rl.date < $1::date"
		args = append(args, todayStr, lookback)
	} else {
		whereClause = "WHERE rl.date < $1::date"
		args = append(args, todayStr)
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT rl.date
		FROM   daily_rank_lists rl
		LEFT JOIN trade_outcomes_daily tod
		  ON  tod.entry_date = rl.date
		  AND tod.list_type  = rl.list_type
		  AND tod.ticker     = rl.ticker
		%s
		GROUP BY rl.date
		HAVING COUNT(*) FILTER (WHERE tod.entry_date IS NULL OR tod.evaluated_days < 20) > 0
		ORDER  BY rl.date ASC
	`, whereClause)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("GetPendingSignalDates: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("GetPendingSignalDates scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ── STORY-R06 methods ───────────────────────────────────────────────────────────

const selectOutcomeColumns = `
    entry_date, list_type, ticker, rank, entry_price,
    return_1d, return_2d, return_3d, return_4d,
    return_5d, return_10d, return_20d,
    max_runup_20d, max_drawdown_20d, evaluated_days,
    is_duplicate_signal, trading_days_since_prior,
    is_primary_observation, cross_list_duplicate, cluster_id,
    corporate_action_count,
    regime_label, slippage_tier, adv_cap_applied, adv_cap_pct,
    net_return_5d, net_return_10d, net_return_20d,
    exit_type, stop_slippage_bps, regime_bucket,
    created_at, updated_at`

// GetByRegimeBucket returns trade outcomes filtered by regime_bucket.
// regimeBucket: "risk_on", "risk_off", or "all" (no filter)
// listType: filter by list_type, or empty string for all
// minEvaluatedDays: minimum evaluated_days (e.g. 20 for mature windows)
// limit: max rows, 0 = unlimited
func (r *TradeOutcomeRepo) GetByRegimeBucket(
	ctx context.Context,
	regimeBucket string,
	listType string,
	minEvaluatedDays int,
	limit int,
) ([]models.TradeOutcomeDaily, error) {
	args := make([]any, 0, 4)
	where := "WHERE tod.evaluated_days >= $1"
	args = append(args, minEvaluatedDays)
	argIdx := 2

	if regimeBucket != "all" && regimeBucket != "" {
		where += fmt.Sprintf(" AND tod.regime_bucket = $%d", argIdx)
		args = append(args, regimeBucket)
		argIdx++
	}
	if listType != "" {
		where += fmt.Sprintf(" AND tod.list_type = $%d", argIdx)
		args = append(args, listType)
		argIdx++
	}

	query := fmt.Sprintf(`
		SELECT`+selectOutcomeColumns+`
		FROM trade_outcomes_daily tod
		%s
		ORDER BY tod.entry_date DESC, tod.list_type, tod.rank
	`, where)

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("GetByRegimeBucket: %w", err)
	}
	defer rows.Close()

	return scanTradeOutcomeRows(rows)
}

// UpdateNetReturns updates net return columns, slippage_tier, and regime_label
// for a single trade outcome row. Idempotent UPSERT behavior.
// NOTE: regime_bucket is a GENERATED ALWAYS column derived from regime_label,
// so it must NOT appear in the SET clause.
func (r *TradeOutcomeRepo) UpdateNetReturns(
	ctx context.Context,
	entryDate time.Time,
	listType string,
	ticker string,
	netReturn5D, netReturn10D, netReturn20D *float64,
	slippageTier *string,
	advCapApplied bool,
	advCapPct *float64,
	regimeLabel *string,
	exitType *string,
	stopSlippageBps *float64,
) error {
	result, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily
		SET    net_return_5d     = $4,
		       net_return_10d    = $5,
		       net_return_20d    = $6,
		       slippage_tier     = $7,
		       adv_cap_applied   = $8,
		       adv_cap_pct       = $9,
		       regime_label      = $10,
		       exit_type         = $11,
		       stop_slippage_bps = $12,
		       updated_at        = NOW()
		WHERE  entry_date = $1
		  AND  list_type  = $2
		  AND  ticker     = $3
	`, entryDate, listType, ticker,
		netReturn5D, netReturn10D, netReturn20D,
		slippageTier, advCapApplied, advCapPct,
		regimeLabel, exitType, stopSlippageBps)

	if err != nil {
		return fmt.Errorf("UpdateNetReturns %s %s %s: %w",
			entryDate.Format("2006-01-02"), listType, ticker, err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("UpdateNetReturns %s %s %s: no rows affected (row missing?)",
			entryDate.Format("2006-01-02"), listType, ticker)
	}
	return nil
}

// BackfillRegimeLabels joins trade_outcomes_daily to market_regime_daily
// on entry_date = date, and sets regime_label only.
// regime_bucket is a GENERATED ALWAYS column derived from regime_label,
// so it must NOT appear in the SET clause.
// Only updates rows where regime_label IS NULL.
func (r *TradeOutcomeRepo) BackfillRegimeLabels(ctx context.Context) (int, error) {
	result, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily tod
		SET    regime_label = mrd.regime,
		       updated_at   = NOW()
		FROM   market_regime_daily mrd
		WHERE  tod.entry_date = mrd.date
		  AND  tod.regime_label IS NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("BackfillRegimeLabels: %w", err)
	}
	return int(result.RowsAffected()), nil
}

// GetOutcomesByDateRangeWithRegime returns trade outcomes within a date range,
// joined with regime data. Used for stress window analysis.
func (r *TradeOutcomeRepo) GetOutcomesByDateRangeWithRegime(
	ctx context.Context,
	start, end time.Time,
) ([]models.TradeOutcomeDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT`+selectOutcomeColumns+`
		FROM trade_outcomes_daily tod
		LEFT JOIN market_regime_daily mrd ON mrd.date = tod.entry_date
		WHERE tod.entry_date >= $1
		  AND tod.entry_date <= $2
		ORDER BY tod.entry_date DESC, tod.list_type, tod.rank
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("GetOutcomesByDateRangeWithRegime: %w", err)
	}
	defer rows.Close()

	return scanTradeOutcomeRows(rows)
}

// scanTradeOutcomeRows scans trade_outcomes_daily rows including all R06 fields.
func scanTradeOutcomeRows(rows pgx.Rows) ([]models.TradeOutcomeDaily, error) {
	var out []models.TradeOutcomeDaily
	for rows.Next() {
		var m models.TradeOutcomeDaily
		var lt string
		if err := rows.Scan(
			&m.EntryDate,
			&lt,
			&m.Ticker,
			&m.Rank,
			&m.EntryPrice,
			&m.Return1D,
			&m.Return2D,
			&m.Return3D,
			&m.Return4D,
			&m.Return5D,
			&m.Return10D,
			&m.Return20D,
			&m.MaxRunup20D,
			&m.MaxDrawdown20D,
			&m.EvaluatedDays,
			&m.IsDuplicateSignal,
			&m.TradingDaysSincePrior,
			&m.IsPrimaryObservation,
			&m.CrossListDuplicate,
			&m.ClusterID,
			&m.CorporateActionCount,
			&m.RegimeLabel,
			&m.SlippageTier,
			&m.ADVCapApplied,
			&m.ADVCapPct,
			&m.NetReturn5D,
			&m.NetReturn10D,
			&m.NetReturn20D,
			&m.ExitType,
			&m.StopSlippageBps,
			&m.RegimeBucket,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanTradeOutcomeRows: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetRecentOutcomes returns trade outcomes from the most recent N distinct
// entry dates that have at least 1 evaluated day. Used for the performance
// summary endpoint. Results are ordered by entry_date DESC, then list_type, rank.
// Only returns is_primary_observation = true rows to exclude dedup observations.
func (r *TradeOutcomeRepo) GetRecentOutcomes(ctx context.Context, limit int) ([]models.TradeOutcomeDaily, error) {
	rows, err := r.db.Query(ctx, `
		WITH recent_dates AS (
			SELECT DISTINCT entry_date
			FROM trade_outcomes_daily
			WHERE evaluated_days >= 1
			  AND is_primary_observation = true
			ORDER BY entry_date DESC
			LIMIT $1
		)
		SELECT
			t.entry_date,
			t.list_type,
			t.ticker,
			t.rank,
			t.entry_price,
			t.return_1d,
			t.return_2d,
			t.return_3d,
			t.return_4d,
			t.return_5d,
			t.return_10d,
			t.return_20d,
			t.max_runup_20d,
			t.max_drawdown_20d,
			t.evaluated_days,
			t.is_duplicate_signal,
			t.trading_days_since_prior,
			t.is_primary_observation,
			t.cross_list_duplicate,
			t.cluster_id,
			t.created_at,
			t.updated_at
		FROM trade_outcomes_daily t
		JOIN recent_dates rd ON rd.entry_date = t.entry_date
		WHERE t.evaluated_days >= 1
		  AND t.is_primary_observation = true
		ORDER BY t.entry_date DESC, t.list_type, t.rank
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("GetRecentOutcomes: %w", err)
	}
	defer rows.Close()

	var out []models.TradeOutcomeDaily
	for rows.Next() {
		var m models.TradeOutcomeDaily
		var lt string
		if err := rows.Scan(
			&m.EntryDate,
			&lt,
			&m.Ticker,
			&m.Rank,
			&m.EntryPrice,
			&m.Return1D,
			&m.Return2D,
			&m.Return3D,
			&m.Return4D,
			&m.Return5D,
			&m.Return10D,
			&m.Return20D,
			&m.MaxRunup20D,
			&m.MaxDrawdown20D,
			&m.EvaluatedDays,
			&m.IsDuplicateSignal,
			&m.TradingDaysSincePrior,
			&m.IsPrimaryObservation,
			&m.CrossListDuplicate,
			&m.ClusterID,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetRecentOutcomes scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetTradeOutcomes returns all trade outcome rows for the given entry date,
// ordered by list_type then rank.
func (r *TradeOutcomeRepo) GetTradeOutcomes(ctx context.Context, entryDate time.Time) ([]models.TradeOutcomeDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			entry_date,
			list_type,
			ticker,
			rank,
			entry_price,
			return_1d,
			return_2d,
			return_3d,
			return_4d,
			return_5d,
			return_10d,
			return_20d,
			max_runup_20d,
			max_drawdown_20d,
			evaluated_days,
			is_duplicate_signal,
			trading_days_since_prior,
			created_at,
			updated_at
		FROM trade_outcomes_daily
		WHERE entry_date = $1
		ORDER BY list_type, rank
	`, entryDate)
	if err != nil {
		return nil, fmt.Errorf("GetTradeOutcomes %s: %w", entryDate.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.TradeOutcomeDaily
	for rows.Next() {
		var m models.TradeOutcomeDaily
		var lt string
		if err := rows.Scan(
			&m.EntryDate,
			&lt,
			&m.Ticker,
			&m.Rank,
			&m.EntryPrice,
			&m.Return1D,
			&m.Return2D,
			&m.Return3D,
			&m.Return4D,
			&m.Return5D,
			&m.Return10D,
			&m.Return20D,
			&m.MaxRunup20D,
			&m.MaxDrawdown20D,
			&m.EvaluatedDays,
			&m.IsDuplicateSignal,
			&m.TradingDaysSincePrior,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetTradeOutcomes scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}

// DuplicateSignalPair represents a pair of signals for the same ticker where
// the newer signal falls within a trading-day window of an older open signal.
type DuplicateSignalPair struct {
	OriginalTicker     string
	OriginalDate       time.Time
	OriginalListType   string
	NewTicker          string
	NewDate            time.Time
	NewListType        string
	TradingDaysBetween int
}

// FindDuplicateSignals returns pairs of trade_outcomes_daily rows where:
//   - both share the same ticker
//   - the newer signal's entry_date falls within windowDays trading days of the older
//   - the older signal has evaluated_days < 20 (still open)
//   - they are different rows (different entry_date or list_type)
//   - the newer signal is not already flagged (is_duplicate_signal = false)
//
// Trading days are counted using candles_daily rows between the two dates.
func (r *TradeOutcomeRepo) FindDuplicateSignals(ctx context.Context, windowDays int) ([]DuplicateSignalPair, error) {
	query := `
		SELECT
			older.ticker,
			older.entry_date,
			older.list_type,
			newer.ticker,
			newer.entry_date,
			newer.list_type,
			(
				SELECT COUNT(DISTINCT cd.date)
				FROM   candles_daily cd
				WHERE  cd.ticker = older.ticker
				  AND  cd.date > older.entry_date
				  AND  cd.date <= newer.entry_date
			)::int AS trading_days_between
		FROM   trade_outcomes_daily newer
		JOIN   trade_outcomes_daily older
		  ON   older.ticker = newer.ticker
		  AND  older.entry_date < newer.entry_date
		  AND  older.evaluated_days < 20
		  AND  (older.entry_date <> newer.entry_date OR older.list_type <> newer.list_type)
		WHERE  newer.is_duplicate_signal = false
		  AND  (
				SELECT COUNT(DISTINCT cd.date)
				FROM   candles_daily cd
				WHERE  cd.ticker = older.ticker
				  AND  cd.date > older.entry_date
				  AND  cd.date <= newer.entry_date
		       ) <= $1
	`

	rows, err := r.db.Query(ctx, query, windowDays)
	if err != nil {
		return nil, fmt.Errorf("FindDuplicateSignals: %w", err)
	}
	defer rows.Close()

	var out []DuplicateSignalPair
	for rows.Next() {
		var p DuplicateSignalPair
		if err := rows.Scan(
			&p.OriginalTicker, &p.OriginalDate, &p.OriginalListType,
			&p.NewTicker, &p.NewDate, &p.NewListType,
			&p.TradingDaysBetween,
		); err != nil {
			return nil, fmt.Errorf("FindDuplicateSignals scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkDuplicateSignal sets is_duplicate_signal=true and trading_days_since_prior
// for a specific trade outcome row identified by (entry_date, list_type, ticker).
func (r *TradeOutcomeRepo) MarkDuplicateSignal(ctx context.Context, entryDate time.Time, listType, ticker string, daysSincePrior int) error {
	_, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily
		SET    is_duplicate_signal     = true,
		       trading_days_since_prior = $4,
		       updated_at              = NOW()
		WHERE  entry_date = $1
		  AND  list_type  = $2
		  AND  ticker     = $3
	`, entryDate, listType, ticker, daysSincePrior)
	if err != nil {
		return fmt.Errorf("MarkDuplicateSignal %s %s %s: %w",
			entryDate.Format("2006-01-02"), listType, ticker, err)
	}
	return nil
}

// ── STORY-R04 dedup methods ────────────────────────────────────────────────────

// PrimaryWindowDays is the number of trading days defining the canonical
// observation window for is_primary_observation.
const PrimaryWindowDays = 20

// GetPriorPrimaryObservation returns the most recent row for (ticker, list_type)
// whose entry_date is within PrimaryWindowDays trading days of the given date.
// Returns nil if no prior observation exists.
func (r *TradeOutcomeRepo) GetPriorPrimaryObservation(ctx context.Context, ticker, listType string, entryDate time.Time) (*models.TradeOutcomeDaily, error) {
	row := r.db.QueryRow(ctx, `
		SELECT entry_date, list_type, ticker, rank, entry_price,
		       return_1d, return_2d, return_3d, return_4d,
		       return_5d, return_10d, return_20d,
		       max_runup_20d, max_drawdown_20d, evaluated_days,
		       is_duplicate_signal, trading_days_since_prior,
		       is_primary_observation, cross_list_duplicate, cluster_id,
		       corporate_action_count,
		       created_at, updated_at
		FROM   trade_outcomes_daily
		WHERE  ticker     = $1
		  AND  list_type  = $2
		  AND  entry_date < $3
		  AND  entry_date >= (
		        SELECT MIN(cd.date)
		        FROM   candles_daily cd
		        WHERE  cd.ticker = $1
		          AND  cd.date >= (
		            SELECT MIN(sub.date) FROM (
		              SELECT date FROM candles_daily
		              WHERE ticker = $1 AND date < $3
		              ORDER BY date DESC LIMIT $4
		            ) sub
		          )
		      )
		ORDER BY entry_date DESC
		LIMIT  1
	`, ticker, listType, entryDate, PrimaryWindowDays+1)

	var m models.TradeOutcomeDaily
	var lt string
	err := row.Scan(
		&m.EntryDate, &lt, &m.Ticker, &m.Rank, &m.EntryPrice,
		&m.Return1D, &m.Return2D, &m.Return3D, &m.Return4D,
		&m.Return5D, &m.Return10D, &m.Return20D,
		&m.MaxRunup20D, &m.MaxDrawdown20D, &m.EvaluatedDays,
		&m.IsDuplicateSignal, &m.TradingDaysSincePrior,
		&m.IsPrimaryObservation, &m.CrossListDuplicate, &m.ClusterID,
		&m.CorporateActionCount,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no prior primary observation found
		}
		return nil, fmt.Errorf("GetPriorPrimaryObservation %s %s: %w", ticker, listType, err)
	}
	m.ListType = models.ListType(strings.Clone(lt))
	m.Ticker = strings.Clone(m.Ticker)
	return &m, nil
}

// GetCrossListPriorTicker returns the highest-priority existing row for the
// same ticker and entry_date across any list type. Priority: leaders > momentum > ep.
// Returns nil if no cross-list row exists.
func (r *TradeOutcomeRepo) GetCrossListPriorTicker(ctx context.Context, ticker string, entryDate time.Time) (*models.TradeOutcomeDaily, error) {
	row := r.db.QueryRow(ctx, `
		SELECT entry_date, list_type, ticker, rank, entry_price,
		       return_1d, return_2d, return_3d, return_4d,
		       return_5d, return_10d, return_20d,
		       max_runup_20d, max_drawdown_20d, evaluated_days,
		       is_duplicate_signal, trading_days_since_prior,
		       is_primary_observation, cross_list_duplicate, cluster_id,
		       corporate_action_count,
		       created_at, updated_at
		FROM   trade_outcomes_daily
		WHERE  ticker     = $1
		  AND  entry_date = $2
		ORDER  BY
		  CASE list_type
		    WHEN 'leaders'   THEN 1
		    WHEN 'momentum'  THEN 2
		    WHEN 'ep'        THEN 3
		    ELSE 4
		  END
		LIMIT 1
	`, ticker, entryDate)

	var m models.TradeOutcomeDaily
	var lt string
	err := row.Scan(
		&m.EntryDate, &lt, &m.Ticker, &m.Rank, &m.EntryPrice,
		&m.Return1D, &m.Return2D, &m.Return3D, &m.Return4D,
		&m.Return5D, &m.Return10D, &m.Return20D,
		&m.MaxRunup20D, &m.MaxDrawdown20D, &m.EvaluatedDays,
		&m.IsDuplicateSignal, &m.TradingDaysSincePrior,
		&m.IsPrimaryObservation, &m.CrossListDuplicate, &m.ClusterID,
		&m.CorporateActionCount,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no cross-list prior ticker found
		}
		return nil, fmt.Errorf("GetCrossListPriorTicker %s: %w", ticker, err)
	}
	m.ListType = models.ListType(strings.Clone(lt))
	m.Ticker = strings.Clone(m.Ticker)
	return &m, nil
}

// MarkCrossListDuplicate sets cross_list_duplicate = true and
// is_primary_observation = false for the given row.
func (r *TradeOutcomeRepo) MarkCrossListDuplicate(ctx context.Context, entryDate time.Time, listType, ticker string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily
		SET    cross_list_duplicate     = true,
		       is_primary_observation   = false,
		       updated_at               = NOW()
		WHERE  entry_date = $1
		  AND  list_type  = $2
		  AND  ticker     = $3
	`, entryDate, listType, ticker)
	if err != nil {
		return fmt.Errorf("MarkCrossListDuplicate %s %s %s: %w",
			entryDate.Format("2006-01-02"), listType, ticker, err)
	}
	return nil
}

// UpdateDedupFields sets is_primary_observation, cross_list_duplicate, and cluster_id
// for a specific (entry_date, list_type, ticker) row in a single UPDATE call.
func (r *TradeOutcomeRepo) UpdateDedupFields(ctx context.Context, entryDate time.Time, listType, ticker string, isPrimary, crossListDup bool, clusterID *int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily
		SET    is_primary_observation = $4,
		       cross_list_duplicate   = $5,
		       cluster_id             = $6,
		       updated_at             = NOW()
		WHERE  entry_date = $1
		  AND  list_type  = $2
		  AND  ticker     = $3
	`, entryDate, listType, ticker, isPrimary, crossListDup, clusterID)
	if err != nil {
		return fmt.Errorf("UpdateDedupFields %s %s %s: %w",
			entryDate.Format("2006-01-02"), listType, ticker, err)
	}
	return nil
}

// CountTradingDays counts the number of distinct trading days (candle dates) between
// two dates for the given ticker using candles_daily. Returns an error if the
// underlying query fails.
func (r *TradeOutcomeRepo) CountTradingDays(ctx context.Context, ticker string, from, to time.Time) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT date)::INT
		FROM   candles_daily
		WHERE  ticker = $1
		  AND  date > $2
		  AND  date <= $3
	`, ticker, from, to).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("CountTradingDays %s %s-%s: %w", ticker, from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}
	return count, nil
}

// UpdateClusterID sets the cluster_id for a specific (entry_date, list_type, ticker) row.
func (r *TradeOutcomeRepo) UpdateClusterID(ctx context.Context, entryDate time.Time, listType, ticker string, clusterID int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE trade_outcomes_daily
		SET    cluster_id  = $4,
		       updated_at  = NOW()
		WHERE  entry_date = $1
		  AND  list_type  = $2
		  AND  ticker     = $3
	`, entryDate, listType, ticker, clusterID)
	if err != nil {
		return fmt.Errorf("UpdateClusterID %s %s %s: %w",
			entryDate.Format("2006-01-02"), listType, ticker, err)
	}
	return nil
}

// GetEffectiveSampleSize returns raw_N and effective_N for the given query scope.
// effective_N = rows WHERE is_primary_observation = true AND evaluated_days >= 20.
// Logs a warning when raw_N / effective_N > 1.5 via the supplied logger.
func (r *TradeOutcomeRepo) GetEffectiveSampleSize(ctx context.Context, scope string) (rawN, effectiveN int, err error) {
	// scope can be: 'all', a list_type, or a date range identifier
	var whereClause string
	switch scope {
	case "all":
		whereClause = "WHERE evaluated_days >= 1"
	default:
		whereClause = "WHERE evaluated_days >= 1 AND list_type = $1"
	}

	query := fmt.Sprintf(`
		SELECT
			COUNT(*)::INT AS raw_n,
			COUNT(*) FILTER (WHERE is_primary_observation = true AND evaluated_days >= 20)::INT AS effective_n
		FROM trade_outcomes_daily
		%s
	`, whereClause)

	var args []any
	if scope != "all" {
		args = append(args, scope)
	}
	err = r.db.QueryRow(ctx, query, args...).Scan(&rawN, &effectiveN)
	if err != nil {
		return 0, 0, fmt.Errorf("GetEffectiveSampleSize: %w", err)
	}
	return rawN, effectiveN, nil
}

// GetCrossListDuplicateCount returns the count of rows with cross_list_duplicate = true
// for the given date range.
func (r *TradeOutcomeRepo) GetCrossListDuplicateCount(ctx context.Context, start, end time.Time) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM   trade_outcomes_daily
		WHERE  cross_list_duplicate = true
		  AND  entry_date >= $1
		  AND  entry_date <= $2
	`, start, end).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("GetCrossListDuplicateCount %s-%s: %w", start.Format("2006-01-02"), end.Format("2006-01-02"), err)
	}
	return count, nil
}

// GetAllOutcomes returns every trade outcome row with at least 1 evaluated day
// where is_primary_observation = true. Used for the verified track record page.
// Ordered by entry_date DESC, list_type, rank.
func (r *TradeOutcomeRepo) GetAllOutcomes(ctx context.Context) ([]models.TradeOutcomeDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			entry_date,
			list_type,
			ticker,
			rank,
			entry_price,
			return_1d,
			return_2d,
			return_3d,
			return_4d,
			return_5d,
			return_10d,
			return_20d,
			max_runup_20d,
			max_drawdown_20d,
			evaluated_days,
			is_duplicate_signal,
			trading_days_since_prior,
			is_primary_observation,
			cross_list_duplicate,
			cluster_id,
			created_at,
			updated_at
		FROM trade_outcomes_daily
		WHERE evaluated_days >= 1
		  AND is_primary_observation = true
		ORDER BY entry_date DESC, list_type, rank
	`)
	if err != nil {
		return nil, fmt.Errorf("GetAllOutcomes: %w", err)
	}
	defer rows.Close()

	var out []models.TradeOutcomeDaily
	for rows.Next() {
		var m models.TradeOutcomeDaily
		var lt string
		if err := rows.Scan(
			&m.EntryDate,
			&lt,
			&m.Ticker,
			&m.Rank,
			&m.EntryPrice,
			&m.Return1D,
			&m.Return2D,
			&m.Return3D,
			&m.Return4D,
			&m.Return5D,
			&m.Return10D,
			&m.Return20D,
			&m.MaxRunup20D,
			&m.MaxDrawdown20D,
			&m.EvaluatedDays,
			&m.IsDuplicateSignal,
			&m.TradingDaysSincePrior,
			&m.IsPrimaryObservation,
			&m.CrossListDuplicate,
			&m.ClusterID,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetAllOutcomes scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}
