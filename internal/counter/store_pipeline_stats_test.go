package counter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/gputrace/internal/trace"
)

// openFixture opens a committed capture-only trace bundle.
func openFixture(t *testing.T, name string) *trace.Trace {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "traces", name, name+"-run1.gputrace")
	tr, err := trace.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

// TestPerfFixtureStreamDataStoreAgreement compares the two archived sources
// in a real profiler capture. The fixture is intentionally opt-in because the
// capture is several gigabytes and is not part of this repository.
func TestPerfFixtureStreamDataStoreAgreement(t *testing.T) {
	fixture := testtrace.Path("GPUTRACE_PERF_FIXTURE", testtrace.Bundle)
	if fixture == "" {
		t.Skip("set GPUTRACE_TEST_TRACE or GPUTRACE_PERF_FIXTURE to a .gputrace bundle")
	}
	fixture, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	root := fixture
	perfDir := fixture
	if strings.HasSuffix(filepath.Base(fixture), ".gpuprofiler_raw") {
		root = filepath.Dir(fixture)
	} else {
		entries, readErr := os.ReadDir(fixture)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var found bool
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".gpuprofiler_raw") {
				perfDir = filepath.Join(fixture, entry.Name())
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no .gpuprofiler_raw sidecar in %s", fixture)
		}
	}

	stream, err := ParseStreamData(perfDir, nil)
	if err != nil {
		t.Fatalf("parse streamData: %v", err)
	}
	store, err := ExtractStoreStats(&trace.Trace{Path: root}, 0)
	if err != nil {
		t.Fatalf("parse store0: %v", err)
	}
	agreement := 0
	checked := 0
	for _, pipeline := range stream.Pipelines {
		if pipeline.FunctionName == "" {
			continue
		}
		checked++
		other := store.PipelineForLabel(pipeline.FunctionName)
		if other == nil {
			t.Errorf("store has no pipeline %q", pipeline.FunctionName)
			continue
		}
		if pipeline.InstructionCount != other.InstructionCount ||
			pipeline.TemporaryRegisterCount != other.TemporaryRegisterCount ||
			pipeline.UniformRegisterCount != other.UniformRegisterCount ||
			pipeline.SpilledBytes != other.SpilledBytes ||
			pipeline.ThreadgroupMemory != other.ThreadgroupMemory ||
			pipeline.ConstantCalculationTemporaryRegisterCount != other.ConstantCalculationTemporaryRegisterCount ||
			pipeline.ConstantCalculationPhasePresent != other.ConstantCalculationPhasePresent {
			t.Errorf("pipeline %q disagrees: stream=%+v store=%+v", pipeline.FunctionName, pipeline, *other)
			continue
		}
		agreement++
	}
	if checked == 0 {
		t.Fatal("streamData contained no named pipelines")
	}
	if agreement != checked {
		t.Fatalf("stream/store agreement = %d/%d named pipelines", agreement, checked)
	}
}

// TestExtractStoreStats checks the statistics Xcode archived for each fixture.
// The fixture names describe the register and ALU pressure their kernels were
// written to exercise, so the decoded values must agree with those names.
func TestExtractStoreStats(t *testing.T) {
	tests := []struct {
		name         string
		function     string
		tempRegs     int
		uniformRegs  int
		instructions int
		alu          int
	}{
		{"low-alu-simple-add", "simple_add", 3, 8, 6, 1},
		{"high-alu-complex-math", "complex_math", 19, 16, 59, 51},
		{"high-occupancy-low-registers", "low_register_pressure", 2, 4, 5, 1},
		{"low-occupancy-high-registers", "high_register_pressure", 23, 4, 80, 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats, err := ExtractStoreStats(openFixture(t, tt.name), 0)
			if err != nil {
				t.Fatalf("extract store stats: %v", err)
			}
			if len(stats.Pipelines) != 1 {
				t.Fatalf("pipelines = %d, want 1", len(stats.Pipelines))
			}

			ps := stats.Pipelines[0]
			if ps.FunctionName != tt.function {
				t.Errorf("function name = %q, want %q", ps.FunctionName, tt.function)
			}
			if ps.TemporaryRegisterCount != tt.tempRegs {
				t.Errorf("temporary registers = %d, want %d", ps.TemporaryRegisterCount, tt.tempRegs)
			}
			if ps.UniformRegisterCount != tt.uniformRegs {
				t.Errorf("uniform registers = %d, want %d", ps.UniformRegisterCount, tt.uniformRegs)
			}
			if ps.InstructionCount != tt.instructions {
				t.Errorf("instruction count = %d, want %d", ps.InstructionCount, tt.instructions)
			}
			if ps.ALUInstructionCount != tt.alu {
				t.Errorf("ALU instruction count = %d, want %d", ps.ALUInstructionCount, tt.alu)
			}
		})
	}
}

// TestExtractStoreStatsSixEncoders checks that every encoder in a multi-kernel
// capture contributes its own statistics section, in dispatch order.
func TestExtractStoreStatsSixEncoders(t *testing.T) {
	stats, err := ExtractStoreStats(openFixture(t, "06-six-encoders"), 0)
	if err != nil {
		t.Fatalf("extract store stats: %v", err)
	}

	want := []string{
		"simple_add",
		"simple_multiply",
		"simple_subtract",
		"simple_divide",
		"complex_math",
		"low_register_pressure",
	}
	if len(stats.Pipelines) != len(want) {
		t.Fatalf("pipelines = %d, want %d", len(stats.Pipelines), len(want))
	}
	for i, name := range want {
		if got := stats.Pipelines[i].FunctionName; got != name {
			t.Errorf("pipeline %d function name = %q, want %q", i, got, name)
		}
	}

	// complex_math is the only kernel here with branching control flow.
	complexMath := stats.Pipelines[4]
	if complexMath.BranchInstructionCount != 1 {
		t.Errorf("complex_math branch count = %d, want 1", complexMath.BranchInstructionCount)
	}
	if complexMath.FP32InstructionCount != 38 {
		t.Errorf("complex_math FP32 count = %d, want 38", complexMath.FP32InstructionCount)
	}

	// Every kernel in this capture reads two buffers and writes one, except
	// low_register_pressure, which reads one.
	for i, ps := range stats.Pipelines {
		if ps.DeviceStoreCount != 1 {
			t.Errorf("pipeline %d device stores = %d, want 1", i, ps.DeviceStoreCount)
		}
		if ps.SpilledBytes != 0 {
			t.Errorf("pipeline %d spilled bytes = %d, want 0", i, ps.SpilledBytes)
		}
	}
}

// TestExtractStoreStatsSource checks that the archived Metal source is
// recovered alongside the statistics.
func TestExtractStoreStatsSource(t *testing.T) {
	stats, err := ExtractStoreStats(openFixture(t, "06-six-encoders"), 0)
	if err != nil {
		t.Fatalf("extract store stats: %v", err)
	}
	if !strings.Contains(stats.Source, "kernel void simple_add(") {
		t.Errorf("source does not declare simple_add:\n%s", stats.Source)
	}
	if !strings.Contains(stats.Source, "kernel void complex_math(") {
		t.Errorf("source does not declare complex_math:\n%s", stats.Source)
	}
}

// TestExtractStoreStatsMissingStore reports an error rather than empty stats.
func TestExtractStoreStatsMissingStore(t *testing.T) {
	if _, err := ExtractStoreStats(openFixture(t, "06-six-encoders"), 99); err == nil {
		t.Fatal("expected an error for a store that does not exist")
	}
}

// TestResolveKeyedDictionaryOutOfRange checks that a truncated archive, whose
// dictionary still references objects that were cut off, is skipped rather
// than indexed out of range.
func TestResolveKeyedDictionaryOutOfRange(t *testing.T) {
	objects := []interface{}{"$null", "Instruction count", 6}
	dict := map[string]interface{}{
		"NS.keys":    []interface{}{plist.UID(1), plist.UID(99)},
		"NS.objects": []interface{}{plist.UID(2), plist.UID(99)},
	}

	resolved := resolveKeyedDictionary(objects, dict)
	if got := resolved["Instruction count"]; got != 6 {
		t.Errorf("instruction count = %v, want 6", got)
	}
	if len(resolved) != 1 {
		t.Errorf("resolved %d entries, want 1: %v", len(resolved), resolved)
	}
}
