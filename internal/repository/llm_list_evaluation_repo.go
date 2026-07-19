package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// LLMListEvaluationRepo handles persistence for the llm_list_evaluations table.
type LLMListEvaluationRepo struct {
	db dbPool // reuse the interface defined in market_inputs_repo.go
}

// NewLLMListEvaluationRepo creates a new LLMListEvaluationRepo backed by a live pool.
func NewLLMListEvaluationRepo(db *pgxpool.Pool) *LLMListEvaluationRepo {
	return &LLMListEvaluationRepo{db: db}
}

const upsertLLMListEvaluationSQL = `
	INSERT INTO llm_list_evaluations (
		date, list_type, variant_name, provider, model, prompt_version,
		system_prompt, user_prompt, raw_response, parsed_json,
		input_tickers, output_tickers,
		input_tokens, output_tokens, duration_ms
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10::jsonb,
		$11, $12,
		$13, $14, $15
	)
	ON CONFLICT (date, list_type, variant_name) DO UPDATE SET
		provider       = EXCLUDED.provider,
		model          = EXCLUDED.model,
		prompt_version = EXCLUDED.prompt_version,
		system_prompt  = EXCLUDED.system_prompt,
		user_prompt    = EXCLUDED.user_prompt,
		raw_response   = EXCLUDED.raw_response,
		parsed_json    = EXCLUDED.parsed_json,
		input_tickers  = EXCLUDED.input_tickers,
		output_tickers = EXCLUDED.output_tickers,
		input_tokens   = EXCLUDED.input_tokens,
		output_tokens  = EXCLUDED.output_tokens,
		duration_ms    = EXCLUDED.duration_ms`

// UpsertEvaluation inserts or replaces an LLM evaluation row.
// Idempotent: re-running for the same (date, list_type, variant_name) overwrites.
func (r *LLMListEvaluationRepo) UpsertEvaluation(ctx context.Context, m *models.LLMListEvaluation) error {
	_, err := r.db.Exec(ctx, upsertLLMListEvaluationSQL,
		m.Date, string(m.ListType), m.VariantName, m.Provider, m.Model, m.PromptVersion,
		m.SystemPrompt, m.UserPrompt, m.RawResponse, m.ParsedJSON,
		m.InputTickers, m.OutputTickers,
		m.InputTokens, m.OutputTokens, m.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("UpsertEvaluation %s %s: %w",
			m.Date.Format("2006-01-02"), m.ListType, err)
	}
	return nil
}

// GetEvaluation returns the LLM evaluation for a given date and list type.
// Returns nil, nil when no evaluation exists (so callers can check for idempotency).
func (r *LLMListEvaluationRepo) GetEvaluation(ctx context.Context, date time.Time, listType models.ListType) (*models.LLMListEvaluation, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, date, list_type, provider, model, prompt_version,
		       system_prompt, user_prompt, raw_response, parsed_json,
		       input_tickers, output_tickers,
		       input_tokens, output_tokens, duration_ms, created_at
		FROM   llm_list_evaluations
		WHERE  date = $1 AND list_type = $2
	`, date, string(listType))

	var m models.LLMListEvaluation
	var lt string
	var parsedJSONSingle []byte
	if err := row.Scan(
		&m.ID, &m.Date, &lt, &m.Provider, &m.Model, &m.PromptVersion,
		&m.SystemPrompt, &m.UserPrompt, &m.RawResponse, &parsedJSONSingle,
		&m.InputTickers, &m.OutputTickers,
		&m.InputTokens, &m.OutputTokens, &m.DurationMs, &m.CreatedAt,
	); err != nil {
		if isNoRows(err) {
			return nil, nil //nolint:nilnil // no evaluation found for date+listType
		}
		return nil, fmt.Errorf("GetEvaluation %s %s: %w",
			date.Format("2006-01-02"), listType, err)
	}
	m.ListType = models.ListType(strings.Clone(lt))
	m.Provider = strings.Clone(m.Provider)
	m.Model = strings.Clone(m.Model)
	m.PromptVersion = strings.Clone(m.PromptVersion)
	m.SystemPrompt = strings.Clone(m.SystemPrompt)
	m.UserPrompt = strings.Clone(m.UserPrompt)
	m.RawResponse = strings.Clone(m.RawResponse)
	m.InputTickers = cloneStringSlice(m.InputTickers)
	m.OutputTickers = cloneStringSlice(m.OutputTickers)
	if len(parsedJSONSingle) > 0 {
		m.ParsedJSON = append([]byte(nil), parsedJSONSingle...)
	}
	return &m, nil
}

// GetLatestEvaluationDate returns the most recent date that has at least one
// LLM evaluation. Returns time.Time{} and nil when the table is empty.
func (r *LLMListEvaluationRepo) GetLatestEvaluationDate(ctx context.Context) (time.Time, error) {
	var d time.Time
	err := r.db.QueryRow(ctx, `
		SELECT date FROM llm_list_evaluations ORDER BY date DESC LIMIT 1
	`).Scan(&d)
	if err != nil {
		if isNoRows(err) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("GetLatestEvaluationDate: %w", err)
	}
	return d, nil
}

// ListAvailableDates returns distinct report dates (newest first), limited to n.
func (r *LLMListEvaluationRepo) ListAvailableDates(ctx context.Context, limit int) ([]time.Time, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT date FROM llm_list_evaluations
		ORDER BY date DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListAvailableDates: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("ListAvailableDates scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetEvaluationsByDate returns all LLM evaluations for a given date.
func (r *LLMListEvaluationRepo) GetEvaluationsByDate(ctx context.Context, date time.Time) ([]models.LLMListEvaluation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, date, list_type, provider, model, prompt_version,
		       system_prompt, user_prompt, raw_response, parsed_json,
		       input_tickers, output_tickers,
		       input_tokens, output_tokens, duration_ms, created_at
		FROM   llm_list_evaluations
		WHERE  date = $1
		ORDER  BY list_type
	`, date)
	if err != nil {
		return nil, fmt.Errorf("GetEvaluationsByDate %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	var out []models.LLMListEvaluation
	for rows.Next() {
		var m models.LLMListEvaluation
		var lt string
		var parsedJSON []byte
		if err := rows.Scan(
			&m.ID, &m.Date, &lt, &m.Provider, &m.Model, &m.PromptVersion,
			&m.SystemPrompt, &m.UserPrompt, &m.RawResponse, &parsedJSON,
			&m.InputTickers, &m.OutputTickers,
			&m.InputTokens, &m.OutputTokens, &m.DurationMs, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetEvaluationsByDate scan: %w", err)
		}
		m.ListType = models.ListType(strings.Clone(lt))
		m.Provider = strings.Clone(m.Provider)
		m.Model = strings.Clone(m.Model)
		m.PromptVersion = strings.Clone(m.PromptVersion)
		m.SystemPrompt = strings.Clone(m.SystemPrompt)
		m.UserPrompt = strings.Clone(m.UserPrompt)
		m.RawResponse = strings.Clone(m.RawResponse)
		m.InputTickers = cloneStringSlice(m.InputTickers)
		m.OutputTickers = cloneStringSlice(m.OutputTickers)
		if len(parsedJSON) > 0 {
			m.ParsedJSON = append([]byte(nil), parsedJSON...)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
