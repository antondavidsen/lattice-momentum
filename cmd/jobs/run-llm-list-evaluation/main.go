// cmd/jobs/run_llm_list_evaluation: standalone CLI that runs the LLM list
// evaluation step for a single trading session.
//
// Usage:
//
//	go run ./cmd/jobs/run_llm_list_evaluation.go [--date=YYYY-MM-DD]
//
// Flags:
//
//	--date   Trading session date in YYYY-MM-DD format.
//	         Defaults to today (UTC).
//
// Environment:
//
//	LLM_PROVIDER, OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY
//	LLM_MODEL, LLM_MAX_TOKENS, LLM_TEMPERATURE  (optional overrides)
//	DATABASE_URL (required)
//
// Examples:
//
//	# Evaluate today's ranked lists
//	go run ./cmd/jobs/run_llm_list_evaluation.go
//
//	# Evaluate a specific historical date (idempotent — skips if already done)
//	go run ./cmd/jobs/run_llm_list_evaluation.go --date=2026-04-14

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"ai-stock-service/internal/bootstrap"
	pipelinejobs "ai-stock-service/internal/jobs"
	"ai-stock-service/internal/llm"
	"ai-stock-service/internal/repository"
	llmsvc "ai-stock-service/internal/services/llm"
)

func main() {
	dateFlag := flag.String("date", "",
		"trading session date in YYYY-MM-DD format (default: today UTC)")
	flag.Parse()
	os.Exit(runMain(dateFlag))
}

func runMain(dateFlag *string) int {
	b := bootstrap.MustStartup("llm-list-evaluation",
		bootstrap.WithHandlerOptions(&slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	defer b.Pool.Close()

	logger := b.Logger
	ctx := b.Ctx
	cfg := b.Cfg
	pool := b.Pool

	// ── Resolve target date ───────────────────────────────────────────────────
	targetDate, err := resolveDate(*dateFlag)
	if err != nil {
		logger.Error("invalid --date flag", "value", *dateFlag, "error", err)
		return 1
	}
	logger.Info("run_llm_list_evaluation: target date resolved",
		"date", targetDate.Format("2006-01-02"),
		"source", dateSource(*dateFlag),
	)

	// ── Wire LLM provider ────────────────────────────────────────────────────
	llmProvider, err := llm.NewProvider(cfg.LLMConfig())
	if err != nil {
		logger.Error("init LLM provider", "error", err)
		return 1
	}

	// ── Wire repositories and services ───────────────────────────────────────
	tvSnapshotRepo := repository.NewTVSnapshotRepo(pool)
	marketRegimeRepo := repository.NewMarketRegimeRepo(pool)
	sectorScoresRepo := repository.NewSectorScoresRepo(pool)
	rankListRepo := repository.NewRankListRepo(pool)
	llmEvalRepo := repository.NewLLMListEvaluationRepo(pool)
	candleRepo := repository.NewCandlesDailyRepo(pool)

	evalSvc := llmsvc.NewEvaluationService(
		llmProvider,
		tvSnapshotRepo,
		marketRegimeRepo,
		sectorScoresRepo,
		candleRepo,
		nil, // memoryRepo — not wired yet (R-06: needs PromptMemoryRepo)
		nil, // narrativeVelocityRepo — not wired in standalone job
		nil, // embedder — not wired in standalone job (RAG disabled)
		llmsvc.EvaluationConfig{
			Model:       cfg.LLMModel,
			MaxTokens:   cfg.LLMMaxTokens,
			Temperature: cfg.LLMTemperature,
		},
		logger,
	)

	// ── Run job ───────────────────────────────────────────────────────────────
	job := pipelinejobs.NewLLMListEvaluationJob(evalSvc, rankListRepo, llmEvalRepo, logger)

	logger.Info("run_llm_list_evaluation: starting job",
		"date", targetDate.Format("2006-01-02"),
	)

	if err := job.RunLLMListEvaluationJob(ctx, targetDate); err != nil {
		logger.Error("run_llm_list_evaluation: job failed", "error", err)
		return 1
	}

	logger.Info("run_llm_list_evaluation: job finished successfully",
		"date", targetDate.Format("2006-01-02"),
	)
	return 0
}

// ── helpers ───────────────────────────────────────────────────────────────────

func resolveDate(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC().Truncate(24 * time.Hour), nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected YYYY-MM-DD, got %q: %w", raw, err)
	}
	return t, nil
}

func dateSource(raw string) string {
	if raw == "" {
		return "default (today UTC)"
	}
	return "--date flag"
}
