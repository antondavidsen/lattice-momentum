package regime

import (
	"context"
	"log/slog"
	"time"

	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/models"
)

// GateLevel controls which pipeline steps execute based on market conditions.
type GateLevel int

// GateLevel constants.
const (
	GateFull   GateLevel = iota // All three ranking lists run
	GateEPOnly                  // Only catalyst-driven (EP) list runs
	GateHalt                    // No ranking lists run; report says "sit out"
)

// String returns the human-readable gate level name.
func (g GateLevel) String() string {
	switch g {
	case GateFull:
		return "full"
	case GateEPOnly:
		return "ep_only"
	case GateHalt:
		return "halt"
	default:
		return "unknown"
	}
}

// regimeRepoInterface is the subset of MarketRegimeRepo needed by CircuitBreaker.
type regimeRepoInterface interface {
	GetMarketRegimeDaily(ctx context.Context, date time.Time) (*models.MarketRegimeDaily, error)
}

// vixSource provides the VIX level for a given date.
type vixSource interface {
	GetMarketInputs(ctx context.Context, date time.Time) (*models.MarketInputsDaily, error)
}

// CircuitBreaker evaluates market conditions and returns a gate level that
// controls which pipeline steps should execute.
type CircuitBreaker struct {
	regimeRepo regimeRepoInterface
	vixRepo    vixSource
	log        *slog.Logger
}

// NewCircuitBreaker creates a CircuitBreaker.
func NewCircuitBreaker(regimeRepo regimeRepoInterface, vixRepo vixSource, log *slog.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		regimeRepo: regimeRepo,
		vixRepo:    vixRepo,
		log:        log,
	}
}

// Evaluate reads today's market regime and VIX, then returns the appropriate
// gate level.
//
// Gate logic:
//
//	Regime strong_bull or bull          → FULL
//	Regime neutral                      → FULL
//	Regime correction AND VIX < 28      → EP_ONLY
//	Regime correction AND VIX >= 28     → HALT
//	Regime bear                         → HALT
//	Regime computation failed           → HALT (safe default)
func (cb *CircuitBreaker) Evaluate(ctx context.Context, date time.Time) (GateLevel, error) {
	regime, err := cb.regimeRepo.GetMarketRegimeDaily(ctx, date)
	if err != nil {
		cb.log.Warn("CircuitBreaker: regime fetch failed, defaulting to HALT",
			"date", date.Format("2006-01-02"), "error", err,
		)
		metrics.PipelineGateTotal.WithLabelValues("halt").Inc()
		return GateHalt, nil // safe default, do NOT propagate error
	}

	// Fetch VIX level from market inputs.
	vix := 0.0
	if inputs, inputsErr := cb.vixRepo.GetMarketInputs(ctx, date); inputsErr != nil {
		cb.log.Warn("CircuitBreaker: VIX fetch failed, assuming VIX=0",
			"date", date.Format("2006-01-02"), "error", inputsErr,
		)
	} else if inputs.VIXLevel != nil {
		vix = *inputs.VIXLevel
	}
	// When VIXLevel is nil (data unavailable), vix stays 0.0 which is below
	// the 28 threshold, so the gate defaults to EP_ONLY in correction regime.

	var level GateLevel
	switch regime.Regime {
	case "strong_bull", "bull", "neutral":
		level = GateFull
	case "correction":
		if vix < 28 {
			level = GateEPOnly
		} else {
			level = GateHalt
		}
	case "bear":
		level = GateHalt
	default:
		level = GateHalt
	}

	metrics.PipelineGateTotal.WithLabelValues(level.String()).Inc()
	cb.log.Info("CircuitBreaker: gate evaluated",
		"date", date.Format("2006-01-02"),
		"regime", regime.Regime,
		"vix", vix,
		"gate_level", level.String(),
	)
	return level, nil
}
