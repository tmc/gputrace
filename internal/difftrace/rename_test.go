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

// Pairing is on the count; the names only break ties. Each case below states
// what the counts alone say, and only the ambiguous ones depend on names.
func TestRenamePairsTable(t *testing.T) {
	tests := []struct {
		name      string
		deltas    []FunctionDelta
		want      map[string]string
		ambiguous []string
	}{
		{
			// The reported false negative: a short shared prefix that a
			// name-similarity gate would reject, with unique counts.
			name: "short shared prefix",
			deltas: []FunctionDelta{
				{FunctionName: "gg2_dynamic_copy", DispatchCountA: 48},
				{FunctionName: "gg2_copy", DispatchCountB: 48},
			},
			want: map[string]string{
				"gg2_dynamic_copy": "gg2_copy",
				"gg2_copy":         "gg2_dynamic_copy",
			},
		},
		{
			// Long mangled names, the case that already worked, kept here
			// so a change to the tiebreaker cannot quietly drop it.
			name: "long mangled names",
			deltas: []FunctionDelta{
				{FunctionName: "CV2ISigmoid_f32_broadcast_multiply_0", DispatchCountA: 24},
				{FunctionName: "CV2ISigmoid_f32_broadcast_0", DispatchCountB: 24},
			},
			want: map[string]string{
				"CV2ISigmoid_f32_broadcast_multiply_0": "CV2ISigmoid_f32_broadcast_0",
				"CV2ISigmoid_f32_broadcast_0":          "CV2ISigmoid_f32_broadcast_multiply_0",
			},
		},
		{
			// Two renames at the same count, as the 0.5B diff has at 48.
			// The sdpa names settle each other; what is left is one
			// candidate on each side, which the counts pair by themselves
			// even though the mangled names barely resemble each other.
			name: "pair by elimination",
			deltas: []FunctionDelta{
				{FunctionName: "sdpa_vector_bfloat16_t_64_64_floatmask_qnt_nc_nosinks", DispatchCountA: 48},
				{FunctionName: "CV2ISigmoidADV2IMultiplyACEV2OMultiplyDB_VV_V2V2_1116_contiguous", DispatchCountA: 48},
				{FunctionName: "sdpa_vector_bfloat16_t_64_64_nomask_qnt_nc_nosinks", DispatchCountB: 48},
				{FunctionName: "CV2ISigmoidADV2IBroadcastACEV2IBroadcastCAFV2IMultiplyDEGV2IBroadcastFBHV2IBroadcastBFIV2OMultiplyGH_VV_V2V2_1116_contiguous", DispatchCountB: 48},
			},
			want: map[string]string{
				"sdpa_vector_bfloat16_t_64_64_floatmask_qnt_nc_nosinks":                                                                        "sdpa_vector_bfloat16_t_64_64_nomask_qnt_nc_nosinks",
				"sdpa_vector_bfloat16_t_64_64_nomask_qnt_nc_nosinks":                                                                           "sdpa_vector_bfloat16_t_64_64_floatmask_qnt_nc_nosinks",
				"CV2ISigmoidADV2IMultiplyACEV2OMultiplyDB_VV_V2V2_1116_contiguous":                                                             "CV2ISigmoidADV2IBroadcastACEV2IBroadcastCAFV2IMultiplyDEGV2IBroadcastFBHV2IBroadcastBFIV2OMultiplyGH_VV_V2V2_1116_contiguous",
				"CV2ISigmoidADV2IBroadcastACEV2IBroadcastCAFV2IMultiplyDEGV2IBroadcastFBHV2IBroadcastBFIV2OMultiplyGH_VV_V2V2_1116_contiguous": "CV2ISigmoidADV2IMultiplyACEV2OMultiplyDB_VV_V2V2_1116_contiguous",
			},
		},
		{
			// Three against three at the same count, with nothing in the
			// names to tell them apart: report the group, do not guess.
			name: "ambiguous group",
			deltas: []FunctionDelta{
				{FunctionName: "alpha", DispatchCountA: 48},
				{FunctionName: "bravo", DispatchCountA: 48},
				{FunctionName: "delta", DispatchCountA: 48},
				{FunctionName: "echo", DispatchCountB: 48},
				{FunctionName: "foxtrot", DispatchCountB: 48},
				{FunctionName: "golf", DispatchCountB: 48},
			},
			want:      map[string]string{},
			ambiguous: []string{"alpha", "bravo", "delta", "echo", "foxtrot", "golf"},
		},
		{
			// Two A rows share the count, but one B name singles one out.
			name: "tie broken by name",
			deltas: []FunctionDelta{
				{FunctionName: "gg2_dynamic_copybfloat16bfloat16", DispatchCountA: 48},
				{FunctionName: "ss_Addint32", DispatchCountA: 48},
				{FunctionName: "gg2_copybfloat16bfloat16", DispatchCountB: 48},
			},
			want: map[string]string{
				"gg2_dynamic_copybfloat16bfloat16": "gg2_copybfloat16bfloat16",
				"gg2_copybfloat16bfloat16":         "gg2_dynamic_copybfloat16bfloat16",
			},
			// Nothing is left on the B side for ss_Addint32 to be confused
			// with once the pair settles, so it is a plain one-sided row.
			ambiguous: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pairs, ambiguous := renamePairing(tt.deltas)
			if len(pairs) != len(tt.want) {
				t.Errorf("pairs = %v, want %v", pairs, tt.want)
			}
			for a, b := range tt.want {
				if pairs[a] != b {
					t.Errorf("pairs[%q] = %q, want %q", a, pairs[a], b)
				}
			}
			for _, name := range tt.ambiguous {
				if ambiguous[name] == 0 {
					t.Errorf("%q not reported as ambiguous: %v", name, ambiguous)
				}
			}
			if len(ambiguous) != len(tt.ambiguous) {
				t.Errorf("ambiguous = %v, want %v", ambiguous, tt.ambiguous)
			}
		})
	}
}

// An ambiguous group is reported as such, not left looking like a plain
// one-sided row and not paired with a guess.
func TestStructuralFunctionsReportsAmbiguity(t *testing.T) {
	var b bytes.Buffer
	writeStructuralFunctions(&b, []FunctionDelta{
		{FunctionName: "alpha", DispatchCountA: 48, DispatchCountDelta: 48},
		{FunctionName: "bravo", DispatchCountA: 48, DispatchCountDelta: 48},
		{FunctionName: "echo", DispatchCountB: 48, DispatchCountDelta: -48},
		{FunctionName: "foxtrot", DispatchCountB: 48, DispatchCountDelta: -48},
	}, 20)
	out := b.String()
	if strings.Contains(out, "net 0") {
		t.Errorf("ambiguous group paired anyway:\n%s", out)
	}
	if !strings.Contains(out, "none decisive") {
		t.Errorf("ambiguous group not reported:\n%s", out)
	}
}
