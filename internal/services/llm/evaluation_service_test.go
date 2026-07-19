package llm

import (
	"context"
	"testing"
	"time"

	"ai-stock-service/internal/models"

	"github.com/stretchr/testify/assert"
)

// ── Mock MemorySource ─────────────────────────────────────────────────────────

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestBuildRAGContextString_Empty(t *testing.T) {
	result := buildRAGContextString(nil)
	assert.Empty(t, result, "nil memories should produce empty string")

	result = buildRAGContextString([]models.PromptMemory{})
	assert.Empty(t, result, "empty memories should produce empty string")
}

func TestBuildRAGContextString_WithMemories(t *testing.T) {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	return5D := 0.12
	stopHit := false
	targetHit := true
	setup := "Bull flag on daily"
	conviction := "High conviction"

	memories := []models.PromptMemory{
		{
			Date:             now.AddDate(0, -6, 0),
			Ticker:           "AAPL",
			ListType:         models.ListTypeEP,
			ContextSummary:   "Bull flag on daily, strong volume",
			LLMSetup:         &setup,
			LLMConviction:    &conviction,
			OutcomeReturn5D:  &return5D,
			OutcomeStopHit:   &stopHit,
			OutcomeTargetHit: &targetHit,
		},
	}

	result := buildRAGContextString(memories)
	assert.Contains(t, result, "PAST SIMILAR EVALUATIONS")
	assert.Contains(t, result, "AAPL")
	assert.Contains(t, result, "Bull flag")
	assert.Contains(t, result, "+12.0%")
	assert.Contains(t, result, "High conviction")
}

func TestBuildRAGContextString_WithNilOutcomes(t *testing.T) {
	now := time.Now().UTC().Truncate(24 * time.Hour)

	memories := []models.PromptMemory{
		{
			Date:           now.AddDate(0, -3, 0),
			Ticker:         "MSFT",
			ListType:       models.ListTypeMomentum,
			ContextSummary: "Strong momentum breakout",
		},
	}

	result := buildRAGContextString(memories)
	assert.Contains(t, result, "MSFT")
	assert.Contains(t, result, "Strong momentum")
	// Nil outcome fields should not produce output lines.
	assert.NotContains(t, result, "Return 5D")
	assert.NotContains(t, result, "Stop hit")
}

func TestBuildRAGContextString_MultipleMemories(t *testing.T) {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	return5D := 0.05

	memories := []models.PromptMemory{
		{
			Date:            now.AddDate(0, -6, 0),
			Ticker:          "AAPL",
			ListType:        models.ListTypeEP,
			ContextSummary:  "First memory",
			OutcomeReturn5D: &return5D,
		},
		{
			Date:           now.AddDate(0, -3, 0),
			Ticker:         "MSFT",
			ListType:       models.ListTypeMomentum,
			ContextSummary: "Second memory",
		},
	}

	result := buildRAGContextString(memories)
	assert.Contains(t, result, "Memory 1")
	assert.Contains(t, result, "Memory 2")
	assert.Contains(t, result, "AAPL")
	assert.Contains(t, result, "MSFT")
}

func TestBuildRAGContextSummary_ReturnsZeroVector(t *testing.T) {
	// Verify the placeholder returns a 1536-dim zero vector.
	c := 150.0
	snap := models.TradingViewSnapshotDaily{
		Ticker: "NVDA",
		Close:  &c,
	}
	regime := &models.MarketRegimeDaily{Regime: "bull"}

	vec := buildRAGContextSummary(context.Background(), []models.TradingViewSnapshotDaily{snap}, regime, models.ListTypeEP, nil, nil)
	assert.Equal(t, 1536, len(vec.Slice()), "should return 1536-dim vector")

	// Verify it's a zero vector.
	for _, v := range vec.Slice() {
		assert.Equal(t, float32(0), v, "all elements should be zero")
	}
}

func TestEvaluationService_RAGDisabled(t *testing.T) {
	// When memory is nil, EvaluateList should not call FindSimilar.
	// We test this by verifying the RAG context block is not in the rendered prompt.
	// This test uses renderUserPrompt directly since we can't easily mock the LLM provider.

	c := 150.0
	relVol := 1.5
	snap := models.TradingViewSnapshotDaily{
		Ticker:         "NVDA",
		Close:          &c,
		RelativeVolume: &relVol,
	}

	// Without RAG context (empty string), the {{rag_context}} placeholder
	// should be replaced with "No RAG context available".
	result, _ := renderUserPrompt(
		"## CONTEXT\nAnalysis for today.\n\n## EP SCREENER OUTPUT — TOP 10\n[PASTE 10 ROWS: ticker, price]\n\n---\n\n## YOUR ANALYSIS TASK\nDo analysis.\n\n{{rag_context}}",
		time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		models.ListTypeEP,
		[]models.TradingViewSnapshotDaily{snap},
		nil, nil, nil, nil, "",
	)

	assert.Contains(t, result, "No RAG context available")
	assert.NotContains(t, result, "PAST SIMILAR EVALUATIONS")
}

func TestEvaluationService_RAGEnabled(t *testing.T) {
	// When memory is non-nil and returns memories, the RAG context block
	// should be injected into the prompt.
	c := 150.0
	relVol := 1.5
	snap := models.TradingViewSnapshotDaily{
		Ticker:         "NVDA",
		Close:          &c,
		RelativeVolume: &relVol,
	}

	now := time.Now().UTC().Truncate(24 * time.Hour)
	return5D := 0.12
	memories := []models.PromptMemory{
		{
			Date:            now.AddDate(0, -6, 0),
			Ticker:          "AAPL",
			ListType:        models.ListTypeMomentum,
			ContextSummary:  "Bull flag on daily, strong volume",
			OutcomeReturn5D: &return5D,
		},
	}

	ragContext := buildRAGContextString(memories)
	assert.NotEmpty(t, ragContext, "RAG context should be non-empty with memories")

	result, _ := renderUserPrompt(
		"## CONTEXT\nAnalysis for today.\n\n## EP SCREENER OUTPUT — TOP 10\n[PASTE 10 ROWS: ticker, price]\n\n---\n\n## YOUR ANALYSIS TASK\nDo analysis.\n\n{{rag_context}}",
		time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		models.ListTypeEP,
		[]models.TradingViewSnapshotDaily{snap},
		nil, nil, nil, nil, ragContext,
	)

	assert.Contains(t, result, "PAST SIMILAR EVALUATIONS")
	assert.Contains(t, result, "AAPL")
	assert.Contains(t, result, "+12.0%")
	assert.NotContains(t, result, "No RAG context available")
}

func TestBuildRAGContextSummary_EmptySnaps(t *testing.T) {
	vec := buildRAGContextSummary(context.Background(), nil, nil, models.ListTypeEP, nil, nil)
	assert.Equal(t, 1536, len(vec.Slice()), "should return 1536-dim vector even with nil snaps")
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is a ..."},
		{"exactly ten", 11, "exactly ten"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateString(tt.input, tt.maxLen)
		assert.Equal(t, tt.want, got, "truncateString(%q, %d)", tt.input, tt.maxLen)
	}
}
