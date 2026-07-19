package models

import (
	"time"

	"github.com/google/uuid"
)

// PromptVariant represents a versioned prompt template for A/B testing.
type PromptVariant struct {
	ID                         uuid.UUID `db:"id"`
	ListType                   ListType  `db:"list_type"`
	VariantName                string    `db:"variant_name"`
	IsPrimary                  bool      `db:"is_primary"`
	IsActive                   bool      `db:"is_active"`
	SystemPrompt               string    `db:"system_prompt"`
	UserTemplate               string    `db:"user_template"`
	PromptVersion              string    `db:"prompt_version"`
	ConsecutiveOutperformWeeks int       `db:"consecutive_outperform_weeks"`
	CreatedAt                  time.Time `db:"created_at"`
	Notes                      *string   `db:"notes"`
}
