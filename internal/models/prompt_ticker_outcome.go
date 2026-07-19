package models

import (
	"time"

	"github.com/google/uuid"
)

// MLFeatureUpdate holds an LLM-suggested weight adjustment for a scoring feature.
// Populated from the ml_feature_update field in the outcome analyzer prompt response.
type MLFeatureUpdate struct {
	FeatureName          string  `json:"feature_name"`
	SuggestedWeightDelta float64 `json:"suggested_weight_delta"`
	Confidence           float64 `json:"confidence"`
}

// PromptTickerOutcome joins a parsed LLM recommendation with actual forward returns.
// When LLMRecommended is false, the ticker was in the input but rejected by the LLM —
// the Recommended* fields will be nil, but Actual* fields are populated from trade_outcomes_daily.
type PromptTickerOutcome struct {
	ID             uuid.UUID `db:"id"`
	Date           time.Time `db:"date"`
	ListType       ListType  `db:"list_type"`
	Ticker         string    `db:"ticker"`
	PromptVersion  string    `db:"prompt_version"`
	LLMRecommended bool      `db:"llm_recommended"`

	// Parsed LLM recommendation (nil when LLMRecommended = false)
	RecommendedSetup      *string  `db:"recommended_setup"`
	RecommendedEntryLow   *float64 `db:"recommended_entry_low"`
	RecommendedEntryHigh  *float64 `db:"recommended_entry_high"`
	RecommendedStop       *float64 `db:"recommended_stop"`
	RecommendedTarget1    *float64 `db:"recommended_target_1"`
	RecommendedTarget2    *float64 `db:"recommended_target_2"`
	RecommendedRR         *float64 `db:"recommended_rr"`
	RecommendedSize       *string  `db:"recommended_size"`
	RecommendedConviction *string  `db:"recommended_conviction"`

	// Actual outcomes
	ActualEntryPrice  *float64 `db:"actual_entry_price"`
	ActualReturn5D    *float64 `db:"actual_return_5d"`
	ActualReturn10D   *float64 `db:"actual_return_10d"`
	ActualReturn20D   *float64 `db:"actual_return_20d"`
	ActualMaxRunup    *float64 `db:"actual_max_runup"`
	ActualMaxDrawdown *float64 `db:"actual_max_drawdown"`

	// Derived — old path-blind flags (preserved as read-only history)
	StopHit          *bool    `db:"stop_hit"`
	Target1Hit       *bool    `db:"target_1_hit"`
	Target2Hit       *bool    `db:"target_2_hit"`
	ActualRRAchieved *float64 `db:"actual_rr_achieved"`

	// Path-aware sequenced exit tracking (R03)
	ExitType      *string    `db:"exit_type"` // "stop", "target_2", "time"
	ExitPrice     *float64   `db:"exit_price"`
	ExitDate      *time.Time `db:"exit_date"`
	T1Hit         bool       `db:"t1_hit"`         // T1 touched before final exit (informational)
	LevelsInvalid bool       `db:"levels_invalid"` // stop/entry geometry or R/R floor violated

	EvaluatedDays int `db:"evaluated_days"`

	// Disqualifier fields (R-06)
	Disqualified       bool    `db:"disqualified"`
	DisqualifierReason *string `db:"disqualifier_reason"`

	// ML feature update fields (self-improvement layer)
	MLFeatureName          *string  `db:"ml_feature_name"`
	MLWeightDelta          *float64 `db:"ml_weight_delta"`
	MLSuggestionConfidence *float64 `db:"ml_suggestion_confidence"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
