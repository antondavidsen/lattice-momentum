package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"ai-stock-service/internal/bootstrap"
	"ai-stock-service/internal/repository"
	"ai-stock-service/internal/services/learning"
)

func main() {
	days := flag.Int("days", 90, "lookback window in days (default: 90)")
	flag.Parse()
	os.Exit(runMain(*days))
}

func runMain(days int) int {
	b := bootstrap.MustStartup("calibration-analysis",
		bootstrap.WithoutDotenv(),
		bootstrap.WithoutSetDefault(),
		bootstrap.WithoutMigrations(),
	)
	defer b.Pool.Close()

	ctx := b.Ctx
	logger := b.Logger
	pool := b.Pool

	ptoRepo := repository.NewPromptTickerOutcomeRepo(pool)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -days)

	reports, err := learning.GenerateCalibrationReport(ctx, ptoRepo, from, today)
	if err != nil {
		logger.Error("generate calibration report", "error", err)
		return 1
	}

	fmt.Print(learning.FormatCalibrationReport(reports, from, today))
	return 0
}
