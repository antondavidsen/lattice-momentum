package models

import (
	"time"

	"github.com/google/uuid"
)

// ── SurgeScoreWeights ─────────────────────────────────────────────────────────

// SurgeScoreWeights represents one row in the surge_score_weights table.
type SurgeScoreWeights struct {
	ID              uuid.UUID  `db:"id"`
	Version         int        `db:"version"`
	ComputedDate    time.Time  `db:"computed_date"`
	WGap            float64    `db:"w_gap"`
	WVolume         float64    `db:"w_volume"`
	WCatalyst       float64    `db:"w_catalyst"`
	WFloat          float64    `db:"w_float"`
	WSector         float64    `db:"w_sector"`
	WSimilarity     float64    `db:"w_similarity"`
	WChart          float64    `db:"w_chart"`
	Method          string     `db:"method"`
	SampleSize      int        `db:"sample_size"`
	TrainingWinRate *float64   `db:"training_win_rate"`
	TrainingAUC     *float64   `db:"training_auc"`
	TrainStart      *time.Time `db:"train_start"`
	TrainEnd        *time.Time `db:"train_end"`
	Active          bool       `db:"active"`
	CreatedAt       time.Time  `db:"created_at"`
}

// ── PerformanceWindow ─────────────────────────────────────────────────────────

// PerformanceWindow represents one row in the performance_windows table.
// Fields added by migration 056: regime_label, consecutive_decay/recovery_windows,
// net_return_5d/10d/20d, gross_return_5d/10d/20d, is_evaluable, exclusion_reason,
// total_outcomes, win_rate_5d/10d/20d.
// Field added by migration 065: regime_bucket for regime-segmented reporting.
type PerformanceWindow struct {
	ID                         uuid.UUID `db:"id"`
	PipelineType               string    `db:"pipeline_type"`
	WindowDate                 time.Time `db:"window_date"`
	WindowSize                 int       `db:"window_size"`
	WinRate                    float64   `db:"win_rate"`
	MedianReturn               float64   `db:"median_return"`
	MeanReturn                 float64   `db:"mean_return"`
	HitRate15Pct               float64   `db:"hit_rate_15pct"`
	AvgCatalystScore           float64   `db:"avg_catalyst_score"`
	BaselineWinRate            float64   `db:"baseline_win_rate"`
	BaselineMedian             float64   `db:"baseline_median"`
	DecayPct                   float64   `db:"decay_pct"`
	AlertTriggered             bool      `db:"alert_triggered"`
	CreatedAt                  time.Time `db:"created_at"`
	RegimeLabel                *string   `db:"regime_label"`
	RegimeBucket               *string   `db:"regime_bucket"`
	ConsecutiveDecayWindows    int       `db:"consecutive_decay_windows"`
	ConsecutiveRecoveryWindows int       `db:"consecutive_recovery_windows"`
	NetReturn5d                *float64  `db:"net_return_5d"`
	NetReturn10d               *float64  `db:"net_return_10d"`
	NetReturn20d               *float64  `db:"net_return_20d"`
	GrossReturn5d              *float64  `db:"gross_return_5d"`
	GrossReturn10d             *float64  `db:"gross_return_10d"`
	GrossReturn20d             *float64  `db:"gross_return_20d"`
	IsEvaluable                bool      `db:"is_evaluable"`
	ExclusionReason            *string   `db:"exclusion_reason"`
	TotalOutcomes              int       `db:"total_outcomes"`
	WinRate5d                  *float64  `db:"win_rate_5d"`
	WinRate10d                 *float64  `db:"win_rate_10d"`
	WinRate20d                 *float64  `db:"win_rate_20d"`
}

// ── PromptDegradationAlert ────────────────────────────────────────────────────

// PromptDegradationAlert represents one row in the prompt_degradation_alerts table.
type PromptDegradationAlert struct {
	ID               uuid.UUID  `db:"id"`
	PromptVersion    string     `db:"prompt_version"`
	CatalystCategory string     `db:"catalyst_category"`
	PearsonR         float64    `db:"pearson_r"`
	ConsecutiveWeeks int        `db:"consecutive_weeks"`
	AlertTriggered   bool       `db:"alert_triggered"`
	CreatedAt        time.Time  `db:"created_at"`
	ResolvedAt       *time.Time `db:"resolved_at"`
	ResolutionNotes  *string    `db:"resolution_notes"`
}

// PromptExperimentResult represents one row in the prompt_experiment_results table.
type PromptExperimentResult struct {
	ID                    uuid.UUID `db:"id"`
	PromptVersion         string    `db:"prompt_version"`
	PipelineType          string    `db:"pipeline_type"`
	EvaluationDate        time.Time `db:"evaluation_date"`
	TotalPicks            int       `db:"total_picks"`
	AvgIntradayReturn     *float64  `db:"avg_intraday_return"`
	MedianIntradayReturn  *float64  `db:"median_intraday_return"`
	WinRate               *float64  `db:"win_rate"`
	AvgLLMConfidence      *float64  `db:"avg_llm_confidence"`
	ConfidenceCalibration *float64  `db:"confidence_calibration"`
	TradeStartDate        time.Time `db:"trade_start_date"`
	TradeEndDate          time.Time `db:"trade_end_date"`
	CreatedAt             time.Time `db:"created_at"`
}
