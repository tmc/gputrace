package perfettosql

import (
	"bytes"
	"strings"
	"testing"
)

func TestModuleDefinesStableViews(t *testing.T) {
	for _, name := range []string{
		"gputrace_capture",
		"gputrace_command_buffer",
		"gputrace_semantic_node",
		"gputrace_semantic_link",
		"gputrace_dispatch",
		"gputrace_encoder",
		"gputrace_function",
		"gputrace_pipeline",
		"gputrace_counter_series",
		"gputrace_unmatched",
	} {
		if !strings.Contains(Module, "CREATE PERFETTO VIEW "+name+" AS") {
			t.Errorf("module does not define %s", name)
		}
	}
	var out bytes.Buffer
	if err := Write(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != Module {
		t.Fatal("Write output differs from Module")
	}
}

func TestModuleProjectsRecordedDispatchesAndCompilerFacts(t *testing.T) {
	for _, want := range []string{
		"WHERE category = 'dispatch'",
		"'measured_gpu_execution' AS evidence_kind",
		"'recorded_dispatch' AS evidence_kind",
		"thread_invariant_spilled",
		"int32_instruction_count",
		"device_load_instruction_count",
		"threadgroup_store_instruction_count",
		"constant_calculation_phase_present",
		"compilation_time_ms",
		"metrics_source",
		"extract_arg(arg_set_id, 'debug.allocated_registers')",
		"debug.function_attribution",
		"debug.grid_size",
		"profiling_sample_share_estimate_pct",
		"cast(extract_arg(arg_set_id, 'dispatch_index') AS INT)",
		"debug.command_buffer_index",
		"capture_structure_source",
		"gprwcntr_sample_count",
		"sample_attribution_basis",
		"sample_timestamp_domain",
		"pipeline_idx",
		"geometry_source",
		"source_aggregate_duration_ns",
		"work_share_basis",
		"derived_execution_cost_pct",
		"counter_clock_relation",
		"measured_wall_start_ns",
		"recorded_command_buffer",
	} {
		if !strings.Contains(Module, want) {
			t.Errorf("module missing %q", want)
		}
	}
}
