package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// NightlyRun represents one row in the model-log response — an audit record
// for a single pipeline execution, enriched with regime and VIX data.
type NightlyRun struct {
	RunDate        time.Time `json:"run_date"`
	GateLevel      string    `json:"gate_level"`
	Status         string    `json:"status"`
	StepsCompleted int       `json:"steps_completed"`
	DurationMs     *int64    `json:"duration_ms"`
	RegimeLabel    string    `json:"regime_label"`
	VIXLevel       float64   `json:"vix_level"`
}

// NightlyRunRepo handles persistence for the nightly_runs table.
type NightlyRunRepo struct {
	db dbPool
}

// NewNightlyRunRepo creates a new NightlyRunRepo backed by a live pool.
func NewNightlyRunRepo(db *pgxpool.Pool) *NightlyRunRepo {
	return &NightlyRunRepo{db: db}
}

// Insert creates a new nightly_runs row (status = "running") and returns its ID.
// gateLevel is optional; pass "" to omit.
func (r *NightlyRunRepo) Insert(ctx context.Context, runDate time.Time, stepsTotal int, gateLevel string) (uuid.UUID, error) {
	id := uuid.New()
	var gateLevelArg any
	if gateLevel == "" {
		gateLevelArg = nil
	} else {
		gateLevelArg = gateLevel
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO nightly_runs (id, run_date, status, started_at, steps_total, step_durations, gate_level)
		VALUES ($1, $2, 'running', NOW(), $3, '{}', $4)
	`, id, runDate, stepsTotal, gateLevelArg)
	return id, err
}

// MarkCompleted finalises a successful run.
func (r *NightlyRunRepo) MarkCompleted(ctx context.Context, id uuid.UUID, stepsCompleted int, stepDurations map[string]int64) error {
	durJSON, _ := json.Marshal(stepDurations)
	_, err := r.db.Exec(ctx, `
		UPDATE nightly_runs
		SET status          = 'completed',
		    finished_at     = NOW(),
		    duration_ms     = EXTRACT(EPOCH FROM (NOW() - started_at))::BIGINT * 1000,
		    steps_completed = $2,
		    step_durations  = $3
		WHERE id = $1
	`, id, stepsCompleted, string(durJSON))
	return err
}

// MarkFailed finalises a failed run.
func (r *NightlyRunRepo) MarkFailed(ctx context.Context, id uuid.UUID, stepsCompleted int, failedStep, errMsg string, stepDurations map[string]int64) error {
	durJSON, _ := json.Marshal(stepDurations)
	_, err := r.db.Exec(ctx, `
		UPDATE nightly_runs
		SET status          = 'failed',
		    finished_at     = NOW(),
		    duration_ms     = EXTRACT(EPOCH FROM (NOW() - started_at))::BIGINT * 1000,
		    steps_completed = $2,
		    failed_step     = $3,
		    error_message   = $4,
		    step_durations  = $5
		WHERE id = $1
	`, id, stepsCompleted, failedStep, errMsg, string(durJSON))
	return err
}

// GetModelLog returns the most recent nightly runs (up to limit) enriched with
// regime label and VIX level from the market_regime_daily and market_inputs_daily
// tables.  Used by the /api/v1/reports/model-log endpoint to surface gate level
// history for operators.
func (r *NightlyRunRepo) GetModelLog(ctx context.Context, limit int) ([]NightlyRun, error) {
	query := `
		SELECT
			nr.run_date,
			COALESCE(nr.gate_level, '') AS gate_level,
			nr.status,
			nr.steps_completed,
			nr.duration_ms,
			COALESCE(mrd.regime, 'unknown') AS regime_label,
			COALESCE(mid.vix_level, 0) AS vix_level
		FROM nightly_runs nr
		LEFT JOIN market_regime_daily mrd ON mrd.date = nr.run_date
		LEFT JOIN market_inputs_daily mid ON mid.date = nr.run_date
		ORDER BY nr.run_date DESC
		LIMIT $1
	`
	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("GetModelLog: %w", err)
	}
	defer rows.Close()

	var runs []NightlyRun
	for rows.Next() {
		var run NightlyRun
		if err := rows.Scan(
			&run.RunDate,
			&run.GateLevel,
			&run.Status,
			&run.StepsCompleted,
			&run.DurationMs,
			&run.RegimeLabel,
			&run.VIXLevel,
		); err != nil {
			return nil, fmt.Errorf("GetModelLog scan: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// GetLatest returns the most recent nightly run.
func (r *NightlyRunRepo) GetLatest(ctx context.Context) (*models.NightlyRun, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_date, status, started_at, finished_at, duration_ms,
		       steps_total, steps_completed, failed_step, error_message,
		       step_durations, created_at
		FROM nightly_runs
		ORDER BY started_at DESC
		LIMIT 1
	`)
	var nr models.NightlyRun
	var stepDurations []byte
	err := row.Scan(
		&nr.ID, &nr.RunDate, &nr.Status, &nr.StartedAt, &nr.FinishedAt,
		&nr.DurationMs, &nr.StepsTotal, &nr.StepsCompleted, &nr.FailedStep,
		&nr.ErrorMessage, &stepDurations, &nr.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(stepDurations) > 0 {
		nr.StepDurations = append([]byte(nil), stepDurations...)
	}
	return &nr, nil
}
