package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// NightlyRun represents one row in the nightly_runs table — an audit record
// for a single pipeline execution.
type NightlyRun struct {
	ID             uuid.UUID       `db:"id"`
	RunDate        time.Time       `db:"run_date"`
	Status         string          `db:"status"` // "running", "completed", "failed"
	StartedAt      time.Time       `db:"started_at"`
	FinishedAt     *time.Time      `db:"finished_at"`
	DurationMs     *int64          `db:"duration_ms"`
	StepsTotal     int             `db:"steps_total"`
	StepsCompleted int             `db:"steps_completed"`
	FailedStep     *string         `db:"failed_step"`
	ErrorMessage   *string         `db:"error_message"`
	StepDurations  json.RawMessage `db:"step_durations"`
	GateLevel      *string         `db:"gate_level"` // "full" | "ep_only" | "halt" (R-09)
	CreatedAt      time.Time       `db:"created_at"`
}
