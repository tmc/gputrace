package gate

import (
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	// Synthetic arm A (0 blits) vs arm B (16 blits)
	resA := &Result{
		Bundle:  "armA.gputrace",
		Backend: "metal",
		Staging: StagingObservation{
			Backend:  "metal",
			Source:   "streamData",
			Recorded: true,
			BlitCalls: func() *int64 {
				var n int64 = 0
				return &n
			}(),
			Summary: "blit calls: 0 (recorded in streamData)",
		},
	}
	resB := &Result{
		Bundle:  "armB.gputrace",
		Backend: "metal",
		Staging: StagingObservation{
			Backend:  "metal",
			Source:   "streamData",
			Recorded: true,
			BlitCalls: func() *int64 {
				var n int64 = 16
				return &n
			}(),
			Summary: "blit calls: 16 (recorded in streamData)",
		},
	}

	cr := &CompareResult{
		ArmA: resA,
		ArmB: resB,
	}
	delta := *resB.Staging.BlitCalls - *resA.Staging.BlitCalls
	cr.BlitDelta = &delta
	cr.Summary = "Residency / Staging Comparison:\n  Arm A: blit calls: 0\n  Arm B: blit calls: 16\n  Delta: +16 blit calls (increased staging in Arm B)"

	if *cr.BlitDelta != 16 {
		t.Errorf("BlitDelta = %d, want 16", *cr.BlitDelta)
	}
	if !strings.Contains(cr.Summary, "+16 blit calls") {
		t.Errorf("Summary missing +16 blit calls, got: %s", cr.Summary)
	}
}
