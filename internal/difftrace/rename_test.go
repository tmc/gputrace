package difftrace

import (
	"bytes"
	"strings"
	"testing"
)

// The two pairs below are the ones that turned a true +92 into a hand summed
// +176. Neither pair is a change in the work done: one is the same copy with
// symbolic addressing, the other two JIT variants of a single fusion.
func renamedDeltas() []FunctionDelta {
	return []FunctionDelta{
		{FunctionName: "gg2_dynamic_copybfloat16bfloat16", DispatchCountA: 56, DispatchCountDelta: 56},
		{FunctionName: "gg2_copybfloat16bfloat16", DispatchCountB: 56, DispatchCountDelta: -56},
		{FunctionName: "CV2ISigmoidMultiply", DispatchCountA: 28, DispatchCountDelta: 28},
		{FunctionName: "CV2ISigmoidBroadcast", DispatchCountB: 28, DispatchCountDelta: -28},
		{FunctionName: "ss_Addint32", DispatchCountA: 29, DispatchCountDelta: 29},
	}
}

func TestRenamePairsMatchesOnCountAlone(t *testing.T) {
	pairs := RenamePairs(renamedDeltas())

	for a, b := range map[string]string{
		"gg2_dynamic_copybfloat16bfloat16": "gg2_copybfloat16bfloat16",
		"CV2ISigmoidMultiply":              "CV2ISigmoidBroadcast",
	} {
		if pairs[a] != b {
			t.Errorf("pairs[%q] = %q, want %q", a, pairs[a], b)
		}
		if pairs[b] != a {
			t.Errorf("pairs[%q] = %q, want the pairing to be symmetric", b, pairs[b])
		}
	}
	if partner, ok := pairs["ss_Addint32"]; ok {
		t.Errorf("unpaired one-sided row matched %q", partner)
	}
}

// Two candidates on a side means the trace does not say which pairs with
// which, and inventing one would assert a relationship it does not record.
func TestRenamePairsDeclinesAmbiguousCounts(t *testing.T) {
	pairs := RenamePairs([]FunctionDelta{
		{FunctionName: "a1", DispatchCountA: 8},
		{FunctionName: "a2", DispatchCountA: 8},
		{FunctionName: "b1", DispatchCountB: 8},
	})
	if len(pairs) != 0 {
		t.Errorf("pairs = %v, want none when a count is ambiguous", pairs)
	}
}

// A function that ran on both sides is not a rename however its counts moved.
func TestRenamePairsIgnoresTwoSidedRows(t *testing.T) {
	pairs := RenamePairs([]FunctionDelta{
		{FunctionName: "gemm", DispatchCountA: 96, DispatchCountB: 96},
		{FunctionName: "steel", DispatchCountA: 96, DispatchCountB: 24},
	})
	if len(pairs) != 0 {
		t.Errorf("pairs = %v, want none", pairs)
	}
}

func TestStructuralFunctionsLabelsRenamePairs(t *testing.T) {
	var b bytes.Buffer
	writeStructuralFunctions(&b, renamedDeltas(), 20)
	out := b.String()

	for _, want := range []string{
		"only in A, net 0 with gg2_copybfloat16bfloat16",
		"only in B, net 0 with gg2_dynamic_copybfloat16bfloat16",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ss_Addint32") && strings.Contains(line, "net 0") {
			t.Errorf("unpaired row labelled as a rename: %q", line)
		}
	}
}

// A renamed kernel ran on both sides, so its one-sided rows say nothing about
// where either capture began. Counting them as once-per-forward kernels would
// make a rename look like a truncated trace.
func TestRenamedRowsAreNotComparabilityEvidence(t *testing.T) {
	deltas := []FunctionDelta{
		{FunctionName: "sample_topp", DispatchCountA: 1},
		{FunctionName: "sample_greedy", DispatchCountB: 1},
	}
	check := CheckComparability(deltas, RenamePairs(deltas))
	if !check.Comparable() {
		t.Errorf("rename pair reported as a capture-window difference: %+v", check)
	}
}
