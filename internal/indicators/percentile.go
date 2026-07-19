// Package indicators provides pure, composable financial indicator functions.
package indicators

import "sort"

// PercentileRanks returns an aligned slice of percentile ranks [0,1] for the
// input values.  The highest value receives 1.0, the lowest receives 0.0.
// When n == 1, the single element receives 1.0.
//
// Tied values receive the mean of the ordinal ranks they span, so
// [10, 10, 10] → [0.5, 0.5, 0.5] rather than arbitrary distinct ranks.
// This prevents ranking instability for sectors with identical performance.
//
// This function is reusable across the whole project.
func PercentileRanks(values []float64) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []float64{1.0}
	}

	// Build index-value pairs and sort by value ascending.
	type iv struct {
		idx int
		val float64
	}
	pairs := make([]iv, n)
	for i, v := range values {
		pairs[i] = iv{idx: i, val: v}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].val < pairs[j].val })

	// Assign average rank for tied values.
	// After ascending sort, position 0 = worst, position n-1 = best.
	// Groups of identical values share the mean of their ordinal positions.
	out := make([]float64, n)
	for idx := 0; idx < n; {
		// Find the end of this tie group.
		jdx := idx + 1
		for jdx < n && pairs[jdx].val == pairs[idx].val {
			jdx++
		}
		// Average ordinal rank for positions idx..jdx-1.
		var sumRank float64
		for k := idx; k < jdx; k++ {
			sumRank += float64(k)
		}
		avgPctile := (sumRank / float64(jdx-idx)) / float64(n-1)
		for k := idx; k < jdx; k++ {
			out[pairs[k].idx] = avgPctile
		}
		idx = jdx
	}
	return out
}
