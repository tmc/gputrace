package difftrace

import (
	"strings"
	"testing"
)

func TestStructuralFunctionDeltas(t *testing.T) {
	// Shaped after a static-compiled vs eager decode comparison: a family of
	// kernels present on one side only, plus one that merely runs less often.
	a := map[string]int{
		"compute_dynamic_offset_int32": 56,
		"gg2_dynamic_copy":             56,
		"vv_Addbfloat16":               56,
		"rmsbfloat16":                  57,
	}
	b := map[string]int{
		"vv_Addbfloat16": 56,
		"rmsbfloat16":    28,
	}

	deltas := StructuralFunctionDeltas(a, b)
	if len(deltas) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(deltas), deltas)
	}

	// Largest absolute delta first.
	if got := deltas[0].FunctionName; got != "compute_dynamic_offset_int32" {
		t.Errorf("first row = %q, want the largest delta", got)
	}
	if deltas[0].DispatchCountDelta != 56 {
		t.Errorf("delta = %d, want 56", deltas[0].DispatchCountDelta)
	}

	// A function on both sides with equal counts still appears, at the end.
	last := deltas[len(deltas)-1]
	if last.FunctionName != "vv_Addbfloat16" || last.DispatchCountDelta != 0 {
		t.Errorf("last row = %+v, want vv_Addbfloat16 with no delta", last)
	}

	for _, d := range deltas {
		if d.FunctionName == "rmsbfloat16" {
			if d.DispatchCountA != 57 || d.DispatchCountB != 28 {
				t.Errorf("rmsbfloat16 = %+v, want 57 vs 28", d)
			}
			if StructuralOnly(d) {
				t.Error("a function present on both sides reported as one-sided")
			}
		}
	}
}

func TestStructuralFunctionDeltasTiesAreStable(t *testing.T) {
	// Counts arrive from a map, so equal deltas must break by name or the
	// report reorders between runs.
	a := map[string]int{"zebra": 1, "alpha": 1, "middle": 1}
	first := StructuralFunctionDeltas(a, nil)
	for i := 0; i < 10; i++ {
		again := StructuralFunctionDeltas(a, nil)
		for j := range first {
			if first[j].FunctionName != again[j].FunctionName {
				t.Fatalf("order changed between runs: %v then %v", first, again)
			}
		}
	}
	if first[0].FunctionName != "alpha" {
		t.Errorf("equal deltas not ordered by name: %v", first)
	}
}

func TestStructuralFunctionDeltasEmpty(t *testing.T) {
	if got := StructuralFunctionDeltas(nil, nil); len(got) != 0 {
		t.Errorf("got %d rows from two empty traces, want 0", len(got))
	}
	// A function recorded with a zero count on both sides says nothing.
	if got := StructuralFunctionDeltas(map[string]int{"idle": 0}, map[string]int{"idle": 0}); len(got) != 0 {
		t.Errorf("got %d rows for a never-dispatched function, want 0", len(got))
	}
}

func TestStructuralOnly(t *testing.T) {
	tests := []struct {
		name  string
		delta FunctionDelta
		want  bool
	}{
		{"only in A", FunctionDelta{DispatchCountA: 28, DispatchCountB: 0}, true},
		{"only in B", FunctionDelta{DispatchCountA: 0, DispatchCountB: 28}, true},
		{"both sides", FunctionDelta{DispatchCountA: 28, DispatchCountB: 14}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StructuralOnly(tt.delta); got != tt.want {
				t.Errorf("StructuralOnly(%+v) = %v, want %v", tt.delta, got, tt.want)
			}
		})
	}
}

func TestWriteStructuralFunctions(t *testing.T) {
	deltas := []FunctionDelta{
		{FunctionName: "arangeint32", DispatchCountA: 28, DispatchCountB: 0, DispatchCountDelta: 28},
		{FunctionName: "vv_Add", DispatchCountA: 56, DispatchCountB: 56},
	}
	var b strings.Builder
	writeStructuralFunctions(&b, deltas, 20)
	out := b.String()

	if !strings.Contains(out, "no timing") {
		t.Errorf("header does not say timing is absent:\n%s", out)
	}
	if !strings.Contains(out, "only in A") {
		t.Errorf("one-sided function not flagged:\n%s", out)
	}
	if strings.Count(out, "only in") != 1 {
		t.Errorf("a two-sided function was flagged as one-sided:\n%s", out)
	}
}

func TestWriteStructuralFunctionsReportsTruncation(t *testing.T) {
	deltas := make([]FunctionDelta, 5)
	for i := range deltas {
		deltas[i] = FunctionDelta{FunctionName: string(rune('a' + i)), DispatchCountA: 1}
	}
	var b strings.Builder
	writeStructuralFunctions(&b, deltas, 2)
	if out := b.String(); !strings.Contains(out, "3 more functions") {
		t.Errorf("dropped rows not reported:\n%s", out)
	}
}
