package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// PromptMemory stores a past LLM evaluation with its verified outcome for retrieval.
type PromptMemory struct {
	ID             uuid.UUID       `db:"id"`
	Date           time.Time       `db:"date"`
	ListType       ListType        `db:"list_type"`
	Ticker         string          `db:"ticker"`
	PromptVersion  string          `db:"prompt_version"`
	ContextSummary string          `db:"context_summary"`
	Embedding      pgvector.Vector `db:"embedding"`

	LLMSetup      *string `db:"llm_setup"`
	LLMConviction *string `db:"llm_conviction"`
	LLMEntry      *string `db:"llm_entry"`
	LLMStop       *string `db:"llm_stop"`

	OutcomeStatus    string   `db:"outcome_status"` // pending | verified | insufficient_data
	OutcomeReturn5D  *float64 `db:"outcome_return_5d"`
	OutcomeStopHit   *bool    `db:"outcome_stop_hit"`
	OutcomeTargetHit *bool    `db:"outcome_target_hit"`
	OutcomeSummary   *string  `db:"outcome_summary"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
