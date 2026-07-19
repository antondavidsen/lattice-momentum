// cmd/nightly: runs the full nightly pipeline once and exits.
// Intended to be called by the cron scheduler container each trading day.
//
// Pipeline execution order:
//
//	Step 1 — TradingView screener imports      (tv-collector)
//	Step 2 — TradingView snapshot check        (post-Step-1 sanity check)
//	Step 3 — Start candle ingestion (async)    (spawns worker pool, returns immediately)
//	Step 4 — Wait for priority ingestion       (blocks until CRITICAL tickers are fresh)
//	Step 5 — Market regime inputs              (SMA, breadth, RS, distribution days)
//	          ↑ runs while NORMAL tickers continue ingesting in the background
//	Step 6 — Market regime classification      (label + risk score → market_regime_daily)
//	Step 7 — Sector momentum scoring           (sector rotation scores → sector_scores_daily)
//	Step 8 — Daily ranking lists               (EP + Momentum + Leade → daily_rank_lists)
//	Step 9 — LLM list evaluation               (qualitative diligence → llm_list_evaluations)
//	Step 10— Trade outcome calculation         (forward performance → trade_outcomes_daily)
//	Step 11— Ticker enrichment                 (Massive.com company data)
//	Step 12— Commercial report transformation  (subscriber-friendly reports)
//	Post   — Drain background ingestion        (wait for NORMAL tier before exiting)
//	Post   — Trade outcome calculation         (forward performance → trade_outcomes_daily; needs all candles)
package main

import (
	"ai-stock-service/internal/bootstrap"
	pipelinejobs "ai-stock-service/internal/jobs"
	"ai-stock-service/internal/services/marketdata"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-stock-service/internal/llm"
	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/repository"

	"ai-stock-service/internal/services"
	llmsvc "ai-stock-service/internal/services/llm"

	marketdatajobs "ai-stock-service/internal/services/marketdata/jobs"
	"ai-stock-service/internal/services/marketdata/provider"
	"ai-stock-service/internal/services/outcomes"
	"ai-stock-service/internal/services/ranking"
	"ai-stock-service/internal/services/sector"

	"github.com/prometheus/client_golang/prometheus"
)

var targetDate string

func main() {
	flag.StringVar(&targetDate, "date", "", "Target date YYYY-MM-DD (default: today UTC)")
	flag.Parse()

	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	b := bootstrap.MustStartup("momentum-scheduler",
		bootstrap.WithoutDotenv(),
	)
	defer b.Pool.Close()

	cfg := b.Cfg
	pool := b.Pool
	logger := b.Logger

	baseCtx, cancel := signal.NotifyContext(b.Ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctx, timeoutCancel := context.WithTimeout(baseCtx, 90*time.Minute)
	defer timeoutCancel()

	// ── Dependency wiring ─────────────────────────────────────────────────────
	p, err := provider.NewFromConfig(cfg)
	if err != nil {
		logger.Error("build market data provider", "error", err)
		return err
	}

	tickerRepo := repository.NewTickerRepo(pool)
	candleRepo := repository.NewCandlesDailyRepo(pool)
	nightlyRunRepo := repository.NewNightlyRunRepo(pool)
	marketDataRepo := repository.NewMarketDataRepo(pool)
	marketInputRepo := repository.NewMarketInputsRepo(pool)
	marketRegimeRepo := repository.NewMarketRegimeRepo(pool)
	tvSnapshotRepo := repository.NewTVSnapshotRepo(pool)

	sectorScoresRepo := repository.NewSectorScoresRepo(pool)
	rankListRepo := repository.NewRankListRepo(pool)

	// ── Universe snapshot job (R-04) ───────────────────────────────────────────
	// Records the evaluable universe per date for survivorship quantification.
	// Runs after candle ingestion so prices are available for tradeability check.
	universeSnapshotRepo := repository.NewUniverseSnapshotRepo(pool)
	universeSnapshotJob := pipelinejobs.NewUniverseSnapshotJob(pool, universeSnapshotRepo, logger)

	ingestSvc := marketdata.NewIngestionService(p, candleRepo, tickerRepo, logger)
	marketInputSvc := services.NewMarketInputsService(marketDataRepo, logger)
	regimeJob := pipelinejobs.NewMarketRegimeInputsJob(marketInputSvc, marketInputRepo, logger).
		WithFreshnessChecker(marketDataRepo)
	classificationJob := pipelinejobs.NewMarketRegimeClassificationJob(marketInputRepo, marketRegimeRepo, logger)
	sectorMomentumSvc := sector.NewMomentumService(marketDataRepo, logger)
	sectorMomentumJob := pipelinejobs.NewSectorMomentumJob(sectorMomentumSvc, sectorScoresRepo, logger)

	// ── Weight repos for DB-driven engine weights (R-05) ──────────────────────
	momentumWeightsRepo := repository.NewMomentumWeightsRepo(pool)

	sectorLeadershipRepo := repository.NewSectorLeadershipRepo(pool)

	// ── Narrative velocity repo (for narrative velocity multiplier in ranking) ─
	// Created unconditionally — it's a cheap pool wrapper. The ranking engines use
	// it to apply a max +10% bonus to event quality / breakout strength when
	// narrative velocity data is available. Returns 0.0 / no-op when data is missing.
	narrativeVelRepo := repository.NewNarrativeVelocityRepo(pool)

	momentumEngine := ranking.NewMomentumEngine(tvSnapshotRepo, marketRegimeRepo, sectorScoresRepo, momentumWeightsRepo, logger).
		WithNarrativeVelocity(narrativeVelRepo)

	rankListJob := pipelinejobs.NewDailyRankListsJob(momentumEngine, rankListRepo, logger).
		WithLeadershipBoost(sectorLeadershipRepo)

	// ── RAG validation ───────────────────────────────────────────────────────
	// When RAG_ENABLED=true, EMBEDDING_BACKEND must be "openai" (or "http"/"python")
	// and an API key or endpoint URL must be configured.
	if cfg.RAGEnabled {
		if cfg.EmbeddingBackend == "noop" {
			logger.Error("RAG enabled but EMBEDDING_BACKEND is noop — must be 'openai', 'http', or 'python'")
			return fmt.Errorf("RAG enabled but EMBEDDING_BACKEND=noop")
		}
		if cfg.EmbeddingBackend == "openai" && cfg.OpenAIAPIKey == "" && cfg.EmbeddingEndpointURL == "" {
			logger.Error("RAG enabled with openai backend but no API key or endpoint URL set")
			return fmt.Errorf("RAG enabled: OPENAI_API_KEY or EMBEDDING_ENDPOINT_URL required")
		}
		if cfg.EmbeddingBackend == "http" && cfg.EmbeddingEndpointURL == "" {
			logger.Error("RAG enabled with http backend but EMBEDDING_ENDPOINT_URL is empty")
			return fmt.Errorf("RAG enabled: EMBEDDING_ENDPOINT_URL required for http backend")
		}
	}

	logger.Info("RAG status",
		"enabled", cfg.RAGEnabled,
		"top_k", cfg.RAGTopK,
		"max_age_days", cfg.RAGMaxAgeDays,
		"embedding_backend", cfg.EmbeddingBackend,
	)

	// ── LLM evaluation (Step 9) ──────────────────────────────────────────────
	// Only wired when LLM_EVAL_ENABLED=true and an API key is configured.
	var llmEvalJob *pipelinejobs.LLMListEvaluationJob
	if cfg.LLMEvalEnabled {
		llmProvider, llmErr := llm.NewProvider(cfg.LLMConfig())
		if llmErr != nil {
			logger.Warn("LLM evaluation: provider init failed — Step 9 will be skipped",
				"error", llmErr,
			)
		} else {
			llmEvalRepo := repository.NewLLMListEvaluationRepo(pool)

			// Wire PromptMemoryRepo for RAG context (R-06).
			// When RAG is disabled, memoryRepo stays nil and EvaluateList skips RAG.
			// NOTE: declared as interface type so nil stays truly nil (avoids Go
			// interface footgun where (*T)(nil) != nil when wrapped in interface).
			var memoryRepo llmsvc.MemorySource
			var embedSvc *llmsvc.EmbeddingService
			if cfg.RAGEnabled {
				memoryRepo = repository.NewPromptMemoryRepo(pool)
				embedSvc = llmsvc.NewEmbeddingService(cfg.OpenAIAPIKey, cfg.LLMBaseURL)
				logger.Info("RAG: PromptMemoryRepo wired for LLM evaluation",
					"top_k", cfg.RAGTopK,
					"max_age_days", cfg.RAGMaxAgeDays,
				)
			}

			// Narrative velocity repo (loaded when enrichment is enabled).
			// NOTE: declared as interface type so nil stays truly nil.
			var narrativeVelRepo llmsvc.NarrativeVelocitySource
			if cfg.EnrichmentEnabled {
				narrativeVelRepo = repository.NewNarrativeVelocityRepo(pool)
			}

			evalSvc := llmsvc.NewEvaluationService(
				llmProvider,
				tvSnapshotRepo,
				marketRegimeRepo,
				sectorScoresRepo,
				candleRepo,
				memoryRepo,       // nil when RAG disabled → skips RAG in EvaluateList
				narrativeVelRepo, // nil when enrichment disabled → defaults to 0 in prompts
				embedSvc,         // nil when RAG disabled → zero-vector fallback
				llmsvc.EvaluationConfig{
					Model:       cfg.LLMModel,
					MaxTokens:   cfg.LLMMaxTokens,
					Temperature: cfg.LLMTemperature,
				},
				logger,
			)
			llmEvalJob = pipelinejobs.NewLLMListEvaluationJob(evalSvc, rankListRepo, llmEvalRepo, logger)
		}
	}

	// ── Commercial report repo (shared between report job and enrichment job) ─
	commercialReportRepo := repository.NewCommercialReportRepo(pool)

	// ── Commercial report transformation (Step 12) ──────────────────────────
	// Wired alongside the LLM eval job — only runs if LLM is enabled.
	var commercialReportJob *pipelinejobs.CommercialReportJob
	if cfg.CommercialReportEnabled {
		// Reuse the same LLM provider for the commercial report transformer.
		llmProviderForReport, reportErr := llm.NewProvider(cfg.CommercialLLMConfig())
		if reportErr != nil {
			logger.Warn("Commercial report: provider init failed — Step 12 will be skipped",
				"error", reportErr,
			)
		} else {
			llmEvalRepoForReport := repository.NewLLMListEvaluationRepo(pool)
			commercialSvc := llmsvc.NewCommercialReportService(
				llmProviderForReport,
				llmsvc.CommercialReportConfig{
					Model:       cfg.CommercialLLMModel,
					MaxTokens:   16000,
					Temperature: 0.15,
				},
				logger,
			)
			commercialReportJob = pipelinejobs.NewCommercialReportJob(
				commercialSvc, llmEvalRepoForReport, commercialReportRepo, marketRegimeRepo, logger,
			).WithBreadthDivergence(marketRegimeRepo)
		}
	}

	// ── Trade outcome tracking (Step 10 + quarantine) ─────────────────────────
	tradeOutcomeRepo := repository.NewTradeOutcomeRepo(pool)
	tradeOutcomeSvc := outcomes.NewTradeOutcomeService(rankListRepo, candleRepo, logger)
	quarantineRepo := repository.NewTradeOutcomeQuarantineRepo(pool)
	corporateActionRepo := repository.NewCorporateActionRepo(pool)
	tradeOutcomeSvc = tradeOutcomeSvc.
		WithCorporateActionReader(corporateActionRepo).
		WithCandleCloseReader(candleRepo)
	tradeOutcomeJob := pipelinejobs.NewTradeOutcomeJob(tradeOutcomeSvc, tradeOutcomeRepo, logger).
		WithQuarantineRepo(quarantineRepo)

	// ── R-06: Net return computation ─────────────────────────────────────────
	// Computes net-of-cost returns, slippage tiers, and ADV caps for trade
	// outcomes. Runs after TradeOutcomeJob populates gross returns.
	netReturnJob := pipelinejobs.NewNetReturnJob(
		tradeOutcomeRepo,
		repository.NewTickerSnapshotAdapter(tvSnapshotRepo),
		logger,
	)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if targetDate != "" {
		parsed, err := time.Parse("2006-01-02", targetDate)
		if err != nil {
			logger.Error("invalid --date flag", "value", targetDate, "error", err)
			return fmt.Errorf("invalid --date: %w", err)
		}
		today = parsed.UTC()
		logger.Info("running for override date", "date", today.Format("2006-01-02"))
	}

	// Resolve the provider's configured RPM for observability logging.
	rpm := cfg.PolygonRequestsPerMin
	if cfg.MarketDataProvider == "twelvedata" {
		rpm = cfg.TwelveDataRequestsPerMin
	}

	// ingestionHandle is populated by Step 3 and consumed by Step 4 and the
	// post-pipeline drain.
	var ingestionHandle *marketdatajobs.IngestionHandle

	// tvPriorityTickers is populated by Step 2b and consumed by Step 3.
	// These are the distinct tickers from today's TradingView screener output
	// that should be ingested at CRITICAL priority.
	var tvPriorityTickers []string

	// gateLevel is set by the R-09 circuit breaker step (after regime classification)
	// and controls which downstream steps execute.
	var gateLevel string

	// ── Pipeline ──────────────────────────────────────────────────────────────
	// Step 3 is non-blocking: it spawns the worker pool and returns immediately.
	// Step 4 blocks only until CRITICAL tickers (benchmarks + sector ETFs) are
	// fresh; NORMAL tickers continue ingesting while Steps 5 & 6 run.
	steps := []pipelineStep{
		{
			name: "TradingView imports",
			run:  func() error { return runTradingViewImports(ctx, cfg.TVCollectorURL, logger) },
		},
		{
			name: "TradingView snapshot check",
			run:  func() error { return runFundamentalsSnapshot(ctx, tvSnapshotRepo, today, logger) },
		},
		{
			name: "Corporate action fetch (non-fatal)",
			run: func() error {
				caJob := pipelinejobs.NewCorporateActionJob(corporateActionRepo, cfg.PolygonAPIKey, logger)
				if err := caJob.RunCorporateActionJob(ctx); err != nil {
					logger.Warn("Corporate action fetch failed, continuing without", "error", err)
				}
				return nil // never abort pipeline
			},
		},
		{
			name: "Fetch TV priority tickers",
			run: func() error {
				tvPriorityTickers = fetchTVTickers(ctx, tvSnapshotRepo, today, logger)
				return nil
			},
		},
		{
			name: "Start candle ingestion (async)",
			run: func() error {
				h, err := startCandleIngestionAsync(
					ctx, ingestSvc, tickerRepo, candleRepo,
					cfg.MarketDataWorkerCount, rpm, today, tvPriorityTickers, logger,
				)
				if err != nil {
					return err
				}
				ingestionHandle = h
				return nil
			},
		},
		{
			name: "Wait for priority candle ingestion",
			run:  func() error { return waitForPriorityIngestion(ctx, ingestionHandle, logger) },
		},
		{
			name: "Universe snapshot (R-04)",
			run:  func() error { return universeSnapshotJob.RunUniverseSnapshot(ctx, today) },
		},
		{
			name: "Market regime inputs",
			run:  func() error { return regimeJob.RunMarketInputsJob(ctx, today) },
		},
		{
			name: "Market regime classification",
			run: func() error {
				if err := classificationJob.RunMarketRegimeClassificationJob(ctx, today); err != nil {
					return err
				}
				regime, err := marketRegimeRepo.GetByDate(ctx, today)
				if err != nil {
					logger.Warn("Failed to fetch market regime after classification — defaulting gateLevel to full",
						"error", err,
					)
					gateLevel = "full"
				} else {
					gateLevel = regime.GateLevel
					logger.Info("Circuit breaker gate level set",
						"gate_level", gateLevel,
					)
				}
				return nil
			},
		},
		{
			name: "Sector momentum scoring",
			run:  func() error { return sectorMomentumJob.RunSectorMomentumJob(ctx, today) },
		},

		// ── Step 8: Daily ranking lists (gated) ──────────────────────────────
		// GateFull:   all lists (Momentum)
		// GateHalt:   skip entirely
		{
			name: "Daily ranking lists",
			run: func() error {
				switch gateLevel {
				case "halt":
					logger.Info("Circuit breaker HALT — skipping ranking lists")
					return nil
				default: // "full"
					return rankListJob.RunDailyRankListsJob(ctx, today)
				}
			},
		},
	}

	// ── Step 9 (optional): LLM list evaluation (gated) ──────────────────────
	// Non-fatal: failures are logged but never abort the pipeline.
	// Skipped entirely when gate is HALT.
	if llmEvalJob != nil {
		steps = append(steps, pipelineStep{
			name: "LLM list evaluation",
			run: func() error {
				if gateLevel == "halt" {
					logger.Info("Circuit breaker HALT — skipping LLM list evaluation")
					return nil
				}
				if err := llmEvalJob.RunLLMListEvaluationJob(ctx, today); err != nil {
					logger.Error("LLM list evaluation failed (non-fatal)", "error", err)
				}
				return nil // never abort pipeline
			},
		})
	}

	// ── Step 10: Trade outcome calculation (gated) ───────────────────────────
	// Skipped entirely when gate is HALT.
	// ── R-06: Net return computation (gated) ─────────────────────────────────
	// Computes net-of-cost returns, slippage tiers, and ADV caps.
	// Runs after trade outcome calculation populates gross returns.
	// Non-fatal: failures are logged but never abort the pipeline.
	steps = append(steps,
		pipelineStep{
			name: "Trade outcome calculation",
			run: func() error {
				if gateLevel == "halt" {
					logger.Info("Circuit breaker HALT — skipping trade outcome calculation")
					return nil
				}
				if err := tradeOutcomeJob.RunTradeOutcomeJob(ctx, today); err != nil {
					logger.Error("Trade outcome calculation failed (non-fatal)",
						"error", err,
					)
				}
				return nil // never abort pipeline
			},
		},
		pipelineStep{
			name: "Net return computation",
			run: func() error {
				if gateLevel == "halt" {
					logger.Info("Circuit breaker HALT — skipping net return computation")
					return nil
				}
				if err := netReturnJob.RunNetReturnJob(ctx, today); err != nil {
					logger.Error("Net return computation failed (non-fatal)",
						"error", err,
					)
				}
				return nil // never abort pipeline
			},
		},
	)

	// ── Step 12 (optional): Commercial report transformation (gated) ─────────
	// Skipped entirely when gate is HALT.
	if commercialReportJob != nil {
		steps = append(steps, pipelineStep{
			name: "Commercial report transformation",
			run: func() error {
				if gateLevel == "halt" {
					logger.Info("Circuit breaker HALT — skipping commercial report transformation")
					return nil
				}
				if err := commercialReportJob.RunCommercialReportJob(ctx, today, gateLevel); err != nil {
					logger.Error("Commercial report transformation failed (non-fatal)",
						"error", err,
					)
				}
				return nil // never abort pipeline
			},
		})
	}

	if _, err := runPipeline(ctx, steps, logger, nightlyRunRepo, today, gateLevel); err != nil {
		return err
	}

	// ── Post-pipeline: drain remaining background ingestion ───────────────────
	drainBackgroundIngestion(ctx, ingestionHandle, logger)

	// ── Post-drain: Trade outcome calculation ─────────────────────────────────
	// Runs after all NORMAL ticker candles are ingested so T+1 candles are
	// available for the ranked tickers.
	{
		timer := prometheus.NewTimer(metrics.NightlyPipelineDuration.WithLabelValues("nightly", "trade_outcomes"))
		logger.Info("pipeline step starting", "step_num", "post-drain", "step_name", "Trade outcome calculation")
		if err := tradeOutcomeJob.RunTradeOutcomeJob(ctx, today); err != nil {
			logger.Error("Trade outcome calculation failed (non-fatal)", "error", err)
		}
		timer.ObserveDuration()
		logger.Info("pipeline step complete", "step_num", "post-drain", "step_name", "Trade outcome calculation")

		// ── R-04: Effective sample size ratio check ──────────────────────────
		// After trade outcomes, check if dedup is working. If raw_N / effective_N
		// > 1.5, duplication is inflating apparent N — log a WARN.
		checkEffectiveSampleSize(ctx, tradeOutcomeRepo, logger)
	}

	// ── Post-drain: Prompt ticker outcome attribution ────────────────────────
	{
		timer := prometheus.NewTimer(metrics.NightlyPipelineDuration.WithLabelValues("nightly", "prompt_attribution"))
		ptoRepo := repository.NewPromptTickerOutcomeRepo(pool)
		llmEvalRepoForAttribution := repository.NewLLMListEvaluationRepo(pool)
		attributionJob := pipelinejobs.NewPromptOutcomeAttributionJob(
			llmEvalRepoForAttribution, tradeOutcomeRepo, candleRepo, ptoRepo, logger,
		)
		logger.Info("pipeline step starting", "step_num", "post-drain", "step_name", "Prompt outcome attribution")
		if err := attributionJob.RunAttributionJob(ctx, today); err != nil {
			logger.Error("Prompt outcome attribution failed (non-fatal)", "error", err)
		}
		timer.ObserveDuration()
		logger.Info("pipeline step complete", "step_num", "post-drain", "step_name", "Prompt outcome attribution")
	}

	// ── Post-drain: Nightly prompt experiment tracking ───────────────────────
	{
		timer := prometheus.NewTimer(metrics.NightlyPipelineDuration.WithLabelValues("nightly", "prompt_experiment_tracking"))
		ptoRepo := repository.NewPromptTickerOutcomeRepo(pool)
		promptExpRepo := repository.NewPromptExperimentRepo(pool)
		nightlyExpJob := pipelinejobs.NewNightlyPromptExperimentJob(ptoRepo, promptExpRepo, logger)
		logger.Info("pipeline step starting", "step_num", "post-drain", "step_name", "Nightly prompt experiment tracking")
		if err := nightlyExpJob.RunNightlyPromptExperimentJob(ctx); err != nil {
			logger.Error("Nightly prompt experiment tracking failed (non-fatal)", "error", err)
		}
		timer.ObserveDuration()
		logger.Info("pipeline step complete", "step_num", "post-drain", "step_name", "Nightly prompt experiment tracking")
	}

	// ── Post-drain: Prompt memory outcome update (if enabled) ────────────────
	if cfg.PromptMemoryEnabled {
		timer := prometheus.NewTimer(metrics.NightlyPipelineDuration.WithLabelValues("nightly", "prompt_memory_update"))
		memoryRepo := repository.NewPromptMemoryRepo(pool)
		llmEvalRepoForMemory := repository.NewLLMListEvaluationRepo(pool)
		embedSvc := llmsvc.NewEmbeddingService(cfg.OpenAIAPIKey, cfg.LLMBaseURL)
		proRepo := repository.NewPromptTickerOutcomeRepo(pool)
		memoryJob := pipelinejobs.NewPromptMemoryJob(memoryRepo, llmEvalRepoForMemory, proRepo, embedSvc, logger)

		logger.Info("pipeline step starting", "step_num", "post-drain", "step_name", "Prompt memory outcome update")
		if err := memoryJob.UpdateOutcomes(ctx); err != nil {
			logger.Error("Prompt memory outcome update failed (non-fatal)", "error", err)
		}
		timer.ObserveDuration()
		logger.Info("pipeline step complete", "step_num", "post-drain", "step_name", "Prompt memory outcome update")
	}

	return nil
}
