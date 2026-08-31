package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/tmc/gputrace/internal/counter"
)

// compileSummary aggregates shader compilation across a capture's pipelines.
//
// Compilation runs on the host before the GPU executes anything, so it lands in
// no dispatch duration, no encoder span, and no execution cost. A run that
// compiles inside its measured window pays a price none of those numbers
// report. That is the whole reason for printing this beside them: a warmup
// either moved compilation out of the window or it did not, and until now the
// only way to check was to export a Perfetto trace and read the arguments.
type compileSummary struct {
	// Timed is the number of pipelines carrying a compilation time. It is
	// reported against the pipeline count because a partial denominator is
	// the usual reason a total looks too small.
	Timed       int
	Pipelines   int
	TotalMs     float64
	SlowestMs   float64
	SlowestName string

	// Cached and Compiled count pipelines by "Function was cached". They come
	// from a separate dictionary than the times above and are frequently
	// absent when the times are present, so they are counted, not derived.
	Cached   int
	Compiled int

	// Phases sums the compiler phase timings that were recorded as
	// non-negative. The archive uses -1 for a phase it did not measure, which
	// is why a plain sum would be wrong; NegativePhases counts what was
	// dropped so an unexpectedly small total is visible as such.
	Phases         []compilePhase
	NegativePhases int
}

type compilePhase struct {
	Name string
	NS   int64
	N    int
}

// summarizeCompilation collects compilation timing from pipeline statistics.
// It returns nil when nothing in the capture recorded any, which is the normal
// case for a trace whose shaders were all warm.
func summarizeCompilation(pipelines []counter.PipelineStats) *compileSummary {
	s := &compileSummary{Pipelines: len(pipelines)}
	phaseNS := map[string]*compilePhase{}
	order := []string{
		"translator", "optimization", "backend",
		"compiler total", "driver total", "synchronous service",
	}
	add := func(name string, v *int64) {
		if v == nil {
			return
		}
		if *v < 0 {
			s.NegativePhases++
			return
		}
		p := phaseNS[name]
		if p == nil {
			p = &compilePhase{Name: name}
			phaseNS[name] = p
		}
		p.NS += *v
		p.N++
	}
	for i := range pipelines {
		p := &pipelines[i]
		if p.CompilationTimeMs > 0 || p.HasRecordedStatistic("Compilation time in milliseconds") {
			s.Timed++
			s.TotalMs += p.CompilationTimeMs
			if p.CompilationTimeMs > s.SlowestMs {
				s.SlowestMs = p.CompilationTimeMs
				s.SlowestName = p.DisplayName()
			}
		}
		cp := p.CompilePerformance
		if cp == nil {
			continue
		}
		if cp.FunctionWasCached != nil {
			if *cp.FunctionWasCached {
				s.Cached++
			} else {
				s.Compiled++
			}
		}
		add("translator", cp.CompilerTranslatorNanoseconds)
		add("optimization", cp.CompilerOptimizationNanoseconds)
		add("backend", cp.CompilerBackendNanoseconds)
		add("compiler total", cp.CompilerTotalNanoseconds)
		add("driver total", cp.DriverTotalNanoseconds)
		add("synchronous service", cp.SynchronousServiceNanoseconds)
	}
	for _, name := range order {
		if p := phaseNS[name]; p != nil {
			s.Phases = append(s.Phases, *p)
		}
	}
	if s.Timed == 0 && s.Cached == 0 && s.Compiled == 0 && len(s.Phases) == 0 && s.NegativePhases == 0 {
		return nil
	}
	return s
}

// writeCompileSummary prints the shader compilation section.
func writeCompileSummary(w io.Writer, s *compileSummary) {
	if s == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, Colorize("Shader Compilation", ColorBold))
	fmt.Fprintln(w, TableSeparator(60))
	if s.Timed > 0 {
		fmt.Fprintf(w, "  Total Compile Time: %.3f ms across %d of %d %s\n",
			s.TotalMs, s.Timed, s.Pipelines, Pluralize(s.Pipelines, "pipeline", "pipelines"))
		if s.SlowestName != "" {
			fmt.Fprintf(w, "  Slowest:            %.3f ms  %s\n", s.SlowestMs, s.SlowestName)
		}
	} else {
		fmt.Fprintf(w, "  Total Compile Time: (no pipeline recorded one)\n")
	}
	switch {
	case s.Cached > 0 || s.Compiled > 0:
		fmt.Fprintf(w, "  Compile Cache:      %d cached, %d compiled\n", s.Cached, s.Compiled)
	default:
		fmt.Fprintf(w, "  Compile Cache:      (not recorded in this trace)\n")
	}
	for _, p := range s.Phases {
		fmt.Fprintf(w, "  %-18s %s over %d %s\n",
			compilePhaseLabel(p.Name), FormatDurationNs(uint64(p.NS)), p.N,
			Pluralize(p.N, "pipeline", "pipelines"))
	}
	if s.NegativePhases > 0 {
		fmt.Fprintf(w, "  %d phase %s recorded as -1 and are excluded; the archive uses -1 for a phase it did not measure.\n",
			s.NegativePhases, Pluralize(s.NegativePhases, "timing", "timings"))
	}
	fmt.Fprintln(w, "Compilation is host-side work. It is in no dispatch, encoder, or execution-cost")
	fmt.Fprintln(w, "number above, so a warmup that failed shows up here and nowhere else.")
}

func compilePhaseLabel(name string) string {
	switch name {
	case "translator":
		return "Translator:"
	case "optimization":
		return "Optimization:"
	case "backend":
		return "Backend:"
	case "compiler total":
		return "Compiler Total:"
	case "driver total":
		return "Driver Total:"
	case "synchronous service":
		return "Sync Service:"
	}
	return name + ":"
}

// writePipelineCompileDetail prints one pipeline's compilation fields, for the
// detailed kernel listing. Absent fields are omitted rather than printed as
// zero, because zero is a real compile time and absent is not.
func writePipelineCompileDetail(w io.Writer, p *counter.PipelineStats) {
	var parts []string
	if p.CompilationTimeMs > 0 || p.HasRecordedStatistic("Compilation time in milliseconds") {
		parts = append(parts, fmt.Sprintf("%.3f ms", p.CompilationTimeMs))
	}
	if cp := p.CompilePerformance; cp != nil {
		if cp.FunctionWasCached != nil {
			if *cp.FunctionWasCached {
				parts = append(parts, "cached")
			} else {
				parts = append(parts, "not cached")
			}
		}
		for _, f := range []struct {
			name  string
			value *int64
		}{
			{"translator", cp.CompilerTranslatorNanoseconds},
			{"optimization", cp.CompilerOptimizationNanoseconds},
			{"backend", cp.CompilerBackendNanoseconds},
		} {
			if f.value == nil {
				continue
			}
			if *f.value < 0 {
				parts = append(parts, f.name+"=not recorded")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%s", f.name, FormatDurationNs(uint64(*f.value))))
		}
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(w, "      Compile: %s\n", strings.Join(parts, ", "))
}
