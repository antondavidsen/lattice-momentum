package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// SurgeWeightsRepo handles persistence for the surge_score_weights table.
type SurgeWeightsRepo struct {
	db *pgxpool.Pool
}

// NewSurgeWeightsRepo creates a new SurgeWeightsRepo backed by a live pool.
func NewSurgeWeightsRepo(db *pgxpool.Pool) *SurgeWeightsRepo {
	return &SurgeWeightsRepo{db: db}
}

// GetActive returns the currently active weight set.
func (r *SurgeWeightsRepo) GetActive(ctx context.Context) (*models.SurgeScoreWeights, error) {
	var m models.SurgeScoreWeights
	err := r.db.QueryRow(ctx, `
		SELECT id, version, computed_date,
		       w_gap, w_volume, w_catalyst, w_float, w_sector, w_similarity, w_chart,
		       method, sample_size, training_win_rate, training_auc,
		       train_start, train_end, active, created_at
		FROM   surge_score_weights
		WHERE  active = TRUE
		LIMIT  1
	`).Scan(
		&m.ID, &m.Version, &m.ComputedDate,
		&m.WGap, &m.WVolume, &m.WCatalyst, &m.WFloat, &m.WSector, &m.WSimilarity, &m.WChart,
		&m.Method, &m.SampleSize, &m.TrainingWinRate, &m.TrainingAUC,
		&m.TrainStart, &m.TrainEnd, &m.Active, &m.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("GetActive surge_score_weights: %w", err)
	}
	m.Method = strings.Clone(m.Method)
	return &m, nil
}

// Insert creates a new weight row (inactive by default).
func (r *SurgeWeightsRepo) Insert(ctx context.Context, m *models.SurgeScoreWeights) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO surge_score_weights (
			version, computed_date,
			w_gap, w_volume, w_catalyst, w_float, w_sector, w_similarity,
			method, sample_size, training_win_rate, training_auc,
			train_start, train_end, active
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`,
		m.Version, m.ComputedDate,
		m.WGap, m.WVolume, m.WCatalyst, m.WFloat, m.WSector, m.WSimilarity,
		m.Method, m.SampleSize, m.TrainingWinRate, m.TrainingAUC,
		m.TrainStart, m.TrainEnd, m.Active,
	)
	return err
}

// SetActive activates a specific version and deactivates all others.
func (r *SurgeWeightsRepo) SetActive(ctx context.Context, version int) error {
	_, err := r.db.Exec(ctx, `UPDATE surge_score_weights SET active = (version = $1)`, version)
	return err
}

// ── PerformanceWindowRepo ─────────────────────────────────────────────────────

// PerformanceWindowRepo handles persistence for the performance_windows table.
type PerformanceWindowRepo struct {
	db *pgxpool.Pool
}

// TradeOutcomeAggregation holds per-pipeline aggregate metrics from trade_outcomes_daily.
type TradeOutcomeAggregation struct {
	ListType        string
	TotalOutcomes   int
	WinRate         float64
	MedianReturn    float64
	MeanReturn      float64
	WinRate5d       float64
	WinRate10d      float64
	WinRate20d      float64
	MedianReturn5d  float64
	MedianReturn10d float64
	MedianReturn20d float64
	GrossReturn5d   float64
	GrossReturn10d  float64
	GrossReturn20d  float64
}

// AggregateTradeOutcomes queries trade_outcomes_daily grouped by list_type,
// applying evaluability filters. Returns one row per list_type that has
// >= minOutcomes evaluable rows.
func (r *PerformanceWindowRepo) AggregateTradeOutcomes(ctx context.Context, minOutcomes int) ([]TradeOutcomeAggregation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			list_type,
			COUNT(*)                                                          AS total_outcomes,
			COUNT(*) FILTER (WHERE return_20d > 0)::NUMERIC / NULLIF(COUNT(*), 0) AS win_rate,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY return_20d)               AS median_return,
			AVG(return_20d)                                                       AS mean_return,
			COUNT(*) FILTER (WHERE return_5d > 0)::NUMERIC / NULLIF(COUNT(*), 0)  AS win_rate_5d,
			COUNT(*) FILTER (WHERE return_10d > 0)::NUMERIC / NULLIF(COUNT(*), 0) AS win_rate_10d,
			COUNT(*) FILTER (WHERE return_20d > 0)::NUMERIC / NULLIF(COUNT(*), 0) AS win_rate_20d,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY return_5d)                AS median_return_5d,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY return_10d)               AS median_return_10d,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY return_20d)               AS median_return_20d,
			AVG(return_5d)                                                        AS gross_return_5d,
			AVG(return_10d)                                                       AS gross_return_10d,
			AVG(return_20d)                                                       AS gross_return_20d
		FROM trade_outcomes_daily
		WHERE evaluated_days >= 20
		  AND is_duplicate_signal = FALSE
		  AND return_5d IS NOT NULL
		  AND return_10d IS NOT NULL
		  AND return_20d IS NOT NULL
		GROUP BY list_type
		HAVING COUNT(*) >= $1
	`, minOutcomes)
	if err != nil {
		return nil, fmt.Errorf("AggregateTradeOutcomes: %w", err)
	}
	defer rows.Close()

	var out []TradeOutcomeAggregation
	for rows.Next() {
		var a TradeOutcomeAggregation
		if err := rows.Scan(
			&a.ListType,
			&a.TotalOutcomes, &a.WinRate, &a.MedianReturn, &a.MeanReturn,
			&a.WinRate5d, &a.WinRate10d, &a.WinRate20d,
			&a.MedianReturn5d, &a.MedianReturn10d, &a.MedianReturn20d,
			&a.GrossReturn5d, &a.GrossReturn10d, &a.GrossReturn20d,
		); err != nil {
			return nil, fmt.Errorf("scan TradeOutcomeAggregation: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SPYForwardReturn holds the SPY benchmark forward return for a single horizon.
type SPYForwardReturn struct {
	DaysForward int
	Return      float64
}

// SPYBaselineRates holds the SPY win rates and median returns for the 5d, 10d, and 20d horizons.
type SPYBaselineRates struct {
	WinRate5d    float64
	WinRate10d   float64
	WinRate20d   float64
	MedianRet5d  float64
	MedianRet10d float64
	MedianRet20d float64
}

// GetSPYBaselineWinRates computes the SPY win rate (return > 0) for each forward
// horizon (5d, 10d, 20d) using candles_daily data up to (and including) the
// latest available date.
//
// TODO: Option A — migrate to a rolling historical baseline from a broader
// universe once the historical baseline table is built.
func (r *PerformanceWindowRepo) GetSPYBaselineWinRates(ctx context.Context) (*SPYBaselineRates, error) {
	var b SPYBaselineRates
	err := r.db.QueryRow(ctx, `
		WITH spy_close AS (
			SELECT date, close,
				LAG(close, 5)  OVER (ORDER BY date) AS close_5d_ago,
				LAG(close, 10) OVER (ORDER BY date) AS close_10d_ago,
				LAG(close, 20) OVER (ORDER BY date) AS close_20d_ago
			FROM candles_daily
			WHERE ticker = 'SPY'
			  AND close IS NOT NULL
		),
		spy_returns AS (
			SELECT date,
				(close - close_5d_ago)  / close_5d_ago  AS ret_5d,
				(close - close_10d_ago) / close_10d_ago AS ret_10d,
				(close - close_20d_ago) / close_20d_ago AS ret_20d
			FROM spy_close
			WHERE close_5d_ago IS NOT NULL
			  AND close_10d_ago IS NOT NULL
			  AND close_20d_ago IS NOT NULL
		)
		SELECT
			COUNT(*) FILTER (WHERE ret_5d > 0)::NUMERIC / NULLIF(COUNT(*), 0),
			COUNT(*) FILTER (WHERE ret_10d > 0)::NUMERIC / NULLIF(COUNT(*), 0),
			COUNT(*) FILTER (WHERE ret_20d > 0)::NUMERIC / NULLIF(COUNT(*), 0),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ret_5d),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ret_10d),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ret_20d)
		FROM spy_returns
	`).Scan(&b.WinRate5d, &b.WinRate10d, &b.WinRate20d, &b.MedianRet5d, &b.MedianRet10d, &b.MedianRet20d)
	if err != nil {
		return nil, fmt.Errorf("GetSPYBaselineWinRates: %w", err)
	}
	return &b, nil
}

// GetRegimeLabel retrieves the regime label from market_regime_daily for the
// given date (exact match).
func (r *PerformanceWindowRepo) GetRegimeLabel(ctx context.Context, date time.Time) (*string, error) {
	var label *string
	err := r.db.QueryRow(ctx, `
		SELECT regime
		FROM market_regime_daily
		WHERE date = $1
	`, date).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no regime label found for date
	}
	if err != nil {
		return nil, fmt.Errorf("GetRegimeLabel %s: %w", date.Format("2006-01-02"), err)
	}
	return label, nil
}

// UpsertPerformanceWindow inserts or replaces a performance_windows row keyed
// on (pipeline_type, window_date, window_size, regime_bucket). regime_bucket was
// added by migration 065 for R06 regime-segmented performance reporting.
func (r *PerformanceWindowRepo) UpsertPerformanceWindow(ctx context.Context, w *models.PerformanceWindow) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO performance_windows (
			pipeline_type, window_date, window_size,
			win_rate, median_return, mean_return,
			hit_rate_15pct, avg_catalyst_score,
			baseline_win_rate, baseline_median, decay_pct, alert_triggered,
			regime_label, regime_bucket,
			consecutive_decay_windows, consecutive_recovery_windows,
			net_return_5d, net_return_10d, net_return_20d,
			gross_return_5d, gross_return_10d, gross_return_20d,
			is_evaluable, exclusion_reason, total_outcomes,
			win_rate_5d, win_rate_10d, win_rate_20d
		) VALUES (
			$1,  $2,  $3,
			$4,  $5,  $6,
			$7,  $8,
			$9,  $10, $11, $12,
			$13, $14,
			$15, $16,
			$17, $18, $19,
			$20, $21, $22,
			$23, $24, $25,
			$26, $27, $28
		) ON CONFLICT (pipeline_type, window_date, window_size, regime_bucket) DO UPDATE SET
			win_rate                     = EXCLUDED.win_rate,
			median_return                = EXCLUDED.median_return,
			mean_return                  = EXCLUDED.mean_return,
			hit_rate_15pct               = EXCLUDED.hit_rate_15pct,
			avg_catalyst_score           = EXCLUDED.avg_catalyst_score,
			baseline_win_rate            = EXCLUDED.baseline_win_rate,
			baseline_median              = EXCLUDED.baseline_median,
			decay_pct                    = EXCLUDED.decay_pct,
			alert_triggered              = EXCLUDED.alert_triggered,
			regime_label                 = EXCLUDED.regime_label,
			regime_bucket                = EXCLUDED.regime_bucket,
			consecutive_decay_windows    = EXCLUDED.consecutive_decay_windows,
			consecutive_recovery_windows = EXCLUDED.consecutive_recovery_windows,
			net_return_5d                = EXCLUDED.net_return_5d,
			net_return_10d               = EXCLUDED.net_return_10d,
			net_return_20d               = EXCLUDED.net_return_20d,
			gross_return_5d              = EXCLUDED.gross_return_5d,
			gross_return_10d             = EXCLUDED.gross_return_10d,
			gross_return_20d             = EXCLUDED.gross_return_20d,
			is_evaluable                 = EXCLUDED.is_evaluable,
			exclusion_reason             = EXCLUDED.exclusion_reason,
			total_outcomes               = EXCLUDED.total_outcomes,
			win_rate_5d                  = EXCLUDED.win_rate_5d,
			win_rate_10d                 = EXCLUDED.win_rate_10d,
			win_rate_20d                 = EXCLUDED.win_rate_20d,
			updated_at                   = NOW()
	`, w.PipelineType, w.WindowDate, w.WindowSize,
		w.WinRate, w.MedianReturn, w.MeanReturn,
		w.HitRate15Pct, w.AvgCatalystScore,
		w.BaselineWinRate, w.BaselineMedian, w.DecayPct, w.AlertTriggered,
		w.RegimeLabel, w.RegimeBucket,
		w.ConsecutiveDecayWindows, w.ConsecutiveRecoveryWindows,
		w.NetReturn5d, w.NetReturn10d, w.NetReturn20d,
		w.GrossReturn5d, w.GrossReturn10d, w.GrossReturn20d,
		w.IsEvaluable, w.ExclusionReason, w.TotalOutcomes,
		w.WinRate5d, w.WinRate10d, w.WinRate20d,
	)
	if err != nil {
		return fmt.Errorf("UpsertPerformanceWindow %s/%s: %w", w.PipelineType, w.WindowDate.Format("2006-01-02"), err)
	}
	return nil
}

// GetPerformanceWindowsBefore retrieves up to lookback windows for the given
// pipeline_type, ordered by window_date DESC, ending before the cutoff date.
// Used by the hysteresis logic in PerformanceMonitorJob.
func (r *PerformanceWindowRepo) GetPerformanceWindowsBefore(ctx context.Context, pipelineType string, beforeDate time.Time, lookback int) ([]models.PerformanceWindow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			id, pipeline_type, window_date, window_size,
			win_rate, median_return, mean_return,
			hit_rate_15pct, avg_catalyst_score,
			baseline_win_rate, baseline_median, decay_pct, alert_triggered,
			regime_label, regime_bucket,
			consecutive_decay_windows, consecutive_recovery_windows,
			net_return_5d, net_return_10d, net_return_20d,
			gross_return_5d, gross_return_10d, gross_return_20d,
			is_evaluable, exclusion_reason, total_outcomes,
			win_rate_5d, win_rate_10d, win_rate_20d,
			created_at
		FROM performance_windows
		WHERE pipeline_type = $1
		  AND window_date < $2
		ORDER BY window_date DESC
		LIMIT $3
	`, pipelineType, beforeDate, lookback)
	if err != nil {
		return nil, fmt.Errorf("GetPerformanceWindowsBefore %s: %w", pipelineType, err)
	}
	defer rows.Close()

	var out []models.PerformanceWindow
	for rows.Next() {
		var w models.PerformanceWindow
		if err := rows.Scan(
			&w.ID, &w.PipelineType, &w.WindowDate, &w.WindowSize,
			&w.WinRate, &w.MedianReturn, &w.MeanReturn,
			&w.HitRate15Pct, &w.AvgCatalystScore,
			&w.BaselineWinRate, &w.BaselineMedian, &w.DecayPct, &w.AlertTriggered,
			&w.RegimeLabel, &w.RegimeBucket,
			&w.ConsecutiveDecayWindows, &w.ConsecutiveRecoveryWindows,
			&w.NetReturn5d, &w.NetReturn10d, &w.NetReturn20d,
			&w.GrossReturn5d, &w.GrossReturn10d, &w.GrossReturn20d,
			&w.IsEvaluable, &w.ExclusionReason, &w.TotalOutcomes,
			&w.WinRate5d, &w.WinRate10d, &w.WinRate20d,
			&w.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan PerformanceWindow: %w", err)
		}
		w.PipelineType = strings.Clone(w.PipelineType)
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetLatest retrieves the most recent performance_windows row for the given
// pipeline_type and window_size. Used by the API handler for dashboard display.
func (r *PerformanceWindowRepo) GetLatest(ctx context.Context, pipelineType string, windowSize int) (*models.PerformanceWindow, error) {
	if r == nil {
		return nil, fmt.Errorf("PerformanceWindowRepo is nil")
	}
	rows, _ := r.db.Query(ctx, `
		SELECT
			id, pipeline_type, window_date, window_size,
			win_rate, median_return, mean_return,
			hit_rate_15pct, avg_catalyst_score,
			baseline_win_rate, baseline_median, decay_pct, alert_triggered,
			regime_label, regime_bucket,
			consecutive_decay_windows, consecutive_recovery_windows,
			net_return_5d, net_return_10d, net_return_20d,
			gross_return_5d, gross_return_10d, gross_return_20d,
			is_evaluable, exclusion_reason, total_outcomes,
			win_rate_5d, win_rate_10d, win_rate_20d,
			created_at
		FROM performance_windows
		WHERE pipeline_type = $1 AND window_size = $2
		ORDER BY window_date DESC
		LIMIT 1
	`, pipelineType, windowSize)
	if rows == nil {
		return nil, fmt.Errorf("GetLatest performance_windows %s/%d: row is nil", pipelineType, windowSize)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("GetLatest performance_windows %s/%d: %w", pipelineType, windowSize, pgx.ErrNoRows)
	}
	var w models.PerformanceWindow
	if err := rows.Scan(
		&w.ID, &w.PipelineType, &w.WindowDate, &w.WindowSize,
		&w.WinRate, &w.MedianReturn, &w.MeanReturn,
		&w.HitRate15Pct, &w.AvgCatalystScore,
		&w.BaselineWinRate, &w.BaselineMedian, &w.DecayPct, &w.AlertTriggered,
		&w.RegimeLabel, &w.RegimeBucket,
		&w.ConsecutiveDecayWindows, &w.ConsecutiveRecoveryWindows,
		&w.NetReturn5d, &w.NetReturn10d, &w.NetReturn20d,
		&w.GrossReturn5d, &w.GrossReturn10d, &w.GrossReturn20d,
		&w.IsEvaluable, &w.ExclusionReason, &w.TotalOutcomes,
		&w.WinRate5d, &w.WinRate10d, &w.WinRate20d,
		&w.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetLatest scan: %w", err)
	}
	w.PipelineType = strings.Clone(w.PipelineType)
	return &w, nil
}

// GetLatestEvaluable retrieves the most recent evaluable performance_windows row
// for the given pipeline_type and window_size. Uses is_evaluable = TRUE to ensure
// only windows with sufficient data quality are returned.
func (r *PerformanceWindowRepo) GetLatestEvaluable(ctx context.Context, pipelineType string, windowSize int) (*models.PerformanceWindow, error) {
	if r == nil {
		return nil, fmt.Errorf("PerformanceWindowRepo is nil")
	}
	rows, _ := r.db.Query(ctx, `
		SELECT
			id, pipeline_type, window_date, window_size,
			win_rate, median_return, mean_return,
			hit_rate_15pct, avg_catalyst_score,
			baseline_win_rate, baseline_median, decay_pct, alert_triggered,
			regime_label, regime_bucket,
			consecutive_decay_windows, consecutive_recovery_windows,
			net_return_5d, net_return_10d, net_return_20d,
			gross_return_5d, gross_return_10d, gross_return_20d,
			is_evaluable, exclusion_reason, total_outcomes,
			win_rate_5d, win_rate_10d, win_rate_20d,
			created_at
		FROM performance_windows
		WHERE pipeline_type = $1 AND window_size = $2 AND is_evaluable = TRUE
		ORDER BY window_date DESC
		LIMIT 1
	`, pipelineType, windowSize)
	if rows == nil {
		return nil, fmt.Errorf("GetLatestEvaluable performance_windows %s/%d: row is nil", pipelineType, windowSize)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("GetLatestEvaluable performance_windows %s/%d: %w", pipelineType, windowSize, pgx.ErrNoRows)
	}
	var w models.PerformanceWindow
	if err := rows.Scan(
		&w.ID, &w.PipelineType, &w.WindowDate, &w.WindowSize,
		&w.WinRate, &w.MedianReturn, &w.MeanReturn,
		&w.HitRate15Pct, &w.AvgCatalystScore,
		&w.BaselineWinRate, &w.BaselineMedian, &w.DecayPct, &w.AlertTriggered,
		&w.RegimeLabel, &w.RegimeBucket,
		&w.ConsecutiveDecayWindows, &w.ConsecutiveRecoveryWindows,
		&w.NetReturn5d, &w.NetReturn10d, &w.NetReturn20d,
		&w.GrossReturn5d, &w.GrossReturn10d, &w.GrossReturn20d,
		&w.IsEvaluable, &w.ExclusionReason, &w.TotalOutcomes,
		&w.WinRate5d, &w.WinRate10d, &w.WinRate20d,
		&w.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("GetLatestEvaluable scan: %w", err)
	}
	w.PipelineType = strings.Clone(w.PipelineType)
	return &w, nil
}

// ── PromptExperimentRepo ──────────────────────────────────────────────────────

// PromptExperimentRepo handles persistence for the prompt_experiment_results table.
type PromptExperimentRepo struct {
	db *pgxpool.Pool
}

// NewPromptExperimentRepo creates a new PromptExperimentRepo with the given database connection pool.
func NewPromptExperimentRepo(db *pgxpool.Pool) *PromptExperimentRepo {
	return &PromptExperimentRepo{db: db}
}

// Upsert inserts or updates a prompt_experiment_results row keyed on
func (r *PromptExperimentRepo) Upsert(ctx context.Context, m *models.PromptExperimentResult) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO prompt_experiment_results (
			prompt_version, pipeline_type, evaluation_date,
			total_picks, avg_intraday_return, median_intraday_return,
			win_rate, avg_llm_confidence, confidence_calibration,
			trade_start_date, trade_end_date
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (prompt_version, pipeline_type, evaluation_date) DO UPDATE SET
			total_picks           = EXCLUDED.total_picks,
			avg_intraday_return   = EXCLUDED.avg_intraday_return,
			median_intraday_return = EXCLUDED.median_intraday_return,
			win_rate              = EXCLUDED.win_rate,
			avg_llm_confidence    = EXCLUDED.avg_llm_confidence,
			confidence_calibration = EXCLUDED.confidence_calibration,
			trade_start_date      = EXCLUDED.trade_start_date,
			trade_end_date        = EXCLUDED.trade_end_date
	`,
		m.PromptVersion, m.PipelineType, m.EvaluationDate,
		m.TotalPicks, m.AvgIntradayReturn, m.MedianIntradayReturn,
		m.WinRate, m.AvgLLMConfidence, m.ConfidenceCalibration,
		m.TradeStartDate, m.TradeEndDate,
	)
	return err
}

// GetAllResults returns every prompt experiment result row, ordered by
// evaluation_date DESC then prompt_version DESC.
func (r *PromptExperimentRepo) GetAllResults(ctx context.Context) ([]models.PromptExperimentResult, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, prompt_version, pipeline_type, evaluation_date,
		       total_picks, avg_intraday_return, median_intraday_return,
		       win_rate, avg_llm_confidence, confidence_calibration,
		       trade_start_date, trade_end_date, created_at
		FROM   prompt_experiment_results
		ORDER  BY evaluation_date DESC, prompt_version DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetAllResults: %w", err)
	}
	defer rows.Close()

	var out []models.PromptExperimentResult
	for rows.Next() {
		var m models.PromptExperimentResult
		if err := rows.Scan(
			&m.ID, &m.PromptVersion, &m.PipelineType, &m.EvaluationDate,
			&m.TotalPicks, &m.AvgIntradayReturn, &m.MedianIntradayReturn,
			&m.WinRate, &m.AvgLLMConfidence, &m.ConfidenceCalibration,
			&m.TradeStartDate, &m.TradeEndDate, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetAllResults scan: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}
