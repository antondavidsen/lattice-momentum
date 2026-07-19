package regime

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

// ── Mocks ──────────────────────────────────────────────────────────────────────

type mockRegimeRepo struct {
	regime *models.MarketRegimeDaily
	err    error
}

func (m *mockRegimeRepo) GetMarketRegimeDaily(_ context.Context, _ time.Time) (*models.MarketRegimeDaily, error) {
	return m.regime, m.err
}

type mockVIXSource struct {
	inputs *models.MarketInputsDaily
	err    error
}

func (m *mockVIXSource) GetMarketInputs(_ context.Context, _ time.Time) (*models.MarketInputsDaily, error) {
	return m.inputs, m.err
}

func vixPtr(v float64) *float64 { return &v }

func newMockCircuitBreaker(regime *models.MarketRegimeDaily, regimeErr error, vix float64, vixErr error) *CircuitBreaker {
	return NewCircuitBreaker(
		&mockRegimeRepo{regime: regime, err: regimeErr},
		&mockVIXSource{
			inputs: &models.MarketInputsDaily{VIXLevel: vixPtr(vix)},
			err:    vixErr,
		},
		slog.Default(),
	)
}

// ── Tests ──────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_Evaluate_StrongBull_AnyVIX(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "strong_bull"}, nil, 35.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateFull {
		t.Fatalf("expected GateFull, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Bull_AnyVIX(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "bull"}, nil, 30.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateFull {
		t.Fatalf("expected GateFull, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Neutral_AnyVIX(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "neutral"}, nil, 25.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateFull {
		t.Fatalf("expected GateFull, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Correction_VIXBelow28(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "correction"}, nil, 27.9, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateEPOnly {
		t.Fatalf("expected GateEPOnly, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Correction_VIXAt28(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "correction"}, nil, 28.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateHalt {
		t.Fatalf("expected GateHalt, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Correction_VIXAbove28(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "correction"}, nil, 35.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateHalt {
		t.Fatalf("expected GateHalt, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_Bear_AnyVIX(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "bear"}, nil, 40.0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != GateHalt {
		t.Fatalf("expected GateHalt, got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_RegimeRepoError(t *testing.T) {
	cb := newMockCircuitBreaker(
		nil, assertAnError{}, 0, nil,
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("expected nil error (safe default), got %v", err)
	}
	if level != GateHalt {
		t.Fatalf("expected GateHalt (safe default), got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_VIXRepoError(t *testing.T) {
	cb := newMockCircuitBreaker(
		&models.MarketRegimeDaily{Regime: "correction"}, nil, 0, assertAnError{},
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("expected nil error (safe default), got %v", err)
	}
	// VIX fetch fails → VIX=0 → correction with VIX<28 → EP_ONLY
	if level != GateEPOnly {
		t.Fatalf("expected GateEPOnly (VIX=0 on error), got %s", level)
	}
}

func TestCircuitBreaker_Evaluate_MissingRegimeData(t *testing.T) {
	// Simulate regime repo returning nil regime (missing data).
	cb := NewCircuitBreaker(
		&mockRegimeRepo{regime: nil, err: assertAnError{}},
		&mockVIXSource{inputs: &models.MarketInputsDaily{VIXLevel: vixPtr(0)}, err: nil},
		slog.Default(),
	)
	level, err := cb.Evaluate(context.Background(), time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("expected nil error (safe default), got %v", err)
	}
	if level != GateHalt {
		t.Fatalf("expected GateHalt (safe default), got %s", level)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// assertAnError implements the error interface for mock error returns.
type assertAnError struct{}

func (assertAnError) Error() string { return "mock error" }
