package gate

import (
	"fmt"
	"strings"

	"github.com/tmc/gputrace/internal/capture"
	"github.com/tmc/gputrace/internal/cupticapture"
)

// HostProvenance identifies where a capture arm was produced, read from the
// bundle's provenance sidecar (meta.json on CUDA, gputrace-meta.json on
// Metal). Absence means the bundle predates the sidecar or was produced by
// another tool — a cross-session comparison then cannot be labeled.
type HostProvenance struct {
	Recorded bool   `json:"recorded"`
	Hostname string `json:"hostname,omitempty"`
	Platform string `json:"platform,omitempty"`
	Device   string `json:"device,omitempty"`
}

// ReadHostProvenance reads a bundle's host identity from whichever
// provenance sidecar it carries: meta.json (CUDA) or gputrace-meta.json
// (Metal). The zero value means the bundle records no provenance.
func ReadHostProvenance(bundle string) HostProvenance {
	if meta, err := cupticapture.ReadMeta(bundle); err == nil {
		return HostProvenance{
			Recorded: true,
			Hostname: meta.Hostname,
			Platform: "driver " + meta.DriverVersion,
			Device:   meta.GPUName,
		}
	}
	if meta, err := capture.ReadMeta(bundle); err == nil {
		return HostProvenance{
			Recorded: true,
			Hostname: meta.Hostname,
			Platform: meta.OS,
			Device:   meta.Chip,
		}
	}
	return HostProvenance{}
}

func readProvenance(res *Result) HostProvenance {
	return ReadHostProvenance(res.Bundle)
}

// CompareResult stores the residency and data movement delta between two
// captures (Arm A and Arm B).
type CompareResult struct {
	ArmA       *Result  `json:"arm_a"`
	ArmB       *Result  `json:"arm_b"`
	BlitDelta  *int64   `json:"blit_delta,omitempty"`
	HtoDDelta  int      `json:"htod_delta"`
	BytesDelta int64    `json:"bytes_delta"`
	// HostA and HostB label where each arm was captured; a cross-host
	// comparison is noise for timing but still valid for structure.
	HostA HostProvenance `json:"host_a"`
	HostB HostProvenance `json:"host_b"`
	// DispatchDelta is the difference in invariant-matched dispatch counts
	// (arm B minus arm A). Structural counts are load-independent, so any
	// nonzero delta means the two arms did not run the same workload shape.
	DispatchDelta int `json:"dispatch_delta"`
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

	// Structural delta: invariant-matched dispatch counts per arm.
	cr.DispatchDelta = resB.Completeness.MatchedCount - resA.Completeness.MatchedCount

	cr.HostA = readProvenance(resA)
	cr.HostB = readProvenance(resB)

	// Build summary
	var lines []string
	switch {
	case cr.HostA.Recorded && cr.HostB.Recorded && cr.HostA.Hostname != cr.HostB.Hostname:
		lines = append(lines, fmt.Sprintf("CROSS-HOST comparison: arm A on %s (%s), arm B on %s (%s) — timing deltas are noise; structural counts remain comparable",
			cr.HostA.Hostname, cr.HostA.Device, cr.HostB.Hostname, cr.HostB.Device))
	case cr.HostA.Recorded && cr.HostB.Recorded:
		lines = append(lines, fmt.Sprintf("Same host: %s (%s)", cr.HostA.Hostname, cr.HostA.Device))
	default:
		var missing []string
		if !cr.HostA.Recorded {
			missing = append(missing, "arm A")
		}
		if !cr.HostB.Recorded {
			missing = append(missing, "arm B")
		}
		lines = append(lines, fmt.Sprintf("Host provenance absent from %s: cross-session comparison cannot be labeled",
			strings.Join(missing, " and ")))
	}
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

	if resA.Completeness.InvariantSymbol != "" {
		if cr.DispatchDelta != 0 {
			lines = append(lines, fmt.Sprintf("  Delta: %+d %s dispatches (%d vs %d) — arms did not run the same workload shape",
				cr.DispatchDelta, resA.Completeness.InvariantSymbol,
				resA.Completeness.MatchedCount, resB.Completeness.MatchedCount))
		} else if resA.Completeness.MatchedCount > 0 {
			lines = append(lines, fmt.Sprintf("  Delta: 0 %s dispatches (%d in each arm — structurally comparable)",
				resA.Completeness.InvariantSymbol, resA.Completeness.MatchedCount))
		}
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
