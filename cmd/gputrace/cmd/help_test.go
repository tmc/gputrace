package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandHelpRenders(t *testing.T) {
	walkCommands(t, rootCmd)
}

func walkCommands(t *testing.T, command *cobra.Command) {
	t.Helper()

	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Help(); err != nil {
		t.Fatalf("help failed for %q: %v", command.CommandPath(), err)
	}

	for _, sub := range command.Commands() {
		walkCommands(t, sub)
	}
}

func TestShadersHelpMarksHighRegisterSourceBacked(t *testing.T) {
	if !strings.Contains(shadersCmd.Long, "High Register, shown only when source-backed") {
		t.Fatalf("shaders help should not imply high register is always available:\n%s", shadersCmd.Long)
	}
}

func TestTimingHelpDocumentsTimingSources(t *testing.T) {
	help := timingCmd.Long
	for _, want := range []string{
		".gpuprofiler_raw/streamData",
		"APSTimelineData",
		"kdebug/signpost-derived timing",
		"synthetic timing for visualization only",
		"Hardware counter files",
		"not treated as direct shader timing",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("timing help does not contain %q:\n%s", want, help)
		}
	}

	for _, stale := range []string{
		"Traces without profiling\n      data will use synthetic/estimated timing",
		"hardware counters alone provide timing",
	} {
		if strings.Contains(help, stale) {
			t.Fatalf("timing help still contains stale wording %q:\n%s", stale, help)
		}
	}
}

func TestTimingProfilerHelpMarksLegacyApproximateFallbacks(t *testing.T) {
	help := timingProfilerCmd.Long
	for _, want := range []string{
		`Prefer "gputrace timing"`,
		".gpuprofiler_raw/streamData",
		"APSTimelineData",
		"kdebug GPU execution events",
		"counter-file limiter heuristics",
		"Counter files alone are not direct shader timing",
		"approximate",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("timing-profiler help does not contain %q:\n%s", want, help)
		}
	}

	for _, stale := range []string{
		"Extract GPU timing data from .gpuprofiler_raw hardware performance counters",
		"These files contain the same data that\nInstruments uses to calculate shader cost percentages",
		"The timing data extracted from performance counters is the most accurate available",
	} {
		if strings.Contains(help, stale) {
			t.Fatalf("timing-profiler help still contains stale wording %q:\n%s", stale, help)
		}
	}
}

func TestTimelineFormatHelpIncludesPerfetto(t *testing.T) {
	flag := timelineCmd.Flags().Lookup("format")
	if flag == nil {
		t.Fatal("timeline format flag not found")
	}
	if !strings.Contains(flag.Usage, "perfetto") {
		t.Fatalf("timeline format help does not mention perfetto: %s", flag.Usage)
	}
	if !strings.Contains(timelineCmd.Long, "timeline trace.gputrace --format chrome -o timeline.json") {
		t.Fatalf("timeline file-output example should include explicit non-text format:\n%s", timelineCmd.Long)
	}
	outputFlag := timelineCmd.Flags().Lookup("output")
	if outputFlag == nil {
		t.Fatal("timeline output flag not found")
	}
	if outputFlag.DefValue != "" {
		t.Fatalf("timeline output default = %q, want empty", outputFlag.DefValue)
	}
	if !strings.Contains(outputFlag.Usage, "stdout for text") {
		t.Fatalf("timeline output help does not describe format-specific default: %s", outputFlag.Usage)
	}
}

func TestGraphHelpMatchesDefaultType(t *testing.T) {
	flag := graphCmd.Flags().Lookup("type")
	if flag == nil {
		t.Fatal("graph type flag not found")
	}
	if flag.DefValue != "hierarchy" {
		t.Fatalf("graph type default = %q, want hierarchy", flag.DefValue)
	}
	if !strings.Contains(graphCmd.Long, "hierarchy: Command buffer") || !strings.Contains(graphCmd.Long, "(default)") {
		t.Fatalf("graph long help does not mark hierarchy as default:\n%s", graphCmd.Long)
	}
	if strings.Contains(graphCmd.Long, "flow: Execution flow (temporal order) - default") {
		t.Fatalf("graph long help still marks flow as default:\n%s", graphCmd.Long)
	}
}
