package cmd

import (
	"io"
	"os"
	"path/filepath"
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

func TestHelpExamplesUseRegisteredLongFlagSpelling(t *testing.T) {
	checks := []struct {
		name  string
		help  string
		wants []string
		stale []string
	}{
		{
			name:  "pprof",
			help:  pprofCmd.Long,
			wants: []string{"--all", "--prefix"},
			stale: []string{" -all ", " -prefix "},
		},
		{
			name:  "timing",
			help:  timingCmd.Long,
			wants: []string{"--json", "--csv", "--compare"},
			stale: []string{" -json ", " -csv ", " -compare "},
		},
	}

	for _, check := range checks {
		for _, want := range check.wants {
			if !strings.Contains(check.help, want) {
				t.Fatalf("%s help does not contain %q:\n%s", check.name, want, check.help)
			}
		}
		for _, stale := range check.stale {
			if strings.Contains(check.help, stale) {
				t.Fatalf("%s help still contains stale flag spelling %q:\n%s", check.name, stale, check.help)
			}
		}
	}
}

func TestReplayCountersHelpUsesRegisteredBoolFlagSpelling(t *testing.T) {
	help := replayCountersCmd.Long
	if !strings.Contains(help, "--dispatch-boundaries=false") {
		t.Fatalf("replay-counters help does not show registered false spelling:\n%s", help)
	}
	if strings.Contains(help, "--no-dispatch-boundaries") {
		t.Fatalf("replay-counters help still contains invalid --no-dispatch-boundaries spelling:\n%s", help)
	}
	if replayCountersCmd.Flags().Lookup("dispatch-boundaries") == nil {
		t.Fatal("replay-counters dispatch-boundaries flag not registered")
	}
}

func TestRootAndReadmeDoNotListMissingServe(t *testing.T) {
	if strings.Contains(rootCmd.Long, "\n  serve") {
		t.Fatalf("root help still lists missing serve command:\n%s", rootCmd.Long)
	}

	readmePath := filepath.Join("..", "..", "..", "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "`serve`") {
		t.Fatalf("README still lists missing serve command:\n%s", readme)
	}
}

func TestManualListsIncludeVisibleCommands(t *testing.T) {
	readmePath := filepath.Join("..", "..", "..", "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name       string
		rootDesc   string
		readmeDesc string
	}{
		{"version", "Print gputrace build version", "Print build version"},
		{"xcode-bindings", "Inspect private Xcode GTShaderProfiler bindings", "Inspect private Xcode GTShaderProfiler bindings"},
		{"xcode-parity", "Audit Xcode metric parity for a trace", "Audit Xcode metric parity for a trace"},
	}
	for _, check := range checks {
		cmd := visibleSubcommand(rootCmd, check.name)
		if cmd == nil {
			t.Fatalf("%s command is not visible", check.name)
		}
		if !manualHelpLists(rootCmd.Long, check.name, check.rootDesc) {
			t.Fatalf("root help does not list %s:\n%s", check.name, rootCmd.Long)
		}
		if !strings.Contains(string(readme), "| | `"+check.name+"` | "+check.readmeDesc+" |") {
			t.Fatalf("README does not list %s:\n%s", check.name, readme)
		}
	}
}

func TestHelpDoesNotReferenceMissingRelatedCommands(t *testing.T) {
	checks := []struct {
		name string
		help string
	}{
		{"shader-source", shaderSourceCmd.Long},
		{"export-counters", exportCountersCmd.Long},
		{"replay-counters", replayCountersCmd.Long},
	}
	for _, check := range checks {
		for _, stale := range []string{"shader-metrics", "perfcounters", "gputrace replay:"} {
			if strings.Contains(check.help, stale) {
				t.Fatalf("%s help still references missing command %q:\n%s", check.name, stale, check.help)
			}
		}
	}

	for _, want := range []string{"gputrace profiler", "gputrace xcode-profile xcode-export-counters"} {
		if !strings.Contains(exportCountersCmd.Long, want) {
			t.Fatalf("export-counters help does not contain existing related command %q:\n%s", want, exportCountersCmd.Long)
		}
		if !strings.Contains(replayCountersCmd.Long, want) {
			t.Fatalf("replay-counters help does not contain existing related command %q:\n%s", want, replayCountersCmd.Long)
		}
		if !registeredCommandPath(want) {
			t.Fatalf("related command path %q is not registered", want)
		}
	}

	if strings.Contains(exportCountersCmd.Long, "gputrace xcode-counters") {
		t.Fatalf("export-counters help still references missing xcode-counters command:\n%s", exportCountersCmd.Long)
	}
	if strings.Contains(replayCountersCmd.Long, "gputrace xcode-counters") {
		t.Fatalf("replay-counters help still references missing xcode-counters command:\n%s", replayCountersCmd.Long)
	}
}

func TestXcodeProfileHelpListsVertexOutput(t *testing.T) {
	cmd := visibleSubcommand(collectXcodeProfileCmd, "vertex-output")
	if cmd == nil {
		t.Fatal("xcode-profile vertex-output command is not visible")
	}
	if !manualHelpLists(collectXcodeProfileCmd.Long, "vertex-output", cmd.Short) {
		t.Fatalf("xcode-profile help does not list vertex-output:\n%s", collectXcodeProfileCmd.Long)
	}
}

func TestXcodeProfileHelpListsShowMemoryAction(t *testing.T) {
	cmd := visibleSubcommand(collectXcodeProfileCmd, "show-memory")
	if cmd == nil {
		t.Fatal("xcode-profile show-memory command is not visible")
	}
	if !manualHelpLists(collectXcodeProfileCmd.Long, "show-memory", cmd.Short) {
		t.Fatalf("xcode-profile help does not describe show-memory as %q:\n%s", cmd.Short, collectXcodeProfileCmd.Long)
	}
	if strings.Contains(collectXcodeProfileCmd.Long, "show-memory       Select Memory tab") {
		t.Fatalf("xcode-profile help still describes show-memory as selecting a tab:\n%s", collectXcodeProfileCmd.Long)
	}
}

func TestPerformanceHelpDescribesCountersTab(t *testing.T) {
	if !strings.Contains(performanceCmd.Long, "counters  Select the Counters tab") {
		t.Fatalf("performance help should describe counters as tab selection:\n%s", performanceCmd.Long)
	}
	if strings.Contains(performanceCmd.Long, "counters  Extract GPU counter values (planned)") {
		t.Fatalf("performance help still describes counters as planned extraction:\n%s", performanceCmd.Long)
	}
}

func TestShaderSourceHintsExampleMatchesDefault(t *testing.T) {
	if !strings.Contains(shaderSourceCmd.Long, "--hints=false") {
		t.Fatalf("shader-source help should show how to disable default hints:\n%s", shaderSourceCmd.Long)
	}
	if strings.Contains(shaderSourceCmd.Long, "Include optimization hints") {
		t.Fatalf("shader-source help still implies --hints is needed to include hints:\n%s", shaderSourceCmd.Long)
	}
}

func TestXcodeProfileExportUsageShowsOptionalOutputPath(t *testing.T) {
	exportCmd, _, err := collectXcodeProfileCmd.Find([]string{"export"})
	if err != nil {
		t.Fatal(err)
	}
	if exportCmd == nil || exportCmd.Name() != "export" {
		t.Fatalf("xcode-profile export command not found: %#v", exportCmd)
	}
	if exportCmd.Use != "export [output_path]" {
		t.Fatalf("xcode-profile export usage = %q, want %q", exportCmd.Use, "export [output_path]")
	}
	if err := exportCmd.Args(exportCmd, nil); err != nil {
		t.Fatalf("xcode-profile export should accept zero args: %v", err)
	}
	if err := exportCmd.Args(exportCmd, []string{"out.gputrace"}); err != nil {
		t.Fatalf("xcode-profile export should accept one arg: %v", err)
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

func visibleSubcommand(command *cobra.Command, name string) *cobra.Command {
	for _, sub := range command.Commands() {
		if sub.Name() == name && !sub.Hidden {
			return sub
		}
	}
	return nil
}

func manualHelpLists(help, name, desc string) bool {
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == name && strings.Contains(line, desc) {
			return true
		}
	}
	return false
}

func registeredCommandPath(path string) bool {
	fields := strings.Fields(path)
	if len(fields) == 0 || fields[0] != rootCmd.Name() {
		return false
	}
	cmd, remaining, err := rootCmd.Find(fields[1:])
	return err == nil && cmd != nil && len(remaining) == 0
}
