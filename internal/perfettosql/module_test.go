package perfettosql

import (
	"bytes"
	"strings"
	"testing"
)

func TestModuleDefinesStableViews(t *testing.T) {
	for _, name := range []string{
		"gputrace_capture",
		"gputrace_manifest_arg",
		"gputrace_command_buffer",
		"gputrace_profiler_stream",
		"gputrace_raw_profiler_sample",
		"gputrace_live_command_buffer",
		"gputrace_host_signpost",
		"gputrace_semantic_node",
		"gputrace_semantic_link",
		"gputrace_semantic_arg",
		"gputrace_dispatch",
		"gputrace_dispatch_arg",
		"gputrace_encoder",
		"gputrace_encoder_arg",
		"gputrace_function",
		"gputrace_pipeline",
		"gputrace_counter_series",
		"gputrace_unattributed_counter",
		"gputrace_unattributed_counter_arg",
		"gputrace_evidence_gap",
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
		"input_uuid_availability",
		"environment_device_availability",
		"dependency_skeletons_retained",
		"presentation_dispatch_accounting",
		"a.display_value",
		"raw profiler stream aggregate; not a GPU encoder interval",
		"counter_decode_status",
		"streamData gpuCommandInfoData functionName",
		"source_available",
		"source_aggregate_simd_groups",
		"source_aggregate_simd_groups_basis",
		"live_timing_projected_command_buffers",
		"host_correlation_max_error_ns",
		"measured original-execution GPU interval from a trace-identified sidecar",
		"debug.bridge_digest",
		"semantic_parent_id",
		"semantic_link_id",
		"debug.target_index",
		"mlx_semantic_producer_version",
		"extract_arg(arg_set_id, 'xcode_type') AS xcode_type",
		"extract_arg(arg_set_id, 'debug.xcode_view') AS xcode_view",
		"'measured_gpu_execution' AS evidence_kind",
		"a.flat_key AS flat_key",
	} {
		if !strings.Contains(Module, want) {
			t.Errorf("module missing %q", want)
		}
	}
}
