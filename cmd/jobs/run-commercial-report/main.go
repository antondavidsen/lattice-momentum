// Usage: go run ./cmd/jobs/run_commercial_report.go [--date=YYYY-MM-DD]
package main

import (
	"flag"
	"os"
	"time"

	"ai-stock-service/internal/bootstrap"
	"ai-stock-service/internal/jobs"
	"ai-stock-service/internal/llm"
	"ai-stock-service/internal/repository"
	llmsvc "ai-stock-service/internal/services/llm"
)

func main() {
	dateFlag := flag.String("date", "", "Report date (YYYY-MM-DD). Default: yesterday.")
	flag.Parse()
	os.Exit(runMain(dateFlag))
}

func runMain(dateFlag *string) int {
	b := bootstrap.MustStartup("commercial-report")
	defer b.Pool.Close()

	ctx := b.Ctx
	logger := b.Logger
	pool := b.Pool
	cfg := b.Cfg

	// Resolve target date.
	date := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour)
	if *dateFlag != "" {
		d, err := time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			logger.Error("invalid date", "date", *dateFlag, "error", err)
			return 1
		}
		date = d.UTC()
	}

	// Wire dependencies.
	llmProvider, err := llm.NewProvider(cfg.LLMConfig())
	if err != nil {
		logger.Error("create LLM provider", "error", err)
		return 1
	}

	evalRepo := repository.NewLLMListEvaluationRepo(pool)
	reportRepo := repository.NewCommercialReportRepo(pool)
	regimeRepo := repository.NewMarketRegimeRepo(pool)

	svc := llmsvc.NewCommercialReportService(
		llmProvider,
		llmsvc.CommercialReportConfig{
			Model:       cfg.LLMModel,
			MaxTokens:   16000,
			Temperature: 0.15,
		},
		logger,
	)

	job := jobs.NewCommercialReportJob(svc, evalRepo, reportRepo, regimeRepo, logger)

	logger.Info("running commercial report job", "date", date.Format("2006-01-02"))
	if err := job.RunCommercialReportJob(ctx, date, "full"); err != nil {
		logger.Error("commercial report job failed", "error", err)
		return 1
	}
	logger.Info("commercial report job completed")
	return 0
}
