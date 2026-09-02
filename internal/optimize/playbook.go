package optimize

import (
	"fmt"
	"strings"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// PlaybookEntry is one suggested action derived from a Finding. Actions are
// starting points for the agent driving the loop; each names what to verify
// so a change can be judged by measurement, not vibes.
type PlaybookEntry struct {
	FindingKind gpuevent.FindingKind `json:"finding_kind"`
	Bound       gpuevent.Bound       `json:"bound,omitempty"`
	Action      string               `json:"action"`
	VerifyWith  string               `json:"verify_with"`
}

// Suggest maps findings to playbook actions in severity order. Every finding
// kind has at least one entry; unknown shapes get a conservative suggestion.
func Suggest(findings []gpuevent.Finding) []PlaybookEntry {
	var out []PlaybookEntry
	for _, f := range findings {
		switch f.Kind {
		case gpuevent.FindingDominance:
			out = append(out, dominanceActions(f)...)
		case gpuevent.FindingLaunchShape:
			out = append(out,
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "batch independent work items into one larger launch instead of many small ones",
					VerifyWith:  "kernel mean duration drops or bound reclassifies away from latency",
				},
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "split sequential loops across blocks; move loop-carried state off the critical path",
					VerifyWith:  "kernel mean duration drops without output change",
				},
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "overlap independent small kernels on separate streams where dependencies allow",
					VerifyWith:  "total kernel time falls; watch for new long-tail findings from contention",
				},
			)
		case gpuevent.FindingLongTail:
			out = append(out,
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "check for input-dependent branching and irregular memory access in the slow launches",
					VerifyWith:  "p95/mean ratio approaches 1 across more iterations",
				},
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "check device clocks and contention during the capture window (gputrace nvidia series)",
					VerifyWith:  "tail disappears at higher iteration counts with stable power/temp",
				},
			)
		case gpuevent.FindingTransferHeavy:
			out = append(out,
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "batch small host<->device copies into fewer larger transfers",
					VerifyWith:  "memcpy count and total memcpy time fall",
				},
				PlaybookEntry{
					FindingKind: f.Kind,
					Action:      "use pinned host memory and keep data resident on-device between kernels",
					VerifyWith:  "transfer-heavy finding clears on re-analyze",
				},
			)
		default:
			out = append(out, PlaybookEntry{
				FindingKind: f.Kind,
				Action:      "inspect the kernel source against its evidence lines before changing anything",
				VerifyWith:  "re-run analyze after any change",
			})
		}
	}
	return out
}

func dominanceActions(f gpuevent.Finding) []PlaybookEntry {
	bound := boundFromEvidence(f)
	switch bound {
	case gpuevent.BoundMemory:
		return []PlaybookEntry{
			{FindingKind: f.Kind, Bound: bound, Action: "use wider vectorized loads (128-bit) and check coalescing so adjacent threads touch adjacent addresses", VerifyWith: "dominant kernel share drops"},
			{FindingKind: f.Kind, Bound: bound, Action: "fuse with adjacent kernels to keep data in registers/shared memory instead of round-tripping through DRAM", VerifyWith: "total kernel time falls; transfer-heavy does not appear"},
			{FindingKind: f.Kind, Bound: bound, Action: "compress data in flight or reduce precision where semantics allow", VerifyWith: "bytes moved per launch drop; outputs within tolerance"},
		}
	case gpuevent.BoundLatency:
		return []PlaybookEntry{
			{FindingKind: f.Kind, Bound: bound, Action: "expose more parallelism first (larger grids, batching); dominance on a tiny grid is a shape problem before it is a kernel problem", VerifyWith: "bound reclassifies away from latency-bound"},
		}
	default:
		return []PlaybookEntry{
			{FindingKind: f.Kind, Bound: bound, Action: "tile for cache/tensor-core reuse before micro-optimizing inner loops", VerifyWith: "dominant kernel share drops"},
			{FindingKind: f.Kind, Bound: bound, Action: "replace hand-rolled math with a vendor library equivalent (cuBLAS/cuDNN/cutlass) where the operation matches", VerifyWith: "kernel disappears from top of report or share drops sharply"},
			{FindingKind: f.Kind, Bound: bound, Action: "reduce redundant launches by batching calls that share inputs", VerifyWith: "launch count falls with unchanged outputs"},
		}
	}
}

// boundFromEvidence recovers the classification from the finding's evidence
// lines so suggestions stay tied to measured shape.
func boundFromEvidence(f gpuevent.Finding) gpuevent.Bound {
	for _, e := range f.Evidence {
		if strings.Contains(e, "grid") && strings.Contains(e, "block") {
			// The metrics engine classifies before writing evidence; recover
			// via the hypothesis wording used for each bound.
			switch {
			case strings.Contains(f.Hypothesis, "bandwidth"):
				return gpuevent.BoundMemory
			case strings.Contains(f.Hypothesis, "tiny grid") || strings.Contains(f.Hypothesis, "too few threads"):
				return gpuevent.BoundLatency
			}
		}
	}
	return gpuevent.BoundUndetermined
}

// RenderSuggestions formats entries as an agent-readable action list.
func RenderSuggestions(entries []PlaybookEntry) string {
	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. %s\n", i+1, e.Action)
		if e.Bound != "" {
			fmt.Fprintf(&b, "   bound: %s\n", e.Bound)
		}
		fmt.Fprintf(&b, "   verify: %s\n", e.VerifyWith)
	}
	return b.String()
}
