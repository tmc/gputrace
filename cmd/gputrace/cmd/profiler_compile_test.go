package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func boolPtr(v bool) *bool  { return &v }
func i64Ptr(v int64) *int64 { return &v }
func msPipe(name string, ms float64) counter.PipelineStats {
	return counter.PipelineStats{FunctionName: name, CompilationTimeMs: ms}
}

func TestSummarizeCompilation(t *testing.T) {
	tests := []struct {
		name      string
		pipelines []counter.PipelineStats
		want      *compileSummary
	}{
		{
			name:      "no compilation data returns nil",
			pipelines: []counter.PipelineStats{{FunctionName: "warm"}},
			want:      nil,
		},
		{
			name:      "times only",
			pipelines: []counter.PipelineStats{msPipe("a", 8.262), msPipe("b", 11.172), {FunctionName: "c"}},
			want: &compileSummary{
				Timed: 2, Pipelines: 3, TotalMs: 19.434,
				SlowestMs: 11.172, SlowestName: "b",
			},
		},
		{
			name: "cache flags counted, not derived",
			pipelines: []counter.PipelineStats{
				{FunctionName: "a", CompilePerformance: &counter.PipelineCompilePerformance{FunctionWasCached: boolPtr(true)}},
				{FunctionName: "b", CompilePerformance: &counter.PipelineCompilePerformance{FunctionWasCached: boolPtr(false)}},
				{FunctionName: "c", CompilePerformance: &counter.PipelineCompilePerformance{}},
			},
			want: &compileSummary{Pipelines: 3, Cached: 1, Compiled: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeCompilation(tt.pipelines)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("summarizeCompilation() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("summarizeCompilation() = nil, want a summary")
			}
			if got.Timed != tt.want.Timed || got.Pipelines != tt.want.Pipelines {
				t.Errorf("timed/pipelines = %d/%d, want %d/%d", got.Timed, got.Pipelines, tt.want.Timed, tt.want.Pipelines)
			}
			if d := got.TotalMs - tt.want.TotalMs; d > 1e-9 || d < -1e-9 {
				t.Errorf("TotalMs = %v, want %v", got.TotalMs, tt.want.TotalMs)
			}
			if got.SlowestName != tt.want.SlowestName {
				t.Errorf("SlowestName = %q, want %q", got.SlowestName, tt.want.SlowestName)
			}
			if got.Cached != tt.want.Cached || got.Compiled != tt.want.Compiled {
				t.Errorf("cached/compiled = %d/%d, want %d/%d", got.Cached, got.Compiled, tt.want.Cached, tt.want.Compiled)
			}
		})
	}
}

// The archive writes -1 for a phase it did not measure. Summing that as a
// duration would report a total smaller than its own parts, so the sentinel is
// excluded and counted instead.
func TestSummarizeCompilationExcludesNegativePhases(t *testing.T) {
	pipelines := []counter.PipelineStats{
		{FunctionName: "a", CompilePerformance: &counter.PipelineCompilePerformance{
			CompilerBackendNanoseconds: i64Ptr(400),
			CompilerTotalNanoseconds:   i64Ptr(-1),
		}},
		{FunctionName: "b", CompilePerformance: &counter.PipelineCompilePerformance{
			CompilerBackendNanoseconds: i64Ptr(600),
		}},
	}
	s := summarizeCompilation(pipelines)
	if s == nil {
		t.Fatal("summarizeCompilation() = nil")
	}
	if s.NegativePhases != 1 {
		t.Errorf("NegativePhases = %d, want 1", s.NegativePhases)
	}
	var backend *compilePhase
	for i := range s.Phases {
		if s.Phases[i].Name == "backend" {
			backend = &s.Phases[i]
		}
		if s.Phases[i].Name == "compiler total" {
			t.Errorf("a -1 phase became a reported total: %+v", s.Phases[i])
		}
	}
	if backend == nil {
		t.Fatal("backend phase missing")
	}
	if backend.NS != 1000 || backend.N != 2 {
		t.Errorf("backend = %d ns over %d, want 1000 over 2", backend.NS, backend.N)
	}
}

func TestWriteCompileSummarySaysWhenCacheIsUnrecorded(t *testing.T) {
	var buf bytes.Buffer
	writeCompileSummary(&buf, summarizeCompilation([]counter.PipelineStats{msPipe("a", 2.5)}))
	out := buf.String()
	if !strings.Contains(out, "not recorded in this trace") {
		t.Errorf("an absent cache flag must say so rather than read as 0 cached:\n%s", out)
	}
	if !strings.Contains(out, "2.500 ms") {
		t.Errorf("compile time missing from output:\n%s", out)
	}
}

func TestWriteCompileSummaryNilWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	writeCompileSummary(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("a capture with no compilation data printed a section:\n%s", buf.String())
	}
}

// Zero is a real compile time and absent is not, so the detail line omits
// fields rather than printing them as zero.
func TestWritePipelineCompileDetail(t *testing.T) {
	var buf bytes.Buffer
	writePipelineCompileDetail(&buf, &counter.PipelineStats{FunctionName: "a"})
	if buf.Len() != 0 {
		t.Errorf("pipeline with no compile data printed a line: %q", buf.String())
	}

	buf.Reset()
	writePipelineCompileDetail(&buf, &counter.PipelineStats{
		FunctionName:      "a",
		CompilationTimeMs: 1.25,
		CompilePerformance: &counter.PipelineCompilePerformance{
			FunctionWasCached:          boolPtr(false),
			CompilerBackendNanoseconds: i64Ptr(-1),
		},
	})
	out := buf.String()
	for _, want := range []string{"1.250 ms", "not cached", "backend=not recorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail line missing %q:\n%s", want, out)
		}
	}
}
