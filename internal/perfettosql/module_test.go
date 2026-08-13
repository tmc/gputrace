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
		"gputrace_restore_interval",
		"gputrace_profiler_stream",
		"gputrace_raw_profiler_sample",
		"gputrace_track_event_arg",
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
		"environment_device_id_availability",
		"environment_device_name_source",
		"environment_device_name_availability",
		"environment_metal_plugin_source",
		"environment_metal_plugin_availability",
		"environment_gpu_generation_source",
		"environment_gpu_generation_availability",
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
		"effective_gpu_time_ns",
		"command_buffer_active_time_ns",
		"display_duration_source",
		"source_raw_profiler_record_count",
		"projected_command_buffer_count",
		"source_restore_interval_count",
		"projected_restore_interval_count",
		"replay restore activity; not GPU execution",
		"clock_conversion_availability",
		"debug.absolute_time",
		"debug.timebase_numer",
		"debug.timebase_denom",
		"debug.continuous_time",
		"continuous_time_availability",
		"debug.pstate",
		"pstate_availability",
		"pstate_semantics",
		"pipeline_address",
		"pipeline_identity_source",
		"pipeline_identity_scope",
		"count(*) AS dispatch_count",
		"measured_dispatch_count",
		"recorded_dispatch_count",
		"source_record_index",
		"s.category IN (",
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
