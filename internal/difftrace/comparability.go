package difftrace

import (
	"fmt"
	"sort"
	"strings"
)

// A capture is a window onto a running program, and two captures of the same
// workload can open and close that window at different points. When they do,
// the kernels that run once per forward pass -- an embedding lookup, a logit
// or sampling tail -- are present in one trace and missing from the other
// while every per-layer kernel matches. The diff is then right row by row and
// wrong as a whole: the one-sided rows read as work that was added or removed,
// and they are counted into the delta.
//
// The shape is specific enough to detect. A per-layer kernel runs once per
// layer, so it appears tens of times. A once-per-forward kernel appears once
// or twice. A low count present on one side and absent from the other is the
// signature of a traced-region boundary rather than of a workload change --
// which is why the check is on the low-count rows, not the large ones.

// ComparabilityMaxCount is the highest dispatch count at which a function is
// read as running once per forward pass rather than once per layer.
const ComparabilityMaxCount = 2

// comparabilityNamesShown bounds how many names a warning lists before
// summarizing the rest. The count is always exact; only the listing is capped.
const comparabilityNamesShown = 5

// ComparabilityCheck records functions that ran a few times on one side of a
// diff and not at all on the other. The zero value reports comparable.
type ComparabilityCheck struct {
	OnlyInA []string `json:"only_in_a,omitempty"`
	OnlyInB []string `json:"only_in_b,omitempty"`
}

// CheckComparability looks for once-per-forward kernels present on one side
// only. deltas must be the full set, not a truncated top-N: the rows that
// carry this signal are the smallest ones in the table and a cost-ordered
// limit is exactly what drops them.
func CheckComparability(deltas []FunctionDelta) ComparabilityCheck {
	var check ComparabilityCheck
	for _, d := range deltas {
		switch {
		case d.DispatchCountB == 0 && d.DispatchCountA > 0 && d.DispatchCountA <= ComparabilityMaxCount:
			check.OnlyInA = append(check.OnlyInA, d.FunctionName)
		case d.DispatchCountA == 0 && d.DispatchCountB > 0 && d.DispatchCountB <= ComparabilityMaxCount:
			check.OnlyInB = append(check.OnlyInB, d.FunctionName)
		}
	}
	sort.Strings(check.OnlyInA)
	sort.Strings(check.OnlyInB)
	return check
}

// Comparable reports whether both traces cover the same once-per-forward work.
func (c ComparabilityCheck) Comparable() bool {
	return len(c.OnlyInA) == 0 && len(c.OnlyInB) == 0
}

// Warning describes the asymmetry in the terms a reader has to act on, and
// returns "" when there is nothing to say. The distinction it draws is the
// useful one: kernels missing from one side only is what a truncated capture
// looks like, whereas both sides having their own is what two genuinely
// different workloads look like.
func (c ComparabilityCheck) Warning() string {
	switch {
	case c.Comparable():
		return ""
	case len(c.OnlyInB) == 0:
		return fmt.Sprintf("capture windows may not be comparable: %s, and none only in B; "+
			"a capture that begins or ends mid-forward is not comparable to one that does not",
			describeComparabilitySide(c.OnlyInA, "A"))
	case len(c.OnlyInA) == 0:
		return fmt.Sprintf("capture windows may not be comparable: %s, and none only in A; "+
			"a capture that begins or ends mid-forward is not comparable to one that does not",
			describeComparabilitySide(c.OnlyInB, "B"))
	default:
		return fmt.Sprintf("check capture windows: %s and %s; "+
			"both sides having their own is also what two different workloads look like, "+
			"so this is a prompt to compare the traced regions rather than a verdict",
			describeComparabilitySide(c.OnlyInA, "A"), describeComparabilitySide(c.OnlyInB, "B"))
	}
}

func describeComparabilitySide(names []string, side string) string {
	shown := names
	suffix := ""
	if len(shown) > comparabilityNamesShown {
		shown = shown[:comparabilityNamesShown]
		suffix = fmt.Sprintf(", and %d more", len(names)-len(shown))
	}
	kernels := "kernels"
	if len(names) == 1 {
		kernels = "kernel"
	}
	return fmt.Sprintf("%d once-per-forward %s ran only in %s (%s%s)",
		len(names), kernels, side, strings.Join(shown, ", "), suffix)
}
