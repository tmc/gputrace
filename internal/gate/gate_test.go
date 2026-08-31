package gate

import (
	"fmt"
	"strings"
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
		{
			name:       "overshoot passes",
			marksCount: 250,
			tokens:     100,
			slack:      2,
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

// An overshoot passed before the count was split in two, so a status-only
// assertion cannot see this defect. What was wrong was the arithmetic and the
// reason: 250 marks against 101 expected reported "-149 missing, within flush
// residual of 2", which is a shortfall claim, a negative count, and a slack
// comparison the overshoot never underwent.
func TestEvaluateCompletenessOvershoot(t *testing.T) {
	marks := make([]uint64, 250)
	for i := range marks {
		marks[i] = uint64(i) * 1000
	}
	res := EvaluateCompleteness("test", marks, "arg_reduce", Options{Tokens: 100, Slack: 2})

	if res.Status != VerdictPass {
		t.Errorf("status = %v, want %v", res.Status, VerdictPass)
	}
	if res.MissingCount < 0 {
		t.Errorf("MissingCount = %d, must never be negative", res.MissingCount)
	}
	if res.MissingCount != 0 {
		t.Errorf("MissingCount = %d, want 0: nothing is missing on an overshoot", res.MissingCount)
	}
	if want := 149; res.ExcessCount != want {
		t.Errorf("ExcessCount = %d, want %d", res.ExcessCount, want)
	}
	if got, want := res.DispatchRatio, 250.0/101.0; got != want {
		t.Errorf("DispatchRatio = %v, want %v", got, want)
	}
	if strings.Contains(res.Reason, "flush residual") {
		t.Errorf("overshoot reason borrows the shortfall wording: %s", res.Reason)
	}
	if !strings.Contains(res.Reason, "overshoot") {
		t.Errorf("overshoot reason does not say it is one: %s", res.Reason)
	}
}

// A shortfall inside the slack keeps reporting a positive missing count, so
// splitting the overshoot out must not disturb the direction that was correct.
func TestEvaluateCompletenessShortfallStillReportsMissing(t *testing.T) {
	marks := make([]uint64, 127)
	for i := range marks {
		marks[i] = uint64(i) * 1000
	}
	res := EvaluateCompleteness("test", marks, "arg_reduce", Options{Tokens: 128, Slack: 2})

	if res.Status != VerdictPass {
		t.Errorf("status = %v, want %v", res.Status, VerdictPass)
	}
	if want := 2; res.MissingCount != want {
		t.Errorf("MissingCount = %d, want %d", res.MissingCount, want)
	}
	if res.ExcessCount != 0 {
		t.Errorf("ExcessCount = %d, want 0 on a shortfall", res.ExcessCount)
	}
	if res.DispatchRatio != 0 {
		t.Errorf("DispatchRatio = %v, want 0 on a shortfall", res.DispatchRatio)
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
