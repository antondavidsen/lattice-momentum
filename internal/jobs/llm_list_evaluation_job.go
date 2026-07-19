package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
	llmsvc "ai-stock-service/internal/services/llm"
)

// llmListEvalJobName is emitted in every log line produced by this job.
const llmListEvalJobName = "LLMListEvaluationJob"

// ── Interfaces ────────────────────────────────────────────────────────────────

// listEvaluator is the subset of EvaluationService that the job requires.
type listEvaluator interface {
	EvaluateList(
		ctx context.Context,
		date time.Time,
		listType models.ListType,
		tickers []string,
	) (*llmsvc.EvaluationResult, error)
}

// rankListLoader reads ranked lists from the database.
type rankListLoader interface {
	GetRankList(ctx context.Context, date time.Time, listType models.ListType) ([]models.DailyRankList, error)
}

// evaluationStorer persists LLM evaluation results.
type evaluationStorer interface {
	UpsertEvaluation(ctx context.Context, m *models.LLMListEvaluation) error
	GetEvaluation(ctx context.Context, date time.Time, listType models.ListType) (*models.LLMListEvaluation, error)
}

// Compile-time assertions.
var _ rankListLoader = (*repository.RankListRepo)(nil)
var _ evaluationStorer = (*repository.LLMListEvaluationRepo)(nil)

// ── Job ───────────────────────────────────────────────────────────────────────

// LLMListEvaluationJob runs the LLM qualitative diligence layer for all three
// ranking lists. It is Step 9 of the nightly pipeline.
type LLMListEvaluationJob struct {
	evaluator listEvaluator
	rankRepo  rankListLoader
	evalRepo  evaluationStorer
	log       *slog.Logger
}

// NewLLMListEvaluationJob constructs a job from production concrete types.
func NewLLMListEvaluationJob(
	evaluator *llmsvc.EvaluationService,
	rankRepo *repository.RankListRepo,
	evalRepo *repository.LLMListEvaluationRepo,
	log *slog.Logger,
) *LLMListEvaluationJob {
	return &LLMListEvaluationJob{
		evaluator: evaluator,
		rankRepo:  rankRepo,
		evalRepo:  evalRepo,
		log:       log,
	}
}

// NewLLMListEvaluationJobFromSources constructs a job from any values that
// satisfy the interfaces. Intended for tests.
func NewLLMListEvaluationJobFromSources(
	evaluator listEvaluator,
	rankRepo rankListLoader,
	evalRepo evaluationStorer,
	log *slog.Logger,
) *LLMListEvaluationJob {
	return &LLMListEvaluationJob{
		evaluator: evaluator,
		rankRepo:  rankRepo,
		evalRepo:  evalRepo,
		log:       log,
	}
}

// allListTypes defines the evaluation order.
var allListTypes = []models.ListType{
	models.ListTypeEP,
	models.ListTypeMomentum,
	models.ListTypeLeaders,
}

// RunLLMListEvaluationJob executes the LLM diligence layer for all three
// lists on the given date.
//
// For each list:
//  1. Load the ranked tickers from daily_rank_lists.
//  2. Check idempotency — skip if evaluation already exists.
//  3. Call EvaluationService.EvaluateList with the ticker symbols.
//  4. Persist the result.
//
// Individual list failures are logged and collected but do not abort the
// remaining lists. The job returns an error only if ALL lists fail.
func (j *LLMListEvaluationJob) RunLLMListEvaluationJob(ctx context.Context, date time.Time) error {
	tag := date.Format("2006-01-02")
	start := time.Now()

	j.log.Info("job starting",
		"job", llmListEvalJobName,
		"date", tag,
	)

	var (
		succeeded int
		skipped   int
		failed    int
		errs      []error
	)

	for _, lt := range allListTypes {
		err := j.evaluateOne(ctx, date, lt, tag)
		switch {
		case errors.Is(err, errAlreadyEvaluated):
			skipped++
		case err != nil:
			failed++
			errs = append(errs, err)
			j.log.Error("list evaluation failed — continuing with next list",
				"job", llmListEvalJobName,
				"date", tag,
				"list_type", string(lt),
				"error", err,
			)
		default:
			succeeded++
		}
	}

	j.log.Info("job completed",
		"job", llmListEvalJobName,
		"date", tag,
		"succeeded", succeeded,
		"skipped", skipped,
		"failed", failed,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	// Only error if ALL lists failed.
	if failed == len(allListTypes) {
		return fmt.Errorf("%s [%s]: all lists failed: %v", llmListEvalJobName, tag, errs)
	}
	return nil
}

// errAlreadyEvaluated is a sentinel used internally to signal idempotency skip.
var errAlreadyEvaluated = fmt.Errorf("already evaluated")

// evaluateOne processes a single list type.
func (j *LLMListEvaluationJob) evaluateOne(
	ctx context.Context,
	date time.Time,
	listType models.ListType,
	tag string,
) error {
	// ── 1. Idempotency check ──────────────────────────────────────────────────
	existing, err := j.evalRepo.GetEvaluation(ctx, date, listType)
	if err != nil {
		return fmt.Errorf("idempotency check for %s: %w", listType, err)
	}
	if existing != nil {
		j.log.Info("evaluation already exists — skipping",
			"job", llmListEvalJobName,
			"date", tag,
			"list_type", string(listType),
		)
		return errAlreadyEvaluated
	}

	// ── 2. Load ranked tickers ────────────────────────────────────────────────
	rankList, err := j.rankRepo.GetRankList(ctx, date, listType)
	if err != nil {
		return fmt.Errorf("load rank list for %s: %w", listType, err)
	}
	if len(rankList) == 0 {
		j.log.Warn("no ranked tickers found — skipping",
			"job", llmListEvalJobName,
			"date", tag,
			"list_type", string(listType),
		)
		return errAlreadyEvaluated // treat as skip
	}

	// Extract ticker symbols in rank order.
	tickers := make([]string, len(rankList))
	for i := range rankList {
		tickers[i] = rankList[i].Ticker
	}

	j.log.Info("evaluating list",
		"job", llmListEvalJobName,
		"date", tag,
		"list_type", string(listType),
		"tickers", tickers,
	)

	// ── 3. Call LLM evaluation ────────────────────────────────────────────────
	result, err := j.evaluator.EvaluateList(ctx, date, listType, tickers)
	if err != nil {
		return fmt.Errorf("evaluate %s: %w", listType, err)
	}

	// ── 4. Persist ────────────────────────────────────────────────────────────
	row := result.ToModel()
	if err := j.evalRepo.UpsertEvaluation(ctx, row); err != nil {
		return fmt.Errorf("persist evaluation for %s: %w", listType, err)
	}

	j.log.Info("list evaluation completed and persisted",
		"job", llmListEvalJobName,
		"date", tag,
		"list_type", string(listType),
		"provider", result.Provider,
		"model", result.Model,
		"input_tokens", result.InputTokens,
		"output_tokens", result.OutputTokens,
		"duration_ms", result.DurationMs,
	)

	return nil
}
