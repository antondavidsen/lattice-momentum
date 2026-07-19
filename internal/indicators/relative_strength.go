package indicators

// ComputeRelativeStrength computes element-wise RS = seriesA[i] / seriesB[i].
//
// Used to measure:
//   - QQQ vs SPY  (Nasdaq leadership relative to broad market)
//   - IWM vs SPY  (small-cap breadth relative to S&P 500)
//   - Any sector ETF vs SPY
//
// When seriesB[i] == 0 the corresponding output element is set to 0 to avoid
// division by zero.
//
// The returned slice has length min(len(seriesA), len(seriesB)); any trailing
// elements in the longer input are silently ignored.
func ComputeRelativeStrength(seriesA, seriesB []float64) []float64 {
	n := len(seriesA)
	if nb := len(seriesB); nb < n {
		n = nb
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		if seriesB[i] == 0 {
			out[i] = 0
			continue
		}
		out[i] = seriesA[i] / seriesB[i]
	}
	return out
}
