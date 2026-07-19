package indicators

import (
	"math"
	"testing"
)

// ── TestPercentileRanks ───────────────────────────────────────────────────────
// Table-driven test covering:
//   - sorted ascending     → lowest gets 0.0, highest gets 1.0
//   - reverse order        → ranks are inverted
//   - identical values     → average rank (prevents divide-by-zero / instability)
//   - empty / single       → edge cases
//   - partial ties         → only the tied subset shares a rank
//   - two elements         → simplest possible ranking

func TestPercentileRanks(t *testing.T) {
	tests := []struct {
		name   string
		input  []float64
		expect []float64 // nil means "expect nil output"
	}{
		{
			name:   "sorted ascending [10,20,30,40,50]",
			input:  []float64{10, 20, 30, 40, 50},
			expect: []float64{0.0, 0.25, 0.5, 0.75, 1.0},
		},
		{
			name:   "reverse order [50,40,30,20,10]",
			input:  []float64{50, 40, 30, 20, 10},
			expect: []float64{1.0, 0.75, 0.5, 0.25, 0.0},
		},
		{
			name:   "identical values [10,10,10] → average rank 0.5",
			input:  []float64{10, 10, 10},
			expect: []float64{0.5, 0.5, 0.5},
		},
		{
			name:   "empty slice → nil",
			input:  nil,
			expect: nil,
		},
		{
			name:   "single element → 1.0",
			input:  []float64{42},
			expect: []float64{1.0},
		},
		{
			name:   "two elements [1,2]",
			input:  []float64{1, 2},
			expect: []float64{0.0, 1.0},
		},
		{
			name:   "two identical elements [5,5] → both 0.5",
			input:  []float64{5, 5},
			expect: []float64{0.5, 0.5},
		},
		{
			name:   "partial ties [10,20,20,30] → middle pair shares rank",
			input:  []float64{10, 20, 20, 30},
			expect: []float64{0.0, 0.5, 0.5, 1.0},
		},
		{
			name:   "tie at bottom [5,5,10,20]",
			input:  []float64{5, 5, 10, 20},
			expect: []float64{1.0 / 6.0, 1.0 / 6.0, 2.0 / 3.0, 1.0},
		},
		{
			name:   "tie at top [10,20,30,30]",
			input:  []float64{10, 20, 30, 30},
			expect: []float64{0.0, 1.0 / 3.0, 5.0 / 6.0, 5.0 / 6.0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PercentileRanks(tc.input)

			if tc.expect == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}

			if len(got) != len(tc.expect) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.expect))
			}

			for i, want := range tc.expect {
				if math.Abs(got[i]-want) > 1e-9 {
					t.Errorf("rank[%d] = %.6f, want %.6f", i, got[i], want)
				}
			}
		})
	}
}

// TestPercentileRanks_SumInvariant verifies that the sum of all ranks for N
// distinct values always equals N/2. This is a useful mathematical invariant
// that catches off-by-one bugs in the ranking formula.
func TestPercentileRanks_SumInvariant(t *testing.T) {
	values := []float64{3, 1, 4, 1, 5, 9, 2, 6}
	ranks := PercentileRanks(values)

	// For N values with possible ties, the sum of percentile ranks should
	// still sum to N * 0.5 (each position contributes its share).
	n := float64(len(ranks))
	var sum float64
	for _, r := range ranks {
		sum += r
	}
	// Expected sum = (0 + 1 + ... + n-1) / (n-1) = n/2
	expectedSum := n / 2.0
	if math.Abs(sum-expectedSum) > 1e-9 {
		t.Errorf("sum of ranks = %v, want %v", sum, expectedSum)
	}
}

// TestPercentileRanks_TieBreaking verifies that tied values receive identical
// percentile ranks using the mean of their ordinal positions.
func TestPercentileRanks_TieBreaking(t *testing.T) {
	// Two ETFs with identical perf_3m values
	values := []float64{0.05, 0.12, 0.12, 0.08, 0.15}
	// Sorted by value: 0.05(@0), 0.08(@3), 0.12(@1), 0.12(@2), 0.15(@4)
	// Ordinal ranks (0-based): 0, 1, 2, 3, 4
	// Ties at positions 2,3 (both 0.12): mean of rank 2 and rank 3 = 2.5
	// Both should receive percentile: 2.5/4 = 0.625

	got := PercentileRanks(values)

	// Expected:
	// Index 0 (0.05):  rank=0 -> 0/4 = 0.0
	// Index 3 (0.08):  rank=1 -> 1/4 = 0.25
	// Index 1 (0.12):  rank=2.5 -> 2.5/4 = 0.625
	// Index 2 (0.12):  rank=2.5 -> 2.5/4 = 0.625
	// Index 4 (0.15):  rank=4 -> 4/4 = 1.0

	want := []float64{0.0, 0.625, 0.625, 0.25, 1.0}

	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}

	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-10 {
			t.Errorf("PercentileRanks()[%d]: got %.6f, want %.6f", i, got[i], want[i])
		}
	}

	// Explicitly verify tied elements have identical ranks
	if got[1] != got[2] {
		t.Errorf("Tied values should have identical percentile ranks: got %.6f and %.6f", got[1], got[2])
	}
}
