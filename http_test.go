package devlite

import "testing"

// TestSampleDecisionAccuracy verifies the accumulator keeps almost exactly
// rate*N requests across a spread of rates. Regression for the collapse
// above 0.5, where `int64(1.0/rate)` truncated to 1 and kept every request.
func TestSampleDecisionAccuracy(t *testing.T) {
	const n = 100_000
	cases := []struct {
		name string
		rate float64
	}{
		{"rate 0.1", 0.1},
		{"rate 0.25", 0.25},
		{"rate 0.5", 0.5},
		{"rate 0.6", 0.6},
		{"rate 0.7", 0.7},
		{"rate 0.8", 0.8},
		{"rate 0.9", 0.9},
		{"rate 0.95", 0.95},
		{"rate 0.99", 0.99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sampleMu.Lock()
			sampleAccum = 0
			sampleMu.Unlock()

			kept := 0
			for i := 0; i < n; i++ {
				if sampleDecision(tc.rate) {
					kept++
				}
			}
			want := float64(n) * tc.rate
			// Bresenham error is < 1 sample; 0.01% tolerance is far looser
			// and still catches the old collapse (diff ~ N*(rate-1)).
			tol := float64(n) * 0.0001
			if diff := float64(kept) - want; diff < -tol || diff > tol {
				t.Fatalf("rate %v: kept %d of %d (want ~%.1f, diff %.0f)", tc.rate, kept, n, want, diff)
			}
		})
	}
}

// TestSampleDecisionBoundaries covers the fixed-rate branches.
func TestSampleDecisionBoundaries(t *testing.T) {
	for _, rate := range []float64{0, -1} {
		if sampleDecision(rate) {
			t.Fatalf("rate %v should never sample", rate)
		}
	}
	for _, rate := range []float64{1, 2} {
		if !sampleDecision(rate) {
			t.Fatalf("rate %v should always sample", rate)
		}
	}
}
