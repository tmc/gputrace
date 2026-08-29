//go:build linux

package cmd

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/optimize"
)

func TestOverheadVerdict(t *testing.T) {
	tests := []struct {
		name        string
		verdict     optimize.Verdict
		overheadPct float64
		effectSize  float64
		wantUsable  bool
		wantReason  string
	}{
		{
			name:        "cost inside noise",
			verdict:     optimize.Equivalent,
			overheadPct: 0.4,
			effectSize:  5,
			wantUsable:  true,
			wantReason:  "inside run-to-run noise",
		},
		{
			// The measurement cannot bound the shim's cost, so it cannot
			// license a claim either way.
			name:        "too noisy to bound",
			verdict:     optimize.NoisyChange,
			overheadPct: 7,
			effectSize:  5,
			wantUsable:  false,
			wantReason:  "too noisy",
		},
		{
			name:        "overhead swamps the effect",
			verdict:     optimize.Regressed,
			overheadPct: 5.7,
			effectSize:  5,
			wantUsable:  false,
			wantReason:  "describe the capture as much as the workload",
		},
		{
			name:        "overhead below the effect",
			verdict:     optimize.Regressed,
			overheadPct: 1.2,
			effectSize:  5,
			wantUsable:  true,
			wantReason:  "below the 5.0% effect",
		},
		{
			// A speedup that large is still a perturbation of this size.
			name:        "negative overhead still counts",
			verdict:     optimize.Improved,
			overheadPct: -8,
			effectSize:  5,
			wantUsable:  false,
			wantReason:  "8.0%",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &OverheadReport{
				Comparison:    &optimize.Comparison{Verdict: tt.verdict},
				OverheadPct:   tt.overheadPct,
				EffectSizePct: tt.effectSize,
			}
			usable, reason := overheadVerdict(rep)
			if usable != tt.wantUsable {
				t.Errorf("usable = %v, want %v (%s)", usable, tt.wantUsable, reason)
			}
			if !strings.Contains(reason, tt.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", reason, tt.wantReason)
			}
		})
	}
}

func TestOverheadMode(t *testing.T) {
	tests := []struct {
		opts overheadOptions
		want string
	}{
		{overheadOptions{}, "activity"},
		{overheadOptions{api: true}, "activity+api"},
		{overheadOptions{api: true, nvtx: true}, "activity+api+nvtx"},
	}
	for _, tt := range tests {
		if got := overheadMode(&tt.opts); got != tt.want {
			t.Errorf("overheadMode(%+v) = %q, want %q", tt.opts, got, tt.want)
		}
	}
}
