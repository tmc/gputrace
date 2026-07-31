package difftrace

import (
	"fmt"
	"io"
	"sort"

	"github.com/tmc/gputrace/internal/fmtutil"
)

// Comparing two traces by function does not need a profiled export. Which
// kernels ran, and how many times each, is a fact about the command stream,
// and the raw capture carries it. Without this a raw diff can only report one
// scalar -- "dispatch count delta -92" -- which says a difference exists and
// nothing about where it lives.
//
// The view that explains a delta is the one that shows a family of related
// kernels appearing together on one side.

// StructuralFunctionDeltas compares two per-function dispatch count maps.
// Functions absent from one side appear with a zero count, so an A-only
// kernel and one that merely ran less often are the same kind of row and sort
// together. The result is ordered by the size of the difference, then by name
// so equal deltas do not reorder between runs.
func StructuralFunctionDeltas(a, b map[string]int) []FunctionDelta {
	names := make(map[string]bool, len(a)+len(b))
	for name := range a {
		names[name] = true
	}
	for name := range b {
		names[name] = true
	}

	deltas := make([]FunctionDelta, 0, len(names))
	for name := range names {
		countA, countB := a[name], b[name]
		if countA == 0 && countB == 0 {
			continue
		}
		deltas = append(deltas, FunctionDelta{
			FunctionName:       name,
			DispatchCountA:     countA,
			DispatchCountB:     countB,
			DispatchCountDelta: countA - countB,
		})
	}

	sort.Slice(deltas, func(i, j int) bool {
		di, dj := abs(deltas[i].DispatchCountDelta), abs(deltas[j].DispatchCountDelta)
		if di != dj {
			return di > dj
		}
		return deltas[i].FunctionName < deltas[j].FunctionName
	})
	return deltas
}

// StructuralOnly reports whether a delta describes a function that ran on one
// side only, which is the shape that identifies a whole code path appearing
// or disappearing rather than shifting in cost.
func StructuralOnly(d FunctionDelta) bool {
	return d.DispatchCountA == 0 || d.DispatchCountB == 0
}

// writeStructuralFunctions renders the per-function comparison available
// without profiler timing. Only counts are shown, because only counts are
// known; there is no time column to leave misleadingly blank.
func writeStructuralFunctions(w io.Writer, deltas []FunctionDelta, limit int) {
	if len(deltas) == 0 {
		return
	}
	fmt.Fprintf(w, "\nBy Function (structural: dispatch counts from capture records, no timing)\n")
	fmt.Fprintf(w, "%-52s %8s %8s %10s  %s\n", "Function", "CountA", "CountB", "Delta", "")
	shown := deltas
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, d := range shown {
		note := ""
		if StructuralOnly(d) {
			note = "only in A"
			if d.DispatchCountA == 0 {
				note = "only in B"
			}
		}
		fmt.Fprintf(w, "%-52s %8d %8d %+10d  %s\n",
			fmtutil.TruncateString(d.FunctionName, 52),
			d.DispatchCountA, d.DispatchCountB, d.DispatchCountDelta, note)
	}
	if len(deltas) > len(shown) {
		fmt.Fprintf(w, "(%d more functions; raise --limit to see them)\n", len(deltas)-len(shown))
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
