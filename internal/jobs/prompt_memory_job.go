package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	llmsvc "ai-stock-service/internal/services/llm"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

type memoryStorer interface {
	UpsertMemory(ctx context.Context, m *models.PromptMemory) error
	GetPendingOutcomes(ctx context.Context) ([]models.PromptMemory, error)
	UpdateOutcome(ctx context.Context, id uuid.UUID, status string, return5d *float64, stopHit, targetHit *bool, summary *string) error
}

type embedder interface {
	Embed(ctx context.Context, text string) (pgvector.Vector, error)
}

// Compile-time assertions.
var _ memoryStorer = (*repository.PromptMemoryRepo)(nil)
var _ embedder = (*llmsvc.EmbeddingService)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// PromptMemoryJob handles storing new memories and updating pending outcomes.
type PromptMemoryJob struct {
	memStorer memoryStorer
	evalRepo  evalSource
	ptoRepo   ptoVersionSource
	embedSvc  embedder
	log       *slog.Logger
}

// NewPromptMemoryJob constructs a new prompt memory job.
func NewPromptMemoryJob(
	memoryRepo *repository.PromptMemoryRepo,
	evalRepo *repository.LLMListEvaluationRepo,
	ptoRepo *repository.PromptTickerOutcomeRepo,
	embedSvc *llmsvc.EmbeddingService,
	log *slog.Logger,
) *PromptMemoryJob {
	return &PromptMemoryJob{
		memStorer: memoryRepo,
		evalRepo:  evalRepo,
		ptoRepo:   ptoRepo,
		embedSvc:  embedSvc,
		log:       log,
	}
}

// NewPromptMemoryJobFromSources constructs a prompt memory job from interfaces (for testing).
func NewPromptMemoryJobFromSources(
	memStorer memoryStorer,
	evalSrc evalSource,
	ptoSrc ptoVersionSource,
	embedder embedder,
	log *slog.Logger,
) *PromptMemoryJob {
	return &PromptMemoryJob{
		memStorer: memStorer,
		evalRepo:  evalSrc,
		ptoRepo:   ptoSrc,
		embedSvc:  embedder,
		log:       log,
	}
}

// StoreMemories stores memories for today's evaluations.
func (j *PromptMemoryJob) StoreMemories(ctx context.Context, date time.Time) error {
	start := time.Now()
	j.log.Info("prompt memory store starting", "date", date)

	evaluations, err := j.evalRepo.GetEvaluationsByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("load evaluations: %w", err)
	}

	var stored, skipped int
	for i := range evaluations {
		eval := &evaluations[i]
		var parsed models.EvaluationParsedOutput
		if len(eval.ParsedJSON) == 0 || string(eval.ParsedJSON) == "{}" {
			continue
		}
		if err := json.Unmarshal(eval.ParsedJSON, &parsed); err != nil {
			continue
		}

		for i := range parsed.Tickers {
			pt := &parsed.Tickers[i]
			// Build context summary.
			contextSummary := fmt.Sprintf("%s|%s|setup_%s",
				pt.Ticker, string(eval.ListType), pt.Setup)

			// Embed the context summary.
			embedding, err := j.embedSvc.Embed(ctx, contextSummary)
			if err != nil {
				j.log.Warn("embed context failed", "ticker", pt.Ticker, "error", err)
				skipped++
				continue
			}

			mem := &models.PromptMemory{
				Date:           date,
				ListType:       eval.ListType,
				Ticker:         pt.Ticker,
				PromptVersion:  eval.PromptVersion,
				ContextSummary: contextSummary,
				Embedding:      embedding,
				OutcomeStatus:  "pending",
			}

			if pt.Setup != "" {
				mem.LLMSetup = &pt.Setup
			}
			if pt.Conviction != "" {
				mem.LLMConviction = &pt.Conviction
			}
			if pt.EntryLow != nil && pt.EntryHigh != nil {
				entry := fmt.Sprintf("$%.2f-$%.2f", *pt.EntryLow, *pt.EntryHigh)
				mem.LLMEntry = &entry
			}
			if pt.StopPrice != nil {
				stop := fmt.Sprintf("$%.2f", *pt.StopPrice)
				mem.LLMStop = &stop
			}

			if err := j.memStorer.UpsertMemory(ctx, mem); err != nil {
				j.log.Warn("upsert memory failed", "ticker", pt.Ticker, "error", err)
				skipped++
			} else {
				stored++
			}
		}
	}

	j.log.Info("prompt memory store complete",
		"stored", stored, "skipped", skipped,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// UpdateOutcomes updates pending memories with verified outcomes.
func (j *PromptMemoryJob) UpdateOutcomes(ctx context.Context) error {
	start := time.Now()
	j.log.Info("prompt memory outcome update starting")

	pending, err := j.memStorer.GetPendingOutcomes(ctx)
	if err != nil {
		return fmt.Errorf("get pending outcomes: %w", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	var updated, insufficient, skipped int

	for i := range pending {
		mem := &pending[i]
		daysSince := int(today.Sub(mem.Date).Hours() / 24)

		// Need at least 5 days of forward data.
		if daysSince < 7 { // 5 trading days ≈ 7 calendar days
			skipped++
			continue
		}

		// Look up matching prompt_ticker_outcome.
		outcomes, err := j.ptoRepo.GetByDateRange(ctx, mem.Date, mem.Date)
		if err != nil {
			j.log.Warn("lookup pto failed", "date", mem.Date, "error", err)
			continue
		}

		var matched *models.PromptTickerOutcome
		for i := range outcomes {
			if outcomes[i].Ticker == mem.Ticker && outcomes[i].ListType == mem.ListType {
				matched = &outcomes[i]
				break
			}
		}

		if matched == nil || matched.EvaluatedDays < 5 {
			if daysSince > 30 {
				// Too old and no data — mark as insufficient.
				if err := j.memStorer.UpdateOutcome(ctx, mem.ID, "insufficient_data", nil, nil, nil, nil); err != nil {
					j.log.Warn("mark insufficient failed", "id", mem.ID, "error", err)
				}
				insufficient++
			} else {
				skipped++
			}
			continue
		}

		// Generate outcome summary (template-based, NOT LLM).
		summary := GenerateOutcomeSummary(mem, matched)

		if err := j.memStorer.UpdateOutcome(ctx, mem.ID, "verified",
			matched.ActualReturn5D, matched.StopHit, matched.Target1Hit, &summary,
		); err != nil {
			j.log.Warn("update outcome failed", "id", mem.ID, "error", err)
		} else {
			updated++
		}
	}

	j.log.Info("prompt memory outcome update complete",
		"updated", updated, "insufficient", insufficient, "skipped", skipped,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// GenerateOutcomeSummary creates a human-readable summary of the outcome for a given memory and its corresponding prompt ticker outcome.
func GenerateOutcomeSummary(mem *models.PromptMemory, pto *models.PromptTickerOutcome) string {
	var parts []string

	if pto.StopHit != nil && *pto.StopHit {
		stopStr := "unknown level"
		if pto.RecommendedStop != nil {
			stopStr = fmt.Sprintf("$%.2f", *pto.RecommendedStop)
		}
		parts = append(parts, fmt.Sprintf("Stop hit at %s.", stopStr))
	}

	if pto.Target1Hit != nil && *pto.Target1Hit {
		parts = append(parts, "Target 1 reached.")
	}

	if pto.ActualReturn5D != nil {
		parts = append(parts, fmt.Sprintf("5D return: %+.1f%%.", *pto.ActualReturn5D*100))
	}

	if len(parts) == 0 {
		parts = append(parts, "Outcome recorded.")
	}

	return fmt.Sprintf("%s [%s]", joinParts(parts), mem.Ticker)
}

func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += " "
		}
		result += p
	}
	return result
}
