package gate

import (
	"fmt"
	"strings"
)

// CompareResult stores the residency and data movement delta between two
// captures (Arm A and Arm B).
type CompareResult struct {
	ArmA       *Result  `json:"arm_a"`
	ArmB       *Result  `json:"arm_b"`
	BlitDelta  *int64   `json:"blit_delta,omitempty"`
	HtoDDelta  int      `json:"htod_delta"`
	BytesDelta int64    `json:"bytes_delta"`
	Notes      []string `json:"notes,omitempty"`
	Summary    string   `json:"summary"`
}

// Compare evaluates two bundles and compares their staging/residency observations.
func Compare(bundleA, bundleB string, opts Options) (*CompareResult, error) {
	resA, err := Evaluate(bundleA, opts)
	if err != nil {
		return nil, fmt.Errorf("evaluate arm A (%s): %w", bundleA, err)
	}
	resB, err := Evaluate(bundleB, opts)
	if err != nil {
		return nil, fmt.Errorf("evaluate arm B (%s): %w", bundleB, err)
	}

	cr := &CompareResult{
		ArmA: resA,
		ArmB: resB,
	}

	// Compare Staging
	// Blit delta
	if resA.Staging.BlitCalls != nil && resB.Staging.BlitCalls != nil {
		delta := *resB.Staging.BlitCalls - *resA.Staging.BlitCalls
		cr.BlitDelta = &delta
	}

	// HtoD delta
	cr.HtoDDelta = resB.Staging.HtoDTransfers - resA.Staging.HtoDTransfers
	cr.BytesDelta = int64(resB.Staging.HtoDBytes) - int64(resA.Staging.HtoDBytes)

	// Build summary
	var lines []string
	lines = append(lines, fmt.Sprintf("Residency / Staging Comparison:\n  Arm A (%s, %s):\n    %s\n  Arm B (%s, %s):\n    %s",
		resA.Bundle, resA.Backend, resA.Staging.Summary,
		resB.Bundle, resB.Backend, resB.Staging.Summary))

	if cr.BlitDelta != nil {
		if *cr.BlitDelta > 0 {
			lines = append(lines, fmt.Sprintf("  Delta: +%d blit calls (increased staging in Arm B)", *cr.BlitDelta))
		} else if *cr.BlitDelta < 0 {
			lines = append(lines, fmt.Sprintf("  Delta: %d blit calls (reduced staging in Arm B)", *cr.BlitDelta))
		} else {
			lines = append(lines, "  Delta: 0 blit calls (identical staging count)")
		}
	} else if resA.Staging.Recorded != resB.Staging.Recorded {
		lines = append(lines, "  Delta: staging data recorded in one arm but absent in other")
	}

	if resA.Backend == "cuda" || resB.Backend == "cuda" {
		if cr.HtoDDelta != 0 || cr.BytesDelta != 0 {
			lines = append(lines, fmt.Sprintf("  Delta: %+d transfers (%+.2f MB)",
				cr.HtoDDelta, float64(cr.BytesDelta)/1e6))
		}
	}

	cr.Summary = strings.Join(lines, "\n")
	return cr, nil
}
