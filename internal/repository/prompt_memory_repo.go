package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"ai-stock-service/internal/models"
)

// PromptMemoryRepo handles persistence for the prompt_memory table.
type PromptMemoryRepo struct {
	db dbPool
}

// NewPromptMemoryRepo creates a new repo backed by a live pool.
func NewPromptMemoryRepo(db *pgxpool.Pool) *PromptMemoryRepo {
	return &PromptMemoryRepo{db: db}
}

// UpsertMemory inserts or updates a prompt memory row.
func (r *PromptMemoryRepo) UpsertMemory(ctx context.Context, m *models.PromptMemory) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO prompt_memory (
			date, list_type, ticker, prompt_version,
			context_summary, embedding,
			llm_setup, llm_conviction, llm_entry, llm_stop,
			outcome_status, outcome_return_5d, outcome_stop_hit,
			outcome_target_hit, outcome_summary
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (date, list_type, ticker) DO UPDATE SET
			prompt_version   = EXCLUDED.prompt_version,
			context_summary  = EXCLUDED.context_summary,
			embedding        = EXCLUDED.embedding,
			llm_setup        = EXCLUDED.llm_setup,
			llm_conviction   = EXCLUDED.llm_conviction,
			llm_entry        = EXCLUDED.llm_entry,
			llm_stop         = EXCLUDED.llm_stop,
			outcome_status   = EXCLUDED.outcome_status,
			outcome_return_5d = EXCLUDED.outcome_return_5d,
			outcome_stop_hit  = EXCLUDED.outcome_stop_hit,
			outcome_target_hit= EXCLUDED.outcome_target_hit,
			outcome_summary  = EXCLUDED.outcome_summary,
			updated_at       = NOW()
	`,
		m.Date, string(m.ListType), m.Ticker, m.PromptVersion,
		m.ContextSummary, m.Embedding,
		m.LLMSetup, m.LLMConviction, m.LLMEntry, m.LLMStop,
		m.OutcomeStatus, m.OutcomeReturn5D, m.OutcomeStopHit,
		m.OutcomeTargetHit, m.OutcomeSummary,
	)
	if err != nil {
		return fmt.Errorf("UpsertMemory %s %s %s: %w",
			m.Date.Format("2006-01-02"), m.ListType, m.Ticker, err)
	}
	return nil
}

// FindSimilar returns the top-K most similar verified memories.
// excludeTicker filters out self-matches; maxAgeDays limits how far back to look.
func (r *PromptMemoryRepo) FindSimilar(ctx context.Context, embedding pgvector.Vector, listType models.ListType, topK int, excludeTicker string, maxAgeDays int) ([]models.PromptMemory, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, ticker, prompt_version,
		       context_summary, embedding,
		       llm_setup, llm_conviction, llm_entry, llm_stop,
		       outcome_status, outcome_return_5d, outcome_stop_hit,
		       outcome_target_hit, outcome_summary,
		       created_at, updated_at
		FROM   prompt_memory
		WHERE  list_type = $1
		  AND  outcome_status = 'verified'
		  AND  embedding IS NOT NULL
		  AND  ($4 = '' OR ticker != $4)
		  AND  date >= NOW() - make_interval(days => $5)
		ORDER  BY embedding <=> $2
		LIMIT  $3
	`, string(listType), embedding, topK, excludeTicker, maxAgeDays)
	if err != nil {
		return nil, fmt.Errorf("FindSimilar: %w", err)
	}
	defer rows.Close()

	var out []models.PromptMemory
	for rows.Next() {
		var m models.PromptMemory
		var lt string
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Ticker, &m.PromptVersion,
			&m.ContextSummary, &m.Embedding,
			&m.LLMSetup, &m.LLMConviction, &m.LLMEntry, &m.LLMStop,
			&m.OutcomeStatus, &m.OutcomeReturn5D, &m.OutcomeStopHit,
			&m.OutcomeTargetHit, &m.OutcomeSummary,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("FindSimilar scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		m.ContextSummary = strings.Clone(m.ContextSummary)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetPendingOutcomes returns memories with status = 'pending'.
func (r *PromptMemoryRepo) GetPendingOutcomes(ctx context.Context) ([]models.PromptMemory, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, ticker, prompt_version,
		       context_summary, embedding,
		       llm_setup, llm_conviction, llm_entry, llm_stop,
		       outcome_status, outcome_return_5d, outcome_stop_hit,
		       outcome_target_hit, outcome_summary,
		       created_at, updated_at
		FROM   prompt_memory
		WHERE  outcome_status = 'pending'
		ORDER  BY date ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetPendingOutcomes: %w", err)
	}
	defer rows.Close()

	var out []models.PromptMemory
	for rows.Next() {
		var m models.PromptMemory
		var lt string
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Ticker, &m.PromptVersion,
			&m.ContextSummary, &m.Embedding,
			&m.LLMSetup, &m.LLMConviction, &m.LLMEntry, &m.LLMStop,
			&m.OutcomeStatus, &m.OutcomeReturn5D, &m.OutcomeStopHit,
			&m.OutcomeTargetHit, &m.OutcomeSummary,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetPendingOutcomes scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Ticker = strings.Clone(m.Ticker)
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateOutcome sets the outcome fields for a memory entry.
func (r *PromptMemoryRepo) UpdateOutcome(ctx context.Context, id uuid.UUID, status string, return5d *float64, stopHit, targetHit *bool, summary *string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE prompt_memory SET
			outcome_status     = $2,
			outcome_return_5d  = $3,
			outcome_stop_hit   = $4,
			outcome_target_hit = $5,
			outcome_summary    = $6,
			updated_at         = NOW()
		WHERE id = $1
	`, id, status, return5d, stopHit, targetHit, summary)
	if err != nil {
		return fmt.Errorf("UpdateOutcome %s: %w", id, err)
	}
	return nil
}
