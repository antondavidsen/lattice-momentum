// Package learning provides shared ML primitives for the nightly weight refit
// pipeline.  All implementations are pure Go — no external ML SDKs.
package learning

import (
	"fmt"
	"math"
	"sort"
)

// LogisticRegressionResult holds fitted weights and performance.
type LogisticRegressionResult struct {
	Weights []float64
	AUC     float64
}

// RunLogisticRegression trains L2-regularized logistic regression with
// non-negative weight constraints via gradient descent.
//
// Inputs:
//   - X: feature matrix (n_samples × n_features), row-major flattened
//   - y: binary labels (0/1)
//   - lambda: L2 regularization strength
//
// Returns fitted weights (normalised to sum=1) and test AUC, or error.
func RunLogisticRegression(x []float64, nSamples, nFeatures int, y []float64, lambda float64) (*LogisticRegressionResult, error) {
	if nSamples == 0 || nFeatures == 0 {
		return nil, fmt.Errorf("RunLogisticRegression: empty data (%d samples, %d features)", nSamples, nFeatures)
	}
	if len(x) != nSamples*nFeatures {
		return nil, fmt.Errorf("RunLogisticRegression: x len %d != %d×%d", len(x), nSamples, nFeatures)
	}
	if len(y) != nSamples {
		return nil, fmt.Errorf("RunLogisticRegression: y len %d != %d samples", len(y), nSamples)
	}

	// Initialise weights to equal values.
	w := make([]float64, nFeatures)
	for i := range w {
		w[i] = 1.0 / float64(nFeatures)
	}

	learningRate := 0.1 // learning rate
	n := float64(nSamples)

	for epoch := 0; epoch < 1000; epoch++ {
		grad := make([]float64, nFeatures)
		for si := 0; si < nSamples; si++ {
			z := dot(w, x[si*nFeatures:(si+1)*nFeatures])
			p := sigmoid(z)
			err := p - y[si]
			for j := 0; j < nFeatures; j++ {
				grad[j] += err * x[si*nFeatures+j]
			}
		}
		for j := 0; j < nFeatures; j++ {
			grad[j] = grad[j]/n + lambda*w[j]
			w[j] -= learningRate * grad[j]
			// Non-negative constraint.
			if w[j] < 0 {
				w[j] = 0
			}
		}
	}

	// Normalise to sum = 1.
	normalised := normaliseWeights(w)

	// Compute AUC on the same data (in-sample evaluation).
	auc := computeAUC(x, nSamples, nFeatures, y, normalised)

	return &LogisticRegressionResult{
		Weights: normalised,
		AUC:     auc,
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func dot(w, x []float64) float64 {
	s := 0.0
	for i := range w {
		s += w[i] * x[i]
	}
	return s
}

func sigmoid(z float64) float64 {
	return 1.0 / (1.0 + math.Exp(-z))
}

func normaliseWeights(w []float64) []float64 {
	total := 0.0
	for _, v := range w {
		total += v
	}
	out := make([]float64, len(w))
	if total <= 0 {
		eq := 1.0 / float64(len(w))
		for i := range out {
			out[i] = eq
		}
		return out
	}
	for i, v := range w {
		out[i] = v / total
	}
	return out
}

// computeAUC computes the area under the ROC curve using the Mann-Whitney U
// statistic approach (equivalent to the trapezoidal method).
func computeAUC(x []float64, nSamples, nFeatures int, y, w []float64) float64 {
	type pred struct {
		score float64
		label float64
	}
	preds := make([]pred, nSamples)
	for si := 0; si < nSamples; si++ {
		score := dot(w, x[si*nFeatures:(si+1)*nFeatures])
		preds[si] = pred{score, y[si]}
	}
	sort.Slice(preds, func(i, j int) bool { return preds[i].score > preds[j].score })

	var totalPos, totalNeg float64
	for _, p := range preds {
		if p.label > 0.5 {
			totalPos++
		} else {
			totalNeg++
		}
	}
	if totalPos == 0 || totalNeg == 0 {
		return 0.5
	}

	var tp, fp, prevTP, prevFP float64
	auc := 0.0
	prevScore := math.Inf(1)
	for _, p := range preds {
		if p.score != prevScore {
			auc += trapezoid(fp/totalNeg, prevFP/totalNeg, tp/totalPos, prevTP/totalPos)
			prevScore = p.score
			prevTP = tp
			prevFP = fp
		}
		if p.label > 0.5 {
			tp++
		} else {
			fp++
		}
	}
	auc += trapezoid(fp/totalNeg, prevFP/totalNeg, tp/totalPos, prevTP/totalPos)
	return auc
}

func trapezoid(x1, x2, y1, y2 float64) float64 {
	return math.Abs(x1-x2) * (y1 + y2) / 2
}
