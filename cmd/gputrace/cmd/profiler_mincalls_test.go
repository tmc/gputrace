package cmd

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

func profilerDispatches() []counter.DispatchInfo {
	var out []counter.DispatchInfo
	add := func(name string, n, us int) {
		for range n {
			out = append(out, counter.DispatchInfo{FunctionName: name, DurationUs: us})
		}
	}
	add("gather_frontbfloat16_uint32_int_2", 1, 487)
	add("s_copyint32int32", 1, 364)
	add("gemm_bfloat16", 96, 20)
	return out
}

func TestProfilerFunctionRowsRankBySpan(t *testing.T) {
	rows := profilerFunctionRows(profilerDispatches(), 487+364+96*20)
	want := []struct {
		name  string
		calls int
	}{
		{"gemm_bfloat16", 96},
		{"gather_frontbfloat16_uint32_int_2", 1},
		{"s_copyint32int32", 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].Name != w.name || rows[i].InvocationCount != w.calls {
			t.Errorf("row %d = %s/%d, want %s/%d", i, rows[i].Name, rows[i].InvocationCount, w.name, w.calls)
		}
	}
	var sum float64
	for _, kt := range rows {
		sum += kt.PercentOfTotal
	}
	if sum < 99.9 || sum > 100.1 {
		t.Errorf("shares sum to %.2f%%, want 100%%", sum)
	}
}

// The profiler command prints its own cost-ranked table, so it needs the same
// single-dispatch marker the timing command grew. It shipped without one.
func TestProfilerFunctionCallsMarksSingleDispatchRows(t *testing.T) {
	rows := profilerFunctionRows(profilerDispatches(), 487+364+96*20)
	out := formatProfilerFunctionCalls(rows, 0, false)

	for _, tt := range []struct {
		prefix string
		marked bool
	}{
		{"gather_frontbfloat16_uint32_int_2", true},
		{"s_copyint32int32", true},
		{"gemm_bfloat16", false},
	} {
		line := tableLine(out, tt.prefix)
		if line == "" {
			t.Fatalf("no row for %s in:\n%s", tt.prefix, out)
		}
		if got := strings.HasSuffix(line, gputrace.LowSampleMarker); got != tt.marked {
			t.Errorf("%s marked = %v, want %v (%q)", tt.prefix, got, tt.marked, line)
		}
	}
	if !strings.Contains(out, "single dispatch (2 of 3)") {
		t.Errorf("missing the marker footnote:\n%s", out)
	}
}

func TestProfilerFunctionCallsMinCalls(t *testing.T) {
	rows := profilerFunctionRows(profilerDispatches(), 487+364+96*20)
	tests := []struct {
		name     string
		minCalls int
		wantRows []string
		wantNote string
	}{
		{
			name:     "off by default",
			minCalls: 0,
			wantRows: []string{"gemm_bfloat16", "gather_frontbfloat16_uint32_int_2", "s_copyint32int32"},
		},
		{
			name:     "one keeps everything",
			minCalls: 1,
			wantRows: []string{"gemm_bfloat16", "gather_frontbfloat16_uint32_int_2", "s_copyint32int32"},
		},
		{
			name:     "two drops the single-dispatch rows",
			minCalls: 2,
			wantRows: []string{"gemm_bfloat16"},
			wantNote: "--min-calls 2 dropped 2 of 3 rows",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := formatProfilerFunctionCalls(rows, tt.minCalls, false)
			for _, name := range []string{"gemm_bfloat16", "gather_frontbfloat16_uint32_int_2", "s_copyint32int32"} {
				want := false
				for _, w := range tt.wantRows {
					want = want || w == name
				}
				if got := tableLine(out, name) != ""; got != want {
					t.Errorf("row %s present = %v, want %v", name, got, want)
				}
			}
			if tt.wantNote == "" {
				if strings.Contains(out, "--min-calls") {
					t.Errorf("unfiltered table carries a filter note:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, tt.wantNote) {
				t.Errorf("missing %q in:\n%s", tt.wantNote, out)
			}
			if !strings.Contains(out, "no longer sum to 100%") {
				t.Errorf("filtered table does not say the shares are partial:\n%s", out)
			}
		})
	}
}

// The filter applies to the table only. JSON and benchfmt are built from the
// same dispatch rows, so the filter must not edit them.
func TestProfilerFunctionCallsLeavesRowsUnfiltered(t *testing.T) {
	rows := profilerFunctionRows(profilerDispatches(), 487+364+96*20)
	before := append([]*gputrace.KernelTiming(nil), rows...)

	formatProfilerFunctionCalls(rows, 2, false)

	if len(rows) != len(before) {
		t.Fatalf("--min-calls mutated the shared rows: %d left, want %d", len(rows), len(before))
	}
	for i := range rows {
		if rows[i] != before[i] {
			t.Errorf("row %d changed identity or order", i)
		}
	}
}

// Filtering must not reorder: the header still claims a cost ranking.
func TestProfilerFunctionCallsKeepsRankOrder(t *testing.T) {
	rows := profilerFunctionRows(profilerDispatches(), 487+364+96*20)
	out := formatProfilerFunctionCalls(rows, 0, false)
	gather := strings.Index(out, "gather_frontbfloat16_uint32_int_2")
	copyIdx := strings.Index(out, "s_copyint32int32")
	gemm := strings.Index(out, "gemm_bfloat16")
	if !(gemm < gather && gather < copyIdx) {
		t.Errorf("rows are not in span order (gemm=%d gather=%d copy=%d):\n%s", gemm, gather, copyIdx, out)
	}
}

// The reported symptom was "unknown flag", so pin the registration itself.
func TestProfilerCommandRegistersMinCalls(t *testing.T) {
	opts := &profilerOptions{limit: 20}
	cmd := newProfilerCommand(opts)
	f := cmd.Flags().Lookup("min-calls")
	if f == nil {
		t.Fatal("profiler has no --min-calls flag")
	}
	if f.DefValue != "0" {
		t.Errorf("--min-calls defaults to %q, want it off at 0", f.DefValue)
	}
}

// tableLine returns the rendered row starting with prefix, or "".
func tableLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimRight(line, " ")
		}
	}
	return ""
}
