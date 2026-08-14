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
		"gputrace_aps_data_blob",
		"gputrace_aps_data_key",
		"gputrace_stream_data_archive_blob",
		"gputrace_stream_data_archive_key",
		"gputrace_stream_data_table",
		"gputrace_stream_data_string",
		"gputrace_pipeline_compiler",
		"gputrace_pipeline_compiler_remark",
		"gputrace_pipeline_compiler_remark_arg",
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
		"gputrace_counter_encoder_sample",
		"gputrace_counter_encoder_aggregate",
		"gputrace_unattributed_counter",
		"gputrace_unattributed_counter_arg",
		"gputrace_evidence_gap",
		"gputrace_raw_profiler_artifact",
		"gputrace_raw_profiler_timeline",
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
		"stream_data_metadata_availability",
		"stream_data_profiled_execution_mode",
		"stream_data_profile_mode_semantics",
		"stream_data_capture_range_semantics",
		"stream_data_has_unused_resources",
		"stream_data_num_blit_calls",
		"stream_data_command_buffer_table_record_count",
		"stream_data_encoder_table_remainder_bytes",
		"stream_data_gpu_command_table_integrity",
		"stream_data_pipeline_table_availability",
		"stream_data_function_table_bytes",
		"stream_data_function_table_sha256",
		"stream_data_function_table_raw_bytes_availability",
		"stream_data_function_table_decode_error",
		"stream_data_string_table_availability",
		"stream_data_string_count",
		"pipeline_compiler_availability",
		"pipeline_compiler_count_semantics",
		"pipeline_compiler_remark_source_location_count",
		"pipeline_compiler_remark_resolved_source_location_count",
		"pipeline_compiler_remark_unresolved_source_location_count",
		"pipeline_compiler_remark_argument_count",
		"pipeline_compiler_remark_argument_malformed_count",
		"pipeline_compiler_remark_argument_count_semantics",
		"counter_encoder_aggregate_availability",
		"counter_encoder_aggregate_count",
		"counter_encoder_aggregate_count_semantics",
		"counter_encoder_sample_availability",
		"counter_encoder_sample_count",
		"counter_values_json",
		"compiler_backend_ns",
		"recorded_statistics_json",
		"stream_data_family_count_semantics",
		"stream_data_aps_data_entry_count",
		"stream_data_aps_timeline_data_availability",
		"stream_data_aps_counter_data_entry_count",
		"stream_data_shader_profiler_data_entry_count",
		"stream_data_gpu_timeline_data_entry_count",
		"stream_data_batch_id_filtered_counters_data_availability",
		"stream_data_decoded_family_count_semantics",
		"stream_data_aps_data_decoded_blob_count",
		"stream_data_aps_data_non_blob_entry_count",
		"stream_data_aps_timeline_data_decode_availability",
		"stream_data_counter_decode_count_semantics",
		"stream_data_counter_decode_decoded_samples",
		"stream_data_counter_decode_attributed_samples",
		"stream_data_counter_decode_stride_mismatch_blobs",
		"stream_data_aps_data_inventory_count_semantics",
		"stream_data_aps_data_inventory_dictionaries",
		"stream_data_aps_data_inventory_with_aps_trace_data_file",
		"stream_data_aps_data_inventory_blob_record_count",
		"stream_data_aps_data_inventory_key_record_count",
		"stream_data_archive_blob_count",
		"stream_data_archive_aps_timeline_data_blob_count",
		"stream_data_archive_aps_counter_data_blob_count",
		"stream_data_archive_scalar_value_count",
		"stream_data_archive_descriptor_error_count",
		"scalar_json",
		"data_sha256",
		"container_count",
		"descriptor_error",
		"raw_profiler_artifact_inventory_sha256",
		"raw_profiler_artifact_total_bytes",
		"timeline_counter_count",
		"timeline_timestamp_semantics",
		"raw_profiler_timeline_header_count",
		"artifact_kind",
		"dependency_skeletons_retained",
		"presentation_dispatch_accounting",
		"a.display_value",
		"raw profiler stream aggregate; not a GPU encoder interval",
		"counter_decode_status",
		"grc_gpu_cycles_raw",
		"grc_sample_type_raw",
		"grc_encoder_id_raw",
		"grc_kick_trace_id_raw",
		"grc_kick_slot_index_raw",
		"grc_source_id_raw",
		"record_stride_bytes",
		"hardware_counter_columns",
		"streamData gpuCommandInfoData functionName",
		"source_available",
		"source_aggregate_simd_groups",
		"source_aggregate_simd_groups_basis",
		"live_timing_projected_command_buffers",
		"host_correlation_max_error_ns",
		"measured original-execution GPU interval from a trace-identified sidecar",
		"debug.kernel_start_ns",
		"debug.kernel_duration_ns",
		"debug.kernel_timing_source",
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

// TestModuleClassifiesEveryView keeps the stability tiers in the module header
// honest. A new view must be listed as stable or fall into a diagnostic
// family; neither is a default, so adding one is a deliberate choice.
func TestModuleClassifiesEveryView(t *testing.T) {
	header, _, ok := strings.Cut(Module, "\nCREATE PERFETTO VIEW")
	if !ok {
		t.Fatal("module has no views")
	}
	stable := make(map[string]bool)
	for _, field := range strings.Fields(header) {
		if strings.HasPrefix(field, "gputrace_") {
			stable[field] = true
		}
	}
	if len(stable) == 0 {
		t.Fatal("module header lists no stable views")
	}

	// Diagnostic families project recorded private archive structure. They are
	// named for what the decoder recovers, not for a user-facing concept.
	diagnostic := []string{
		"gputrace_aps_", "gputrace_counter_catalog", "gputrace_counter_encoder_",
		"gputrace_counter_info", "gputrace_counter_trace_id",
		"gputrace_embedded_profiler_", "gputrace_encoder_program_mapping",
		"gputrace_limiter_", "gputrace_pipeline_compiler",
		"gputrace_profiler_carrier", "gputrace_profiler_configuration",
		"gputrace_profiler_stream", "gputrace_program_address_mapping",
		"gputrace_raw_profiler_sample", "gputrace_shader_binary",
		"gputrace_stream_data_",
	}
	isDiagnostic := func(name string) bool {
		if strings.HasSuffix(name, "_audit") {
			return true
		}
		for _, prefix := range diagnostic {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}

	seen := make(map[string]bool)
	for rest := Module; ; {
		_, after, ok := strings.Cut(rest, "CREATE PERFETTO VIEW ")
		if !ok {
			break
		}
		name, remainder, _ := strings.Cut(after, " ")
		rest = remainder
		seen[name] = true
		switch {
		case stable[name] && isDiagnostic(name):
			t.Errorf("view %s is listed as stable and matches a diagnostic family", name)
		case !stable[name] && !isDiagnostic(name):
			t.Errorf("view %s is neither listed as stable nor in a diagnostic family", name)
		}
	}
	for name := range stable {
		if !seen[name] {
			t.Errorf("module header lists stable view %s, which the module does not define", name)
		}
	}
}
