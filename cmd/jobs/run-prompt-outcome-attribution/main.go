package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"ai-stock-service/internal/bootstrap"
	"ai-stock-service/internal/jobs"
	"ai-stock-service/internal/repository"
)

func main() {
	dateFlag := flag.String("date", "", "reference date YYYY-MM-DD (default: today UTC)")
	flag.Parse()
	os.Exit(runMain(dateFlag))
}

func runMain(dateFlag *string) int {
	b := bootstrap.MustStartup("prompt-outcome-attribution")
	defer b.Pool.Close()

	ctx := b.Ctx
	log := b.Logger
	pool := b.Pool

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if *dateFlag != "" {
		parsed, err := time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			log.Error("invalid --date", "value", *dateFlag, "error", err)
			return 1
		}
		today = parsed
	}

	ptoRepo := repository.NewPromptTickerOutcomeRepo(pool)
	evalRepo := repository.NewLLMListEvaluationRepo(pool)
	outcomeRepo := repository.NewTradeOutcomeRepo(pool)
	candleRepo := repository.NewCandlesDailyRepo(pool)

	job := jobs.NewPromptOutcomeAttributionJob(evalRepo, outcomeRepo, candleRepo, ptoRepo, log)

	fmt.Printf("Running prompt outcome attribution for %s...\n", today.Format("2006-01-02"))
	if err := job.RunAttributionJob(ctx, today); err != nil {
		log.Error("attribution failed", "error", err)
		return 1
	}
	return 0
}
