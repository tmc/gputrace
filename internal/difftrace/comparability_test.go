package difftrace

import (
	"strings"
	"testing"
)

// The fixture is the shape that produced the error this check exists for: a
// 0.5B capture missing an embedding lookup and a logit/sampling tail that the
// capture it was diffed against contained, while every per-layer kernel
// matched exactly.
func truncatedTailDeltas() []FunctionDelta {
	return []FunctionDelta{
		{FunctionName: "gemm_bfloat16", DispatchCountA: 96, DispatchCountB: 96},
		{FunctionName: "steel_matmul", DispatchCountA: 24, DispatchCountB: 24},
		{FunctionName: "looped_logsumexp_float32", DispatchCountA: 0, DispatchCountB: 1},
		{FunctionName: "gather_axisfloat32int32_intcc", DispatchCountA: 0, DispatchCountB: 1},
		{FunctionName: "v_copyuint32int32", DispatchCountA: 0, DispatchCountB: 2},
	}
}

func TestCheckComparabilityFindsOneSidedTail(t *testing.T) {
	check := CheckComparability(truncatedTailDeltas(), nil)
	if check.Comparable() {
		t.Fatal("a trace missing the sampling tail reported comparable")
	}
	if len(check.OnlyInA) != 0 {
		t.Errorf("OnlyInA = %v, want none", check.OnlyInA)
	}
	want := []string{"gather_axisfloat32int32_intcc", "looped_logsumexp_float32", "v_copyuint32int32"}
	if len(check.OnlyInB) != len(want) {
		t.Fatalf("OnlyInB = %v, want %v", check.OnlyInB, want)
	}
	for i, name := range want {
		if check.OnlyInB[i] != name {
			t.Errorf("OnlyInB[%d] = %q, want %q (sorted)", i, check.OnlyInB[i], name)
		}
	}

	warning := check.Warning()
	if !strings.Contains(warning, "may not be comparable") ||
		!strings.Contains(warning, "none only in A") {
		t.Errorf("warning = %q, want a one-sided boundary warning", warning)
	}
}

// A per-layer kernel that runs on one side only is a workload difference, not
// a window difference. Flagging it would make the warning fire on every real
// diff and train the reader to ignore it.
func TestCheckComparabilityIgnoresPerLayerKernels(t *testing.T) {
	check := CheckComparability([]FunctionDelta{
		{FunctionName: "gg2_dynamic_copybfloat16bfloat16", DispatchCountA: 56, DispatchCountB: 0},
		{FunctionName: "ss_Addint32", DispatchCountA: 29, DispatchCountB: 0},
	}, nil)
	if !check.Comparable() {
		t.Errorf("per-layer one-sided kernels flagged as a window difference: %+v", check)
	}
	if check.Warning() != "" {
		t.Errorf("warning = %q, want none", check.Warning())
	}
}

// Matched functions never signal anything, however small their counts.
func TestCheckComparabilityIgnoresMatchedLowCounts(t *testing.T) {
	check := CheckComparability([]FunctionDelta{
		{FunctionName: "gather_axis", DispatchCountA: 1, DispatchCountB: 1},
		{FunctionName: "arangeint32", DispatchCountA: 2, DispatchCountB: 1},
	}, nil)
	if !check.Comparable() {
		t.Errorf("matched low-count rows flagged: %+v", check)
	}
}

// Both sides carrying their own once-per-forward kernels is equally what two
// different workloads look like, so the wording has to stop short of a verdict.
func TestCheckComparabilityHedgesWhenBothSidesDiffer(t *testing.T) {
	check := CheckComparability([]FunctionDelta{
		{FunctionName: "sample_topp", DispatchCountA: 1, DispatchCountB: 0},
		{FunctionName: "sample_greedy", DispatchCountA: 0, DispatchCountB: 1},
	}, nil)
	warning := check.Warning()
	if !strings.Contains(warning, "rather than a verdict") {
		t.Errorf("warning = %q, want a hedged two-sided warning", warning)
	}
	if strings.Contains(warning, "may not be comparable") {
		t.Errorf("warning = %q, should not assert incomparability", warning)
	}
}

func TestComparabilityWarningCapsTheListButNotTheCount(t *testing.T) {
	var deltas []FunctionDelta
	for _, name := range []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"} {
		deltas = append(deltas, FunctionDelta{FunctionName: name, DispatchCountB: 1})
	}
	warning := CheckComparability(deltas, nil).Warning()
	if !strings.Contains(warning, "7 once-per-forward kernels") {
		t.Errorf("warning = %q, want the exact count", warning)
	}
	if !strings.Contains(warning, "and 2 more") {
		t.Errorf("warning = %q, want the elided remainder reported", warning)
	}
	if strings.Contains(warning, "k7") {
		t.Errorf("warning = %q, want the listing capped", warning)
	}
}

// The check must run over every delta, not the cost-ordered top-N, because a
// once-per-forward kernel is by construction at the bottom of that ordering.
func TestReportComparabilitySurvivesTheRowLimit(t *testing.T) {
	a := &TraceData{Path: "a", StructuralFunctions: map[string]int{"gemm": 96}}
	b := &TraceData{Path: "b", StructuralFunctions: map[string]int{
		"gemm": 96, "looped_logsumexp_float32": 1, "gather_axisfloat32": 1,
	}}
	report := BuildReport(a, b, AlignmentResult{}, ReportOptions{Limit: 1})

	if len(report.TopFunctionDeltas) != 1 {
		t.Fatalf("limit not applied: %d rows", len(report.TopFunctionDeltas))
	}
	if len(report.Comparability.OnlyInB) != 2 {
		t.Fatalf("comparability = %+v, want both truncated rows", report.Comparability)
	}
	var found bool
	for _, w := range report.Warnings {
		if strings.Contains(w, "may not be comparable") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the comparability warning", report.Warnings)
	}
}
