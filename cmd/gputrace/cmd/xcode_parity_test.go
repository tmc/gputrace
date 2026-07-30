//go:build darwin

package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/xcodebindings"
)

func TestParityTracePathFindsSingleBundle(t *testing.T) {
	path, err := parityTracePath("../../../testdata/traces/06-six-encoders")
	if err != nil {
		t.Fatal(err)
	}
	if want := "06-six-encoders-run1.gputrace"; len(path) < len(want) || path[len(path)-len(want):] != want {
		t.Fatalf("parityTracePath = %q, want suffix %q", path, want)
	}
}

// TestXcodeParityReportsStoreBackedFields checks which kernel fields a
// capture-only bundle can account for. The store sections archive shader
// compilation statistics, but not the counters Xcode derives at replay time.
func TestXcodeParityReportsStoreBackedFields(t *testing.T) {
	path, err := parityTracePath("../../../testdata/traces/06-six-encoders")
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := timelineForParity(path)
	if err != nil {
		t.Fatal(err)
	}
	report := buildXcodeParityReport(path, timeline, xcodebindings.Probe())
	present := []string{
		"allocated_registers",
		"uniform_registers",
		"spilled_bytes",
		"threadgroup_memory",
		"instruction_count",
	}
	for _, field := range present {
		if !slices.Contains(report.PresentFields, field) {
			t.Errorf("%s absent, want present: %v", field, report.PresentFields)
		}
	}
	for _, kernel := range timeline.Kernels {
		if kernel.Name == "simple_add" {
			if got := kernel.Args["source_available"]; got != true {
				t.Errorf("simple_add source_available = %#v, want true", got)
			}
			if got := kernel.Args["source_file"]; got == nil || !strings.HasSuffix(got.(string), "/store0") {
				t.Errorf("simple_add source_file = %#v, want store0", got)
			}
		}
	}

	// These are not archived in a capture-only bundle and must stay reported
	// as gaps rather than derived from the statistics above.
	absent := []string{
		"high_register",
		"occupancy_pct",
		"alu_utilization_pct",
		"xcode_cost_pct",
		"profiling_cost_pct",
		"pipeline_id",
	}
	for _, field := range absent {
		if slices.Contains(report.PresentFields, field) {
			t.Errorf("%s present, want absent", field)
		}
	}

	for _, field := range []string{"high_register", "occupancy_pct", "alu_utilization_pct", "effective_gpu_time"} {
		if !slices.ContainsFunc(report.RemainingGaps, func(g xcodeParityGap) bool { return g.Metric == field }) {
			t.Errorf("no remaining gap reported for %s", field)
		}
	}
}
