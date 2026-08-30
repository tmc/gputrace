package gate

import (
	"fmt"
	"testing"
)

func TestEvaluateCompleteness(t *testing.T) {
	tests := []struct {
		name       string
		marksCount int
		tokens     int
		slack      int
		exact      bool
		wantStatus Verdict
	}{
		{
			name:       "exact match with prefill",
			marksCount: 129,
			tokens:     128,
			slack:      2,
			wantStatus: VerdictPass,
		},
		{
			name:       "within slack",
			marksCount: 127,
			tokens:     128,
			slack:      2,
			wantStatus: VerdictPass,
		},
		{
			name:       "beyond slack failure",
			marksCount: 102,
			tokens:     128,
			slack:      2,
			wantStatus: VerdictFail,
		},
		{
			name:       "zero matches not evaluable",
			marksCount: 0,
			tokens:     128,
			slack:      2,
			wantStatus: VerdictNotEvaluable,
		},
		{
			name:       "zero tokens unscored",
			marksCount: 129,
			tokens:     0,
			slack:      2,
			wantStatus: VerdictNotEvaluable,
		},
		{
			name:       "exact tokens option",
			marksCount: 128,
			tokens:     128,
			exact:      true,
			wantStatus: VerdictPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := make([]uint64, tt.marksCount)
			opts := Options{
				Tokens:      tt.tokens,
				Slack:       tt.slack,
				ExactTokens: tt.exact,
			}
			res := EvaluateCompleteness("test", marks, "arg_reduce", opts)
			if res.Status != tt.wantStatus {
				t.Errorf("EvaluateCompleteness() status = %v, want %v (reason: %s)", res.Status, tt.wantStatus, res.Reason)
			}
		})
	}
}

func TestEvaluateStationarity(t *testing.T) {
	tests := []struct {
		name       string
		gapsMS     []float64
		threshold  float64
		blockSize  int
		wantStatus Verdict
	}{
		{
			name: "flat trajectory passes",
			gapsMS: []float64{
				7.15, 7.14, 6.82, 6.72, 6.57, 6.56, 6.73, 6.77,
				7.10, 7.12, 6.80, 6.75, 6.60, 6.58, 6.70, 6.75,
				7.15, 7.14, 6.82, 6.72, 6.57, 6.56, 6.73, 6.77,
				7.10, 7.12, 6.80, 6.75, 6.60, 6.58, 6.70, 6.75,
			},
			threshold:  0.15,
			blockSize:  8,
			wantStatus: VerdictPass,
		},
		{
			name: "mid-run excursion fails",
			gapsMS: []float64{
				6.77, 7.15, 6.82, 6.72, 6.57, 6.56, 6.73, 6.77, // block 1 ~6.75
				6.80, 7.12, 6.80, 6.75, 6.60, 6.58, 6.70, 6.75, // block 2 ~6.73
				14.44, 14.50, 14.20, 15.10, 14.80, 14.90, 14.60, 14.70, // block 3 ~14.65 (2.2x excursion)
				6.77, 7.15, 6.82, 6.72, 6.57, 6.56, 6.73, 6.77, // block 4 ~6.75
			},
			threshold:  0.15,
			blockSize:  8,
			wantStatus: VerdictFail,
		},
		{
			name: "too few tokens not evaluable",
			gapsMS: []float64{
				7.0, 7.0, 7.0, 7.0,
			},
			threshold:  0.15,
			blockSize:  16,
			wantStatus: VerdictNotEvaluable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := make([]uint64, len(tt.gapsMS)+1)
			cur := uint64(1000000000)
			marks[0] = cur
			for i, g := range tt.gapsMS {
				cur += uint64(g * 1e6)
				marks[i+1] = cur
			}
			opts := Options{
				StationarityThreshold: tt.threshold,
				BlockSize:             tt.blockSize,
			}
			res := EvaluateStationarity(marks, opts)
			if res.Status != tt.wantStatus {
				t.Errorf("EvaluateStationarity() status = %v, want %v (reason: %s)", res.Status, tt.wantStatus, res.Reason)
			}
		})
	}
}

func ExampleEvaluateCompleteness() {
	marks := make([]uint64, 129)
	opts := Options{Tokens: 128, Slack: 2}
	res := EvaluateCompleteness("qwen3", marks, "arg_reduce", opts)
	fmt.Printf("%s: %s\n", res.Status, res.Reason)
	// Output:
	// PASS: completeness ok    129/129 arg_reduce (want 129 = 128 tokens + 1 prefill)
}
