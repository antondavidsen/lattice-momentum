package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// LLMListEvaluation is one row in the llm_list_evaluations table.
// It stores the full LLM output for a single list evaluation run.
type LLMListEvaluation struct {
	ID            uuid.UUID       `db:"id"`
	Date          time.Time       `db:"date"`
	ListType      ListType        `db:"list_type"`
	Provider      string          `db:"provider"`
	Model         string          `db:"model"`
	PromptVersion string          `db:"prompt_version"`
	SystemPrompt  string          `db:"system_prompt"`
	UserPrompt    string          `db:"user_prompt"`
	RawResponse   string          `db:"raw_response"`
	ParsedJSON    json.RawMessage `db:"parsed_json"`
	InputTickers  []string        `db:"input_tickers"`
	OutputTickers []string        `db:"output_tickers"`
	VariantName   string          `db:"variant_name"`
	InputTokens   *int            `db:"input_tokens"`
	OutputTokens  *int            `db:"output_tokens"`
	DurationMs    *int            `db:"duration_ms"`
	CreatedAt     time.Time       `db:"created_at"`
}
