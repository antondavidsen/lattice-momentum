package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// PromptVariantRepo handles persistence for the prompt_variants table.
type PromptVariantRepo struct {
	db dbPool
}

// NewPromptVariantRepo creates a new repo backed by a live pool.
func NewPromptVariantRepo(db *pgxpool.Pool) *PromptVariantRepo {
	return &PromptVariantRepo{db: db}
}

// GetActiveVariants returns all active variants for a list type.
func (r *PromptVariantRepo) GetActiveVariants(ctx context.Context, listType models.ListType) ([]models.PromptVariant, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, list_type, variant_name, is_primary, is_active,
		       system_prompt, user_template, prompt_version,
		       consecutive_outperform_weeks, created_at, notes
		FROM   prompt_variants
		WHERE  list_type = $1 AND is_active = true
		ORDER  BY is_primary DESC, variant_name
	`, string(listType))
	if err != nil {
		return nil, fmt.Errorf("GetActiveVariants %s: %w", listType, err)
	}
	defer rows.Close()

	var out []models.PromptVariant
	for rows.Next() {
		var m models.PromptVariant
		var lt string
		if err := rows.Scan(
			&m.ID, &lt, &m.VariantName, &m.IsPrimary, &m.IsActive,
			&m.SystemPrompt, &m.UserTemplate, &m.PromptVersion,
			&m.ConsecutiveOutperformWeeks, &m.CreatedAt, &m.Notes,
		); err != nil {
			return nil, fmt.Errorf("GetActiveVariants scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.VariantName = strings.Clone(m.VariantName)
		m.SystemPrompt = strings.Clone(m.SystemPrompt)
		m.UserTemplate = strings.Clone(m.UserTemplate)
		m.PromptVersion = strings.Clone(m.PromptVersion)
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertVariant inserts or updates a prompt variant.
// Idempotent on (list_type, variant_name).
func (r *PromptVariantRepo) UpsertVariant(ctx context.Context, m *models.PromptVariant) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO prompt_variants (
			list_type, variant_name, is_primary, is_active,
			system_prompt, user_template, prompt_version,
			consecutive_outperform_weeks, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (list_type, variant_name) DO UPDATE SET
			is_primary       = EXCLUDED.is_primary,
			is_active        = EXCLUDED.is_active,
			system_prompt    = EXCLUDED.system_prompt,
			user_template    = EXCLUDED.user_template,
			prompt_version   = EXCLUDED.prompt_version,
			consecutive_outperform_weeks = EXCLUDED.consecutive_outperform_weeks,
			notes            = EXCLUDED.notes
	`,
		string(m.ListType), m.VariantName, m.IsPrimary, m.IsActive,
		m.SystemPrompt, m.UserTemplate, m.PromptVersion,
		m.ConsecutiveOutperformWeeks, m.Notes,
	)
	if err != nil {
		return fmt.Errorf("UpsertVariant %s %s: %w", m.ListType, m.VariantName, err)
	}
	return nil
}

// DeactivateVariant sets is_active=false for a variant.
func (r *PromptVariantRepo) DeactivateVariant(ctx context.Context, listType models.ListType, variantName string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE prompt_variants SET is_active = false
		WHERE list_type = $1 AND variant_name = $2
	`, string(listType), variantName)
	if err != nil {
		return fmt.Errorf("DeactivateVariant %s %s: %w", listType, variantName, err)
	}
	return nil
}
