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

// ── Matched-Pair Data Types (STORY-R05) ─────────────────────────────────────────────

// MatchedPairData holds the two arms of the matched-pair analysis.
type MatchedPairData struct {
	Recommended []MatchedPairRow
	Controls    []MatchedPairRow
}

// MatchedPairRow holds a single observation for matched-pair analysis.
type MatchedPairRow struct {
	EntryDate      time.Time
	ListType       string
	Ticker         string
	Sector         string
	FinalScore     float64
	StopPrice      *float64
	EntryPrice     float64
	Target1        *float64
	Target2        *float64
	ExitType       *string
	ExitPrice      *float64
	Return5d       float64
	Return10d      float64
	Return20d      float64
	MaxRunup20d    float64
	MaxDrawdown20d float64
	ActualRR       *float64
	ClusterID      int64
}

// PromptTickerOutcomeRepo handles persistence for the prompt_ticker_outcomes table.
type PromptTickerOutcomeRepo struct {
	db dbPool
}

// NewPromptTickerOutcomeRepo creates a new repo backed by a live pool.
func NewPromptTickerOutcomeRepo(db *pgxpool.Pool) *PromptTickerOutcomeRepo {
	return &PromptTickerOutcomeRepo{db: db}
}

// UpsertOutcome inserts or replaces a prompt ticker outcome row.
// Idempotent on (date, list_type, ticker).
func (r *PromptTickerOutcomeRepo) UpsertOutcome(ctx context.Context, m *models.PromptTickerOutcome) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO prompt_ticker_outcomes (
			date, list_type, ticker, prompt_version, llm_recommended,
			recommended_setup, recommended_entry_low, recommended_entry_high,
			recommended_stop, recommended_target_1, recommended_target_2,
			recommended_rr, recommended_size, recommended_conviction,
			actual_entry_price, actual_return_5d, actual_return_10d, actual_return_20d,
			actual_max_runup, actual_max_drawdown,
			stop_hit, target_1_hit, target_2_hit, actual_rr_achieved,
			evaluated_days,
			disqualified, disqualifier_reason,
			ml_feature_name, ml_weight_delta, ml_suggestion_confidence,
			exit_type, exit_price, exit_date, t1_hit, levels_invalid
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14,
			$15, $16, $17, $18,
			$19, $20,
			$21, $22, $23, $24,
			$25,
			$26, $27,
			$28, $29, $30,
			$31, $32, $33, $34, $35
		)
		ON CONFLICT (date, list_type, ticker) DO UPDATE SET
			prompt_version       = EXCLUDED.prompt_version,
			llm_recommended      = EXCLUDED.llm_recommended,
			recommended_setup    = EXCLUDED.recommended_setup,
			recommended_entry_low = EXCLUDED.recommended_entry_low,
			recommended_entry_high = EXCLUDED.recommended_entry_high,
			recommended_stop     = EXCLUDED.recommended_stop,
			recommended_target_1 = EXCLUDED.recommended_target_1,
			recommended_target_2 = EXCLUDED.recommended_target_2,
			recommended_rr       = EXCLUDED.recommended_rr,
			recommended_size     = EXCLUDED.recommended_size,
			recommended_conviction = EXCLUDED.recommended_conviction,
			actual_entry_price   = EXCLUDED.actual_entry_price,
			actual_return_5d     = EXCLUDED.actual_return_5d,
			actual_return_10d    = EXCLUDED.actual_return_10d,
			actual_return_20d    = EXCLUDED.actual_return_20d,
			actual_max_runup     = EXCLUDED.actual_max_runup,
			actual_max_drawdown  = EXCLUDED.actual_max_drawdown,
			stop_hit             = EXCLUDED.stop_hit,
			target_1_hit         = EXCLUDED.target_1_hit,
			target_2_hit         = EXCLUDED.target_2_hit,
			actual_rr_achieved   = EXCLUDED.actual_rr_achieved,
			evaluated_days       = EXCLUDED.evaluated_days,
			disqualified         = EXCLUDED.disqualified,
			disqualifier_reason  = EXCLUDED.disqualifier_reason,
			ml_feature_name          = EXCLUDED.ml_feature_name,
			ml_weight_delta          = EXCLUDED.ml_weight_delta,
			ml_suggestion_confidence = EXCLUDED.ml_suggestion_confidence,
			exit_type            = EXCLUDED.exit_type,
			exit_price           = EXCLUDED.exit_price,
			exit_date            = EXCLUDED.exit_date,
			t1_hit               = EXCLUDED.t1_hit,
			levels_invalid       = EXCLUDED.levels_invalid,
			updated_at           = NOW()
	`,
		m.Date, string(m.ListType), m.Ticker, m.PromptVersion, m.LLMRecommended,
		m.RecommendedSetup, m.RecommendedEntryLow, m.RecommendedEntryHigh,
		m.RecommendedStop, m.RecommendedTarget1, m.RecommendedTarget2,
		m.RecommendedRR, m.RecommendedSize, m.RecommendedConviction,
		m.ActualEntryPrice, m.ActualReturn5D, m.ActualReturn10D, m.ActualReturn20D,
		m.ActualMaxRunup, m.ActualMaxDrawdown,
		m.StopHit, m.Target1Hit, m.Target2Hit, m.ActualRRAchieved,
		m.EvaluatedDays,
		m.Disqualified, m.DisqualifierReason,
		m.MLFeatureName, m.MLWeightDelta, m.MLSuggestionConfidence,
		m.ExitType, m.ExitPrice, m.ExitDate, m.T1Hit, m.LevelsInvalid,
	)

	if err != nil {
		return fmt.Errorf("UpsertOutcome %s %s %s: %w",
			m.Date.Format("2006-01-02"), m.ListType, m.Ticker, err)
	}
	return nil
}

// GetPendingDates returns dates where evaluated_days < 20.
func (r *PromptTickerOutcomeRepo) GetPendingDates(ctx context.Context, lookbackDays int) ([]time.Time, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT date FROM prompt_ticker_outcomes
		WHERE evaluated_days < 20
		  AND date >= CURRENT_DATE - $1::int
		ORDER BY date ASC
	`, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("GetPendingDates: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("GetPendingDates scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetByVersion returns all outcomes for a given prompt version.
func (r *PromptTickerOutcomeRepo) GetByVersion(ctx context.Context, promptVersion string) ([]models.PromptTickerOutcome, error) {
	return r.queryOutcomes(ctx, `
		SELECT * FROM prompt_ticker_outcomes WHERE prompt_version = $1 ORDER BY date, ticker
	`, promptVersion)
}

// GetByDateRange returns all outcomes in the given date range.
func (r *PromptTickerOutcomeRepo) GetByDateRange(ctx context.Context, from, to time.Time) ([]models.PromptTickerOutcome, error) {
	return r.queryOutcomes(ctx, `
		SELECT * FROM prompt_ticker_outcomes WHERE date >= $1 AND date <= $2 ORDER BY date, ticker
	`, from, to)
}

// VersionPerformanceSummary holds aggregated metrics for a prompt version.
type VersionPerformanceSummary struct {
	PromptVersion    string
	TotalPicks       int
	WinRate5D        float64
	WinRate10D       float64
	AvgReturn5D      float64
	AvgReturn10D     float64
	StopHitRate      float64
	Target1HitRate   float64
	AvgRRAchieved    float64
	AvgRecommendedRR float64
}

// GetVersionSummary returns aggregated metrics for a prompt version.
func (r *PromptTickerOutcomeRepo) GetVersionSummary(ctx context.Context, promptVersion string) (*VersionPerformanceSummary, error) {
	row := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) as total,
			AVG(CASE WHEN actual_return_5d > 0 THEN 1.0 ELSE 0.0 END) as win_rate_5d,
			AVG(CASE WHEN actual_return_10d > 0 THEN 1.0 ELSE 0.0 END) as win_rate_10d,
			COALESCE(AVG(actual_return_5d), 0) as avg_return_5d,
			COALESCE(AVG(actual_return_10d), 0) as avg_return_10d,
			AVG(CASE WHEN stop_hit = true THEN 1.0 ELSE 0.0 END) as stop_hit_rate,
			AVG(CASE WHEN target_1_hit = true THEN 1.0 ELSE 0.0 END) as target_1_hit_rate,
			COALESCE(AVG(actual_rr_achieved), 0) as avg_rr_achieved,
			COALESCE(AVG(recommended_rr), 0) as avg_recommended_rr
		FROM prompt_ticker_outcomes
		WHERE prompt_version = $1
		  AND llm_recommended = true
		  AND evaluated_days >= 5
	`, promptVersion)

	s := &VersionPerformanceSummary{PromptVersion: promptVersion}
	var winRate5D, winRate10D, stopHitRate, target1HitRate *float64
	if err := row.Scan(
		&s.TotalPicks, &winRate5D, &winRate10D,
		&s.AvgReturn5D, &s.AvgReturn10D,
		&stopHitRate, &target1HitRate,
		&s.AvgRRAchieved, &s.AvgRecommendedRR,
	); err != nil {
		if isNoRows(err) {
			return s, nil
		}
		return nil, fmt.Errorf("GetVersionSummary: %w", err)
	}
	if winRate5D != nil {
		s.WinRate5D = *winRate5D
	}
	if winRate10D != nil {
		s.WinRate10D = *winRate10D
	}
	if stopHitRate != nil {
		s.StopHitRate = *stopHitRate
	}
	if target1HitRate != nil {
		s.Target1HitRate = *target1HitRate
	}
	return s, nil
}

// LLMValueComparison compares performance of tickers the LLM recommended vs rejected.
type LLMValueComparison struct {
	RecommendedCount     int
	RecommendedWinRate5D float64
	RecommendedAvgReturn float64
	RejectedCount        int
	RejectedWinRate5D    float64
	RejectedAvgReturn    float64
	LLMAlpha             float64
}

// GetLLMValueComparison returns recommended vs rejected ticker performance.
func (r *PromptTickerOutcomeRepo) GetLLMValueComparison(ctx context.Context) (*LLMValueComparison, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			llm_recommended,
			COUNT(*) as n,
			AVG(CASE WHEN actual_return_5d > 0 THEN 1.0 ELSE 0.0 END) as win_rate_5d,
			COALESCE(AVG(actual_return_5d), 0) as avg_return_5d
		FROM prompt_ticker_outcomes
		WHERE evaluated_days >= 5
		GROUP BY llm_recommended
	`)
	if err != nil {
		return nil, fmt.Errorf("GetLLMValueComparison: %w", err)
	}
	defer rows.Close()

	c := &LLMValueComparison{}
	for rows.Next() {
		var recommended bool
		var n int
		var winRate, avgReturn *float64
		if err := rows.Scan(&recommended, &n, &winRate, &avgReturn); err != nil {
			return nil, fmt.Errorf("GetLLMValueComparison scan: %w", err)
		}
		if recommended {
			c.RecommendedCount = n
			if winRate != nil {
				c.RecommendedWinRate5D = *winRate
			}
			if avgReturn != nil {
				c.RecommendedAvgReturn = *avgReturn
			}
		} else {
			c.RejectedCount = n
			if winRate != nil {
				c.RejectedWinRate5D = *winRate
			}
			if avgReturn != nil {
				c.RejectedAvgReturn = *avgReturn
			}
		}
	}
	c.LLMAlpha = c.RecommendedAvgReturn - c.RejectedAvgReturn
	return c, rows.Err()
}

// queryOutcomes is a helper that scans prompt_ticker_outcomes rows.
func (r *PromptTickerOutcomeRepo) queryOutcomes(ctx context.Context, sql string, args ...any) ([]models.PromptTickerOutcome, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.PromptTickerOutcome
	for rows.Next() {
		var m models.PromptTickerOutcome
		var lt string
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Ticker, &m.PromptVersion, &m.LLMRecommended,
			&m.RecommendedSetup, &m.RecommendedEntryLow, &m.RecommendedEntryHigh,
			&m.RecommendedStop, &m.RecommendedTarget1, &m.RecommendedTarget2,
			&m.RecommendedRR, &m.RecommendedSize, &m.RecommendedConviction,
			&m.ActualEntryPrice, &m.ActualReturn5D, &m.ActualReturn10D, &m.ActualReturn20D,
			&m.ActualMaxRunup, &m.ActualMaxDrawdown,
			&m.StopHit, &m.Target1Hit, &m.Target2Hit, &m.ActualRRAchieved,
			&m.EvaluatedDays,
			&m.CreatedAt, &m.UpdatedAt,
			&m.Disqualified, &m.DisqualifierReason,
			&m.MLFeatureName, &m.MLWeightDelta, &m.MLSuggestionConfidence,
			&m.ExitType, &m.ExitPrice, &m.ExitDate, &m.T1Hit, &m.LevelsInvalid,
		); err != nil {

			return nil, err
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MLWeightDeltaSummary holds aggregated LLM-suggested weight deltas per feature.
type MLWeightDeltaSummary struct {
	FeatureName string
	AvgDelta    float64
	Count       int
}

// GetMLWeightDeltas returns aggregated LLM-suggested weight deltas for the last N days.
// Used by NightlyWeightRefitJob for reconciliation against statistical deltas.
func (r *PromptTickerOutcomeRepo) GetMLWeightDeltas(ctx context.Context, lookbackDays int) ([]MLWeightDeltaSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			ml_feature_name,
			AVG(ml_weight_delta) as avg_delta,
			COUNT(*) as cnt
		FROM prompt_ticker_outcomes
		WHERE ml_feature_name IS NOT NULL
		  AND date >= CURRENT_DATE - $1::int
		GROUP BY ml_feature_name
		ORDER BY ml_feature_name
	`, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("GetMLWeightDeltas: %w", err)
	}
	defer rows.Close()

	var out []MLWeightDeltaSummary
	for rows.Next() {
		var s MLWeightDeltaSummary
		if err := rows.Scan(&s.FeatureName, &s.AvgDelta, &s.Count); err != nil {
			return nil, fmt.Errorf("GetMLWeightDeltas scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// QueryBackfillCandidates returns recommended trades with stop prices and entry dates
// within the lookback window that have not yet been processed by the path-aware exit simulation.
func (r *PromptTickerOutcomeRepo) QueryBackfillCandidates(ctx context.Context, lookbackDays int) ([]models.PromptTickerOutcome, error) {
	return r.queryOutcomes(ctx, `
		SELECT * FROM prompt_ticker_outcomes
		WHERE llm_recommended = true
		  AND recommended_stop IS NOT NULL
		  AND date >= CURRENT_DATE - $1::int
		  AND exit_type IS NULL
		ORDER BY date ASC, ticker ASC
	`, lookbackDays)
}

// ── Matched-Pair Analysis (STORY-R05) ──────────────────────────────────────────

// GetMatchedPairs retrieves recommended trades and their candidate controls
// for matched-pair analysis.
//
// Recommended: llm_recommended=true, is_primary_observation=true, evaluated_days>=20
// Controls: llm_recommended=false, is_primary_observation=true, evaluated_days>=20
// Both filtered to: list_type IN (?), entry_date BETWEEN ? AND ?
func (r *PromptTickerOutcomeRepo) GetMatchedPairs(
	ctx context.Context,
	listTypes []string,
	startDate, endDate time.Time,
) (*MatchedPairData, error) {
	// Build IN clause placeholders
	placeholders := make([]string, len(listTypes))
	args := make([]any, 0, 4+len(listTypes))
	for i, lt := range listTypes {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, lt)
	}
	args = append(args, startDate, endDate)

	inClause := strings.Join(placeholders, ", ")
	nextArg := len(listTypes) + 1

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			pto.date AS entry_date,
			pto.list_type,
			pto.ticker,
			COALESCE(t.sector, '') AS sector,
			drl.final_score,
			pto.recommended_stop AS stop_price,
			COALESCE(tod.entry_price, 0) AS entry_price,
			pto.recommended_target_1 AS target_1,
			pto.recommended_target_2 AS target_2,
			pto.exit_type,
			pto.exit_price,
			COALESCE(tod.return_5d, 0) AS return_5d,
			COALESCE(tod.return_10d, 0) AS return_10d,
			COALESCE(tod.return_20d, 0) AS return_20d,
			COALESCE(tod.max_runup_20d, 0) AS max_runup_20d,
			COALESCE(tod.max_drawdown_20d, 0) AS max_drawdown_20d,
			pto.actual_rr_achieved,
			tod.cluster_id
		FROM prompt_ticker_outcomes pto
		JOIN trade_outcomes_daily tod
			ON tod.entry_date = pto.date
			AND tod.list_type = pto.list_type
			AND tod.ticker = pto.ticker
		LEFT JOIN tickers t ON t.ticker = pto.ticker
		LEFT JOIN daily_rank_lists drl
			ON drl.entry_date = pto.date
			AND drl.list_type = pto.list_type
			AND drl.ticker = pto.ticker
		WHERE pto.list_type IN (%s)
		  AND pto.date BETWEEN $%d AND $%d
		  AND pto.is_primary_observation = true
		  AND pto.evaluated_days >= 20
		  AND pto.llm_recommended = true
		ORDER BY pto.date ASC, pto.ticker ASC
	`, inClause, nextArg, nextArg+1), args...)
	if err != nil {
		return nil, fmt.Errorf("GetMatchedPairs recommended: %w", err)
	}
	defer rows.Close()

	recommended, err := scanMatchedPairRows(rows)
	if err != nil {
		return nil, fmt.Errorf("GetMatchedPairs scan recommended: %w", err)
	}

	// Controls query (llm_recommended = false)
	ctrlArgs := make([]any, 0, 4+len(listTypes))
	for _, lt := range listTypes {
		ctrlArgs = append(ctrlArgs, lt)
	}
	ctrlArgs = append(ctrlArgs, startDate, endDate)

	ctrlRows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			pto.date AS entry_date,
			pto.list_type,
			pto.ticker,
			COALESCE(t.sector, '') AS sector,
			drl.final_score,
			NULL AS stop_price,
			COALESCE(tod.entry_price, 0) AS entry_price,
			NULL AS target_1,
			NULL AS target_2,
			pto.exit_type,
			pto.exit_price,
			COALESCE(tod.return_5d, 0) AS return_5d,
			COALESCE(tod.return_10d, 0) AS return_10d,
			COALESCE(tod.return_20d, 0) AS return_20d,
			COALESCE(tod.max_runup_20d, 0) AS max_runup_20d,
			COALESCE(tod.max_drawdown_20d, 0) AS max_drawdown_20d,
			pto.actual_rr_achieved,
			tod.cluster_id
		FROM prompt_ticker_outcomes pto
		JOIN trade_outcomes_daily tod
			ON tod.entry_date = pto.date
			AND tod.list_type = pto.list_type
			AND tod.ticker = pto.ticker
		LEFT JOIN tickers t ON t.ticker = pto.ticker
		LEFT JOIN daily_rank_lists drl
			ON drl.entry_date = pto.date
			AND drl.list_type = pto.list_type
			AND drl.ticker = pto.ticker
		WHERE pto.list_type IN (%s)
		  AND pto.date BETWEEN $%d AND $%d
		  AND pto.is_primary_observation = true
		  AND pto.evaluated_days >= 20
		  AND pto.llm_recommended = false
		ORDER BY pto.date ASC, pto.ticker ASC
	`, inClause, nextArg, nextArg+1), ctrlArgs...)
	if err != nil {
		return nil, fmt.Errorf("GetMatchedPairs controls: %w", err)
	}
	defer ctrlRows.Close()

	controls, err := scanMatchedPairRows(ctrlRows)
	if err != nil {
		return nil, fmt.Errorf("GetMatchedPairs scan controls: %w", err)
	}

	return &MatchedPairData{
		Recommended: recommended,
		Controls:    controls,
	}, nil
}

// scanMatchedPairRows scans rows into MatchedPairRow slice.
func scanMatchedPairRows(rows pgx.Rows) ([]MatchedPairRow, error) {
	var out []MatchedPairRow
	for rows.Next() {
		var row MatchedPairRow
		if err := rows.Scan(
			&row.EntryDate,
			&row.ListType,
			&row.Ticker,
			&row.Sector,
			&row.FinalScore,
			&row.StopPrice,
			&row.EntryPrice,
			&row.Target1,
			&row.Target2,
			&row.ExitType,
			&row.ExitPrice,
			&row.Return5d,
			&row.Return10d,
			&row.Return20d,
			&row.MaxRunup20d,
			&row.MaxDrawdown20d,
			&row.ActualRR,
			&row.ClusterID,
		); err != nil {
			return nil, fmt.Errorf("scanMatchedPairRow: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
