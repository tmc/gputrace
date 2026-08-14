-- gputrace.perfettosql/v1
-- Load this file after opening a native gputrace Perfetto trace.

CREATE PERFETTO VIEW gputrace_capture AS
SELECT
  id,
  extract_arg(arg_set_id, 'debug.schema') AS schema,
  extract_arg(arg_set_id, 'debug.exporter_version') AS exporter_version,
  extract_arg(arg_set_id, 'debug.exporter_commit') AS exporter_commit,
  extract_arg(arg_set_id, 'debug.exporter_build_date') AS exporter_build_date,
  extract_arg(arg_set_id, 'debug.perfetto_schema_revision') AS perfetto_schema_revision,
  extract_arg(arg_set_id, 'debug.input_uuid') AS input_uuid,
  extract_arg(arg_set_id, 'debug.input_uuid_availability') AS input_uuid_availability,
  extract_arg(arg_set_id, 'debug.input_content_digest') AS input_content_digest,
  extract_arg(arg_set_id, 'debug.input_content_digest_availability') AS input_content_digest_availability,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.clock_mapping') AS clock_mapping,
  extract_arg(arg_set_id, 'debug.clock_conversion_domain') AS clock_conversion_domain,
  extract_arg(arg_set_id, 'debug.clock_conversion_availability') AS clock_conversion_availability,
  extract_arg(arg_set_id, 'debug.clock_conversion_source') AS clock_conversion_source,
  extract_arg(arg_set_id, 'debug.clock_conversion_formula') AS clock_conversion_formula,
  cast(extract_arg(arg_set_id, 'debug.absolute_time') AS INT) AS absolute_time,
  cast(extract_arg(arg_set_id, 'debug.timebase_numer') AS INT) AS timebase_numer,
  cast(extract_arg(arg_set_id, 'debug.timebase_denom') AS INT) AS timebase_denom,
  cast(extract_arg(arg_set_id, 'debug.continuous_time') AS INT) AS continuous_time,
  extract_arg(arg_set_id, 'debug.continuous_time_domain') AS continuous_time_domain,
  extract_arg(arg_set_id, 'debug.continuous_time_availability') AS continuous_time_availability,
  cast(extract_arg(arg_set_id, 'debug.pstate') AS INT) AS pstate,
  extract_arg(arg_set_id, 'debug.pstate_source') AS pstate_source,
  extract_arg(arg_set_id, 'debug.pstate_semantics') AS pstate_semantics,
  extract_arg(arg_set_id, 'debug.pstate_availability') AS pstate_availability,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  cast(extract_arg(arg_set_id, 'debug.timing_approximate') AS INT) AS timing_approximate,
  extract_arg(arg_set_id, 'debug.encoder_timing_source') AS encoder_timing_source,
  cast(extract_arg(arg_set_id, 'debug.encoder_timing_approximate') AS INT) AS encoder_timing_approximate,
  cast(extract_arg(arg_set_id, 'debug.encoder_span_ns') AS INT) AS encoder_span_ns,
  cast(extract_arg(arg_set_id, 'debug.dispatch_span_ns') AS INT) AS dispatch_span_ns,
  cast(extract_arg(arg_set_id, 'debug.effective_gpu_time_ns') AS INT) AS effective_gpu_time_ns,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_active_time_ns') AS INT) AS command_buffer_active_time_ns,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_wall_time_ns') AS INT) AS command_buffer_wall_time_ns,
  cast(extract_arg(arg_set_id, 'debug.restore_active_time_ns') AS INT) AS restore_active_time_ns,
  cast(extract_arg(arg_set_id, 'debug.restore_wall_time_ns') AS INT) AS restore_wall_time_ns,
  cast(extract_arg(arg_set_id, 'debug.display_duration_ns') AS INT) AS display_duration_ns,
  extract_arg(arg_set_id, 'debug.display_duration_source') AS display_duration_source,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_count') AS INT) AS command_buffer_count,
  cast(extract_arg(arg_set_id, 'debug.source_restore_interval_count') AS INT) AS source_restore_interval_count,
  cast(extract_arg(arg_set_id, 'debug.projected_restore_interval_count') AS INT) AS projected_restore_interval_count,
  cast(extract_arg(arg_set_id, 'debug.encoder_count') AS INT) AS encoder_count,
  cast(extract_arg(arg_set_id, 'debug.dispatch_count') AS INT) AS dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.untimed_dispatch_count') AS INT) AS untimed_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.source_command_buffer_count') AS INT) AS source_command_buffer_count,
  cast(extract_arg(arg_set_id, 'debug.source_encoder_count') AS INT) AS source_encoder_count,
  cast(extract_arg(arg_set_id, 'debug.source_dispatch_count') AS INT) AS source_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.source_untimed_dispatch_count') AS INT) AS source_untimed_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.source_raw_profiler_stream_count') AS INT) AS source_raw_profiler_stream_count,
  cast(extract_arg(arg_set_id, 'debug.source_raw_profiler_record_count') AS INT) AS source_raw_profiler_record_count,
  cast(extract_arg(arg_set_id, 'debug.projected_command_buffer_count') AS INT) AS projected_command_buffer_count,
  cast(extract_arg(arg_set_id, 'debug.projected_encoder_count') AS INT) AS projected_encoder_count,
  cast(extract_arg(arg_set_id, 'debug.projected_dispatch_count') AS INT) AS projected_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.projected_untimed_dispatch_count') AS INT) AS projected_untimed_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.projected_raw_profiler_stream_count') AS INT) AS projected_raw_profiler_stream_count,
  cast(extract_arg(arg_set_id, 'debug.projected_raw_profiler_record_count') AS INT) AS projected_raw_profiler_record_count,
  cast(extract_arg(arg_set_id, 'debug.observed_cs_label_count') AS INT) AS observed_cs_label_count,
  cast(extract_arg(arg_set_id, 'debug.unique_cs_label_count') AS INT) AS unique_cs_label_count,
  extract_arg(arg_set_id, 'debug.cs_label_semantics') AS cs_label_semantics,
  cast(extract_arg(arg_set_id, 'debug.raw_profiler_samples') AS INT) AS raw_profiler_samples,
  extract_arg(arg_set_id, 'debug.environment_schema') AS environment_schema,
  extract_arg(arg_set_id, 'debug.environment_os') AS environment_os,
  extract_arg(arg_set_id, 'debug.environment_arch') AS environment_arch,
  extract_arg(arg_set_id, 'debug.environment_exporter_runtime') AS environment_exporter_runtime,
  cast(extract_arg(arg_set_id, 'debug.environment_device_id') AS INT) AS environment_device_id,
  extract_arg(arg_set_id, 'debug.environment_device_id_availability') AS environment_device_id_availability,
  extract_arg(arg_set_id, 'debug.environment_device_name') AS environment_device_name,
  extract_arg(arg_set_id, 'debug.environment_device_name_source') AS environment_device_name_source,
  extract_arg(arg_set_id, 'debug.environment_device_name_availability') AS environment_device_name_availability,
  extract_arg(arg_set_id, 'debug.environment_device_availability') AS environment_device_availability,
  extract_arg(arg_set_id, 'debug.environment_metal_plugin_name') AS environment_metal_plugin_name,
  extract_arg(arg_set_id, 'debug.environment_metal_plugin_source') AS environment_metal_plugin_source,
  extract_arg(arg_set_id, 'debug.environment_metal_plugin_availability') AS environment_metal_plugin_availability,
  cast(extract_arg(arg_set_id, 'debug.environment_gpu_generation') AS INT) AS environment_gpu_generation,
  extract_arg(arg_set_id, 'debug.environment_gpu_generation_source') AS environment_gpu_generation_source,
  extract_arg(arg_set_id, 'debug.environment_gpu_generation_availability') AS environment_gpu_generation_availability,
  extract_arg(arg_set_id, 'debug.stream_data_metadata_availability') AS stream_data_metadata_availability,
  extract_arg(arg_set_id, 'debug.stream_data_metadata_source') AS stream_data_metadata_source,
  cast(extract_arg(arg_set_id, 'debug.stream_data_version') AS INT) AS stream_data_version,
  cast(extract_arg(arg_set_id, 'debug.stream_data_unix_timestamp') AS INT) AS stream_data_unix_timestamp,
  extract_arg(arg_set_id, 'debug.stream_data_trace_name') AS stream_data_trace_name,
  cast(extract_arg(arg_set_id, 'debug.stream_data_profiled_execution_mode') AS INT) AS stream_data_profiled_execution_mode,
  cast(extract_arg(arg_set_id, 'debug.stream_data_profiled_performance_state') AS INT) AS stream_data_profiled_performance_state,
  cast(extract_arg(arg_set_id, 'debug.stream_data_profiled_profiler_mode') AS INT) AS stream_data_profiled_profiler_mode,
  extract_arg(arg_set_id, 'debug.stream_data_profile_mode_semantics') AS stream_data_profile_mode_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_capture_range_location') AS INT) AS stream_data_capture_range_location,
  cast(extract_arg(arg_set_id, 'debug.stream_data_capture_range_length') AS INT) AS stream_data_capture_range_length,
  extract_arg(arg_set_id, 'debug.stream_data_capture_range_semantics') AS stream_data_capture_range_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_has_unused_resources') AS INT) AS stream_data_has_unused_resources,
  cast(extract_arg(arg_set_id, 'debug.stream_data_supports_separate_aps_data') AS INT) AS stream_data_supports_separate_aps_data,
  cast(extract_arg(arg_set_id, 'debug.stream_data_num_blit_calls') AS INT) AS stream_data_num_blit_calls,
  cast(extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_bytes') AS INT) AS stream_data_command_buffer_table_bytes,
  cast(extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_record_size') AS INT) AS stream_data_command_buffer_table_record_size,
  cast(extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_record_count') AS INT) AS stream_data_command_buffer_table_record_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_remainder_bytes') AS INT) AS stream_data_command_buffer_table_remainder_bytes,
  extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_availability') AS stream_data_command_buffer_table_availability,
  extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_integrity') AS stream_data_command_buffer_table_integrity,
  extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_sha256') AS stream_data_command_buffer_table_sha256,
  extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_raw_bytes_availability') AS stream_data_command_buffer_table_raw_bytes_availability,
  extract_arg(arg_set_id, 'debug.stream_data_command_buffer_table_decode_error') AS stream_data_command_buffer_table_decode_error,
  cast(extract_arg(arg_set_id, 'debug.stream_data_encoder_table_bytes') AS INT) AS stream_data_encoder_table_bytes,
  cast(extract_arg(arg_set_id, 'debug.stream_data_encoder_table_record_size') AS INT) AS stream_data_encoder_table_record_size,
  cast(extract_arg(arg_set_id, 'debug.stream_data_encoder_table_record_count') AS INT) AS stream_data_encoder_table_record_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_encoder_table_remainder_bytes') AS INT) AS stream_data_encoder_table_remainder_bytes,
  extract_arg(arg_set_id, 'debug.stream_data_encoder_table_availability') AS stream_data_encoder_table_availability,
  extract_arg(arg_set_id, 'debug.stream_data_encoder_table_integrity') AS stream_data_encoder_table_integrity,
  extract_arg(arg_set_id, 'debug.stream_data_encoder_table_sha256') AS stream_data_encoder_table_sha256,
  extract_arg(arg_set_id, 'debug.stream_data_encoder_table_raw_bytes_availability') AS stream_data_encoder_table_raw_bytes_availability,
  extract_arg(arg_set_id, 'debug.stream_data_encoder_table_decode_error') AS stream_data_encoder_table_decode_error,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_bytes') AS INT) AS stream_data_gpu_command_table_bytes,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_record_size') AS INT) AS stream_data_gpu_command_table_record_size,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_record_count') AS INT) AS stream_data_gpu_command_table_record_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_remainder_bytes') AS INT) AS stream_data_gpu_command_table_remainder_bytes,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_availability') AS stream_data_gpu_command_table_availability,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_integrity') AS stream_data_gpu_command_table_integrity,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_sha256') AS stream_data_gpu_command_table_sha256,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_raw_bytes_availability') AS stream_data_gpu_command_table_raw_bytes_availability,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_command_table_decode_error') AS stream_data_gpu_command_table_decode_error,
  cast(extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_bytes') AS INT) AS stream_data_pipeline_table_bytes,
  cast(extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_record_size') AS INT) AS stream_data_pipeline_table_record_size,
  cast(extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_record_count') AS INT) AS stream_data_pipeline_table_record_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_remainder_bytes') AS INT) AS stream_data_pipeline_table_remainder_bytes,
  extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_availability') AS stream_data_pipeline_table_availability,
  extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_integrity') AS stream_data_pipeline_table_integrity,
  extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_sha256') AS stream_data_pipeline_table_sha256,
  extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_raw_bytes_availability') AS stream_data_pipeline_table_raw_bytes_availability,
  extract_arg(arg_set_id, 'debug.stream_data_pipeline_table_decode_error') AS stream_data_pipeline_table_decode_error,
  cast(extract_arg(arg_set_id, 'debug.stream_data_function_table_bytes') AS INT) AS stream_data_function_table_bytes,
  cast(extract_arg(arg_set_id, 'debug.stream_data_function_table_record_size') AS INT) AS stream_data_function_table_record_size,
  cast(extract_arg(arg_set_id, 'debug.stream_data_function_table_record_count') AS INT) AS stream_data_function_table_record_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_function_table_remainder_bytes') AS INT) AS stream_data_function_table_remainder_bytes,
  extract_arg(arg_set_id, 'debug.stream_data_function_table_availability') AS stream_data_function_table_availability,
  extract_arg(arg_set_id, 'debug.stream_data_function_table_integrity') AS stream_data_function_table_integrity,
  extract_arg(arg_set_id, 'debug.stream_data_function_table_sha256') AS stream_data_function_table_sha256,
  extract_arg(arg_set_id, 'debug.stream_data_function_table_raw_bytes_availability') AS stream_data_function_table_raw_bytes_availability,
  extract_arg(arg_set_id, 'debug.stream_data_function_table_decode_error') AS stream_data_function_table_decode_error,
  extract_arg(arg_set_id, 'debug.stream_data_family_count_semantics') AS stream_data_family_count_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_entry_count') AS INT) AS stream_data_aps_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_data_availability') AS stream_data_aps_data_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_timeline_data_entry_count') AS INT) AS stream_data_aps_timeline_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_timeline_data_availability') AS stream_data_aps_timeline_data_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_counter_data_entry_count') AS INT) AS stream_data_aps_counter_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_counter_data_availability') AS stream_data_aps_counter_data_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_shader_profiler_data_entry_count') AS INT) AS stream_data_shader_profiler_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_shader_profiler_data_availability') AS stream_data_shader_profiler_data_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_timeline_data_entry_count') AS INT) AS stream_data_gpu_timeline_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_timeline_data_availability') AS stream_data_gpu_timeline_data_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_batch_id_filtered_counters_data_entry_count') AS INT) AS stream_data_batch_id_filtered_counters_data_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_batch_id_filtered_counters_data_availability') AS stream_data_batch_id_filtered_counters_data_availability,
  extract_arg(arg_set_id, 'debug.stream_data_decoded_family_count_semantics') AS stream_data_decoded_family_count_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_decoded_blob_count') AS INT) AS stream_data_aps_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_non_blob_entry_count') AS INT) AS stream_data_aps_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_data_decode_availability') AS stream_data_aps_data_decode_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_timeline_data_decoded_blob_count') AS INT) AS stream_data_aps_timeline_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_timeline_data_non_blob_entry_count') AS INT) AS stream_data_aps_timeline_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_timeline_data_decode_availability') AS stream_data_aps_timeline_data_decode_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_counter_data_decoded_blob_count') AS INT) AS stream_data_aps_counter_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_counter_data_non_blob_entry_count') AS INT) AS stream_data_aps_counter_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_aps_counter_data_decode_availability') AS stream_data_aps_counter_data_decode_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_shader_profiler_data_decoded_blob_count') AS INT) AS stream_data_shader_profiler_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_shader_profiler_data_non_blob_entry_count') AS INT) AS stream_data_shader_profiler_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_shader_profiler_data_decode_availability') AS stream_data_shader_profiler_data_decode_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_timeline_data_decoded_blob_count') AS INT) AS stream_data_gpu_timeline_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_gpu_timeline_data_non_blob_entry_count') AS INT) AS stream_data_gpu_timeline_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_gpu_timeline_data_decode_availability') AS stream_data_gpu_timeline_data_decode_availability,
  cast(extract_arg(arg_set_id, 'debug.stream_data_batch_id_filtered_counters_data_decoded_blob_count') AS INT) AS stream_data_batch_id_filtered_counters_data_decoded_blob_count,
  cast(extract_arg(arg_set_id, 'debug.stream_data_batch_id_filtered_counters_data_non_blob_entry_count') AS INT) AS stream_data_batch_id_filtered_counters_data_non_blob_entry_count,
  extract_arg(arg_set_id, 'debug.stream_data_batch_id_filtered_counters_data_decode_availability') AS stream_data_batch_id_filtered_counters_data_decode_availability,
  extract_arg(arg_set_id, 'debug.stream_data_counter_decode_availability') AS stream_data_counter_decode_availability,
  extract_arg(arg_set_id, 'debug.stream_data_counter_decode_count_semantics') AS stream_data_counter_decode_count_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_gprwcntr_blobs') AS INT) AS stream_data_counter_decode_gprwcntr_blobs,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_decoded_samples') AS INT) AS stream_data_counter_decode_decoded_samples,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_attributed_samples') AS INT) AS stream_data_counter_decode_attributed_samples,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_machine_wide_samples') AS INT) AS stream_data_counter_decode_machine_wide_samples,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_unattributed_samples') AS INT) AS stream_data_counter_decode_unattributed_samples,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_known_encoder_ids') AS INT) AS stream_data_counter_decode_known_encoder_ids,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_encoder_aggregates') AS INT) AS stream_data_counter_decode_encoder_aggregates,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_pass_column_groups') AS INT) AS stream_data_counter_decode_pass_column_groups,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_trace_id_rows') AS INT) AS stream_data_counter_decode_trace_id_rows,
  cast(extract_arg(arg_set_id, 'debug.stream_data_counter_decode_stride_mismatch_blobs') AS INT) AS stream_data_counter_decode_stride_mismatch_blobs,
  extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_availability') AS stream_data_aps_data_inventory_availability,
  extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_count_semantics') AS stream_data_aps_data_inventory_count_semantics,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_blobs') AS INT) AS stream_data_aps_data_inventory_blobs,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_dictionaries') AS INT) AS stream_data_aps_data_inventory_dictionaries,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_malformed_blobs') AS INT) AS stream_data_aps_data_inventory_malformed_blobs,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_with_counter_info') AS INT) AS stream_data_aps_data_inventory_with_counter_info,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_with_shader_profiler_data') AS INT) AS stream_data_aps_data_inventory_with_shader_profiler_data,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_with_frame_marker') AS INT) AS stream_data_aps_data_inventory_with_frame_marker,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_with_aps_trace_data_file') AS INT) AS stream_data_aps_data_inventory_with_aps_trace_data_file,
  cast(extract_arg(arg_set_id, 'debug.stream_data_aps_data_inventory_with_trace_id_tables') AS INT) AS stream_data_aps_data_inventory_with_trace_id_tables,
  extract_arg(arg_set_id, 'debug.environment_driver_availability') AS environment_driver_availability,
  extract_arg(arg_set_id, 'debug.environment_mlx_runtime_availability') AS environment_mlx_runtime_availability,
  extract_arg(arg_set_id, 'debug.environment_workload_availability') AS environment_workload_availability,
  extract_arg(arg_set_id, 'debug.environment_capability_catalog_availability') AS environment_capability_catalog_availability,
  extract_arg(arg_set_id, 'debug.capture_mode_availability') AS capture_mode_availability,
  extract_arg(arg_set_id, 'debug.replay_mode_availability') AS replay_mode_availability,
  extract_arg(arg_set_id, 'debug.counter_catalog_availability') AS counter_catalog_availability,
  cast(extract_arg(arg_set_id, 'debug.counter_catalog_entries') AS INT) AS counter_catalog_entries,
  extract_arg(arg_set_id, 'debug.counter_catalog_source') AS counter_catalog_source,
  extract_arg(arg_set_id, 'debug.counter_catalog_semantics') AS counter_catalog_semantics,
  extract_arg(arg_set_id, 'debug.counter_trace_id_availability') AS counter_trace_id_availability,
  cast(extract_arg(arg_set_id, 'debug.counter_trace_id_rows') AS INT) AS counter_trace_id_rows,
  extract_arg(arg_set_id, 'debug.counter_trace_id_source') AS counter_trace_id_source,
  extract_arg(arg_set_id, 'debug.counter_trace_id_semantics') AS counter_trace_id_semantics,
  extract_arg(arg_set_id, 'debug.counter_decoder_availability') AS counter_decoder_availability,
  extract_arg(arg_set_id, 'debug.raw_counter_artifact_availability') AS raw_counter_artifact_availability,
  cast(extract_arg(arg_set_id, 'debug.raw_profiler_artifact_count') AS INT) AS raw_profiler_artifact_count,
  cast(extract_arg(arg_set_id, 'debug.raw_profiler_artifact_total_bytes') AS INT) AS raw_profiler_artifact_total_bytes,
  extract_arg(arg_set_id, 'debug.raw_profiler_artifact_inventory_sha256') AS raw_profiler_artifact_inventory_sha256,
  extract_arg(arg_set_id, 'debug.raw_profiler_artifact_digest_algorithm') AS raw_profiler_artifact_digest_algorithm,
  extract_arg(arg_set_id, 'debug.raw_profiler_artifact_scope') AS raw_profiler_artifact_scope,
  cast(extract_arg(arg_set_id, 'debug.raw_profiler_timeline_header_count') AS INT) AS raw_profiler_timeline_header_count,
  extract_arg(arg_set_id, 'debug.raw_profiler_timeline_header_semantics') AS raw_profiler_timeline_header_semantics,
  cast(extract_arg(arg_set_id, 'debug.packet_family_gpu_info') AS INT) AS packet_family_gpu_info,
  cast(extract_arg(arg_set_id, 'debug.packet_family_gpu_render_stage_event') AS INT) AS packet_family_gpu_render_stage_event,
  cast(extract_arg(arg_set_id, 'debug.packet_family_track_event') AS INT) AS packet_family_track_event,
  cast(extract_arg(arg_set_id, 'debug.packet_family_gpu_counter_event') AS INT) AS packet_family_gpu_counter_event,
  extract_arg(arg_set_id, 'debug.resource_policy') AS resource_policy,
  cast(extract_arg(arg_set_id, 'debug.logical_byte_boundary') AS INT) AS logical_byte_boundary,
  cast(extract_arg(arg_set_id, 'debug.events_considered') AS INT) AS events_considered,
  cast(extract_arg(arg_set_id, 'debug.events_retained') AS INT) AS events_retained,
  cast(extract_arg(arg_set_id, 'debug.events_dropped') AS INT) AS events_dropped,
  cast(extract_arg(arg_set_id, 'debug.counter_samples_considered') AS INT) AS counter_samples_considered,
  cast(extract_arg(arg_set_id, 'debug.counter_samples_retained') AS INT) AS counter_samples_retained,
  cast(extract_arg(arg_set_id, 'debug.counter_samples_dropped') AS INT) AS counter_samples_dropped,
  cast(extract_arg(arg_set_id, 'debug.dependency_skeletons_retained') AS INT) AS dependency_skeletons_retained,
  extract_arg(arg_set_id, 'debug.first_dropped_identity') AS first_dropped_identity,
  extract_arg(arg_set_id, 'debug.last_dropped_identity') AS last_dropped_identity,
  cast(extract_arg(arg_set_id, 'debug.output_complete') AS INT) AS output_complete,
  cast(extract_arg(arg_set_id, 'debug.presentation_dispatch_tracks') AS INT) AS presentation_dispatch_tracks,
  cast(extract_arg(arg_set_id, 'debug.presentation_dispatch_events') AS INT) AS presentation_dispatch_events,
  extract_arg(arg_set_id, 'debug.presentation_dispatch_accounting') AS presentation_dispatch_accounting,
  extract_arg(arg_set_id, 'debug.live_timing_run_id') AS live_timing_run_id,
  extract_arg(arg_set_id, 'debug.live_timing_digest') AS live_timing_digest,
  cast(extract_arg(arg_set_id, 'debug.live_timing_clock_samples') AS INT) AS live_timing_clock_samples,
  cast(extract_arg(arg_set_id, 'debug.live_timing_command_buffers') AS INT) AS live_timing_command_buffers,
  cast(extract_arg(arg_set_id, 'debug.live_timing_projected_command_buffers') AS INT) AS live_timing_projected_command_buffers,
  cast(extract_arg(arg_set_id, 'debug.live_timing_unmatched_command_buffers') AS INT) AS live_timing_unmatched_command_buffers,
  extract_arg(arg_set_id, 'debug.host_correlation_schema') AS host_correlation_schema,
  extract_arg(arg_set_id, 'debug.host_correlation_run_id') AS host_correlation_run_id,
  extract_arg(arg_set_id, 'debug.host_correlation_host_digest') AS host_correlation_host_digest,
  extract_arg(arg_set_id, 'debug.host_correlation_trace_digest') AS host_correlation_trace_digest,
  extract_arg(arg_set_id, 'debug.host_correlation_host_clock') AS host_correlation_host_clock,
  extract_arg(arg_set_id, 'debug.host_correlation_gpu_clock') AS host_correlation_gpu_clock,
  extract_arg(arg_set_id, 'debug.host_correlation_bridge_digest') AS host_correlation_bridge_digest,
  cast(extract_arg(arg_set_id, 'debug.host_correlation_max_error_ns') AS REAL) AS host_correlation_max_error_ns,
  cast(extract_arg(arg_set_id, 'debug.host_correlation_event_count') AS INT) AS host_correlation_event_count,
  extract_arg(arg_set_id, 'debug.mlx_semantic_schema') AS mlx_semantic_schema,
  extract_arg(arg_set_id, 'debug.mlx_semantic_producer_name') AS mlx_semantic_producer_name,
  extract_arg(arg_set_id, 'debug.mlx_semantic_producer_version') AS mlx_semantic_producer_version,
  cast(extract_arg(arg_set_id, 'debug.mlx_semantic_nodes') AS INT) AS mlx_semantic_nodes,
  cast(extract_arg(arg_set_id, 'debug.mlx_semantic_links') AS INT) AS mlx_semantic_links,
  extract_arg(arg_set_id, 'debug.mlx_sidecar_digest') AS mlx_sidecar_digest,
  cast(extract_arg(arg_set_id, 'debug.mlx_semantic_used_nodes') AS INT) AS mlx_semantic_used_nodes,
  cast(extract_arg(arg_set_id, 'debug.mlx_semantic_unused_nodes') AS INT) AS mlx_semantic_unused_nodes,
  cast(extract_arg(arg_set_id, 'debug.unattributed_counter_rows') AS INT) AS unattributed_counter_rows,
  extract_arg(arg_set_id, 'debug.counter_attribution') AS counter_attribution,
  extract_arg(arg_set_id, 'debug.counter_attribution_reason') AS counter_attribution_reason,
  cast(extract_arg(arg_set_id, 'debug.unavailable_evidence_count') AS INT) AS unavailable_evidence_count,
  arg_set_id
FROM slice
WHERE name = 'gputrace evidence manifest';

-- gputrace_manifest_arg is the lossless manifest projection. Typed columns in
-- gputrace_capture cover common queries; this view keeps new and per-class
-- receipt fields queryable without waiting for a module schema change.
CREATE PERFETTO VIEW gputrace_manifest_arg AS
SELECT
  s.id AS capture_id,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.name = 'gputrace evidence manifest'
  AND a.key GLOB 'debug.*';

CREATE PERFETTO VIEW gputrace_dispatch AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'dispatch_index') AS INT) AS dispatch_id,
  cast(extract_arg(arg_set_id, 'encoder_index') AS INT) AS encoder_id,
  cast(extract_arg(arg_set_id, 'pipeline_id') AS INT) AS pipeline_id,
  cast(extract_arg(arg_set_id, 'pipeline_idx') AS INT) AS pipeline_index,
  extract_arg(arg_set_id, 'pipeline_state') AS pipeline_state,
  cast(extract_arg(arg_set_id, 'pipeline_address') AS INT) AS pipeline_address,
  extract_arg(arg_set_id, 'pipeline_identity_source') AS pipeline_identity_source,
  extract_arg(arg_set_id, 'pipeline_identity_scope') AS pipeline_identity_scope,
  cast(extract_arg(arg_set_id, 'command_buffer_index') AS INT) AS command_buffer_id,
  cast(extract_arg(arg_set_id, 'capture_offset') AS INT) AS capture_offset,
  extract_arg(arg_set_id, 'capture_structure_source') AS capture_structure_source,
  extract_arg(arg_set_id, 'timing_source') AS timing_source,
  extract_arg(arg_set_id, 'timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'encoder_containment') AS parent_basis,
  'streamData gpuCommandInfoData functionName' AS function_attribution,
  NULL AS coordinate_source,
  cast(extract_arg(arg_set_id, 'cumulative_us') AS INT) AS source_cumulative_us,
  cast(extract_arg(arg_set_id, 'simd_groups') AS INT) AS simd_groups,
  extract_arg(arg_set_id, 'grid_size') AS grid_size,
  extract_arg(arg_set_id, 'threadgroup_size') AS threadgroup_size,
  extract_arg(arg_set_id, 'geometry_source') AS geometry_source,
  extract_arg(arg_set_id, 'xcode_type') AS xcode_type,
  extract_arg(arg_set_id, 'xcode_view') AS xcode_view,
  extract_arg(arg_set_id, 'source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'source_line') AS INT) AS source_line,
  cast(extract_arg(arg_set_id, 'source_available') AS INT) AS source_available,
  cast(extract_arg(arg_set_id, 'gprwcntr_sample_count') AS INT) AS sample_count,
  cast(extract_arg(arg_set_id, 'sampling_density') AS REAL) AS sampling_density,
  extract_arg(arg_set_id, 'sample_attribution_basis') AS sample_attribution_basis,
  cast(extract_arg(arg_set_id, 'start_ticks') AS INT) AS sample_window_start_ticks,
  cast(extract_arg(arg_set_id, 'end_ticks') AS INT) AS sample_window_end_ticks,
  extract_arg(arg_set_id, 'sample_window_basis') AS sample_window_basis,
  extract_arg(arg_set_id, 'sample_timestamp_domain') AS sample_timestamp_domain,
  cast(extract_arg(arg_set_id, 'profiling_sample_share_estimate_pct') AS REAL) AS profiling_sample_share_estimate_pct,
  'measured_gpu_execution' AS evidence_kind,
  arg_set_id
FROM gpu_slice
UNION ALL
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.dispatch_index') AS INT) AS dispatch_id,
  cast(extract_arg(arg_set_id, 'debug.encoder_index') AS INT) AS encoder_id,
  cast(extract_arg(arg_set_id, 'debug.pipeline_id') AS INT) AS pipeline_id,
  NULL AS pipeline_index,
  coalesce(
    extract_arg(arg_set_id, 'debug.pipeline_state'),
    extract_arg(arg_set_id, 'debug.pipeline_address')
  ) AS pipeline_state,
  cast(extract_arg(arg_set_id, 'debug.pipeline_address') AS INT) AS pipeline_address,
  'capture dispatch record' AS pipeline_identity_source,
  'capture-local' AS pipeline_identity_scope,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_index') AS INT) AS command_buffer_id,
  cast(extract_arg(arg_set_id, 'debug.capture_offset') AS INT) AS capture_offset,
  'capture dispatch record' AS capture_structure_source,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.encoder_attribution') AS parent_basis,
  extract_arg(arg_set_id, 'debug.function_attribution') AS function_attribution,
  extract_arg(arg_set_id, 'debug.coordinate_source') AS coordinate_source,
  NULL AS source_cumulative_us,
  cast(extract_arg(arg_set_id, 'debug.simd_groups') AS INT) AS simd_groups,
  extract_arg(arg_set_id, 'debug.grid_size') AS grid_size,
  extract_arg(arg_set_id, 'debug.threadgroup_size') AS threadgroup_size,
  'capture dispatch record' AS geometry_source,
  extract_arg(arg_set_id, 'debug.xcode_type') AS xcode_type,
  extract_arg(arg_set_id, 'debug.xcode_view') AS xcode_view,
  extract_arg(arg_set_id, 'debug.source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'debug.source_line') AS INT) AS source_line,
  cast(extract_arg(arg_set_id, 'debug.source_available') AS INT) AS source_available,
  NULL AS sample_count,
  NULL AS sampling_density,
  NULL AS sample_attribution_basis,
  NULL AS sample_window_start_ticks,
  NULL AS sample_window_end_ticks,
  NULL AS sample_window_basis,
  NULL AS sample_timestamp_domain,
  NULL AS profiling_sample_share_estimate_pct,
  'recorded_dispatch' AS evidence_kind,
  arg_set_id
FROM slice
WHERE category = 'dispatch';

-- gputrace_dispatch_arg is the lossless argument projection. Native GPU rows
-- use unprefixed argument keys; generic recorded dispatches use debug.* keys.
CREATE PERFETTO VIEW gputrace_dispatch_arg AS
SELECT
  g.id AS dispatch_id,
  'measured_gpu_execution' AS evidence_kind,
  a.key AS key,
  a.flat_key AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM gpu_slice AS g
JOIN args AS a USING (arg_set_id)
UNION ALL
SELECT
  s.id AS dispatch_id,
  'recorded_dispatch' AS evidence_kind,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category = 'dispatch'
  AND a.key GLOB 'debug.*';

CREATE PERFETTO VIEW gputrace_encoder AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.index') AS INT) AS encoder_id,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  cast(extract_arg(arg_set_id, 'debug.timing_approximate') AS INT) AS timing_approximate,
  cast(extract_arg(arg_set_id, 'debug.real_timing') AS INT) AS real_timing,
  cast(extract_arg(arg_set_id, 'debug.gpu_cycles') AS INT) AS gpu_cycles,
  extract_arg(arg_set_id, 'debug.gpu_cycles_source') AS gpu_cycles_source,
  cast(extract_arg(arg_set_id, 'debug.execution_cost_pct') AS REAL) AS derived_execution_cost_pct,
  extract_arg(arg_set_id, 'debug.execution_cost_formula') AS execution_cost_formula,
  extract_arg(arg_set_id, 'debug.counter_attribution_basis') AS counter_attribution_basis,
  extract_arg(arg_set_id, 'debug.counter_coverage') AS counter_coverage,
  cast(extract_arg(arg_set_id, 'debug.counter_end_records') AS INT) AS counter_end_records,
  cast(extract_arg(arg_set_id, 'debug.counter_sample_count') AS INT) AS counter_sample_count,
  cast(extract_arg(arg_set_id, 'debug.counter_batch_id') AS INT) AS counter_batch_id,
  extract_arg(arg_set_id, 'debug.counter_batch_id_source') AS counter_batch_id_source,
  cast(extract_arg(arg_set_id, 'debug.counter_sample_index') AS INT) AS counter_sample_index,
  extract_arg(arg_set_id, 'debug.counter_sample_index_source') AS counter_sample_index_source,
  extract_arg(arg_set_id, 'debug.counter_trace_id_relation') AS counter_trace_id_relation,
  'aggregate details only; counter sample timestamps are not joined to the busy clock' AS counter_clock_relation,
  arg_set_id
FROM slice
WHERE category = 'encoder';

-- gputrace_encoder_arg retains every encoder argument, including fields also
-- present in the typed view, so callers can inspect newer exporter fields.
CREATE PERFETTO VIEW gputrace_encoder_arg AS
SELECT
  s.id AS encoder_id,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category = 'encoder'
  AND a.key GLOB 'debug.*';

CREATE PERFETTO VIEW gputrace_command_buffer AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.index') AS INT) AS command_buffer_id,
  CASE
    WHEN extract_arg(arg_set_id, 'debug.timing_quality') = 'measured' THEN ts
  END AS measured_wall_start_ns,
  CASE
    WHEN extract_arg(arg_set_id, 'debug.timing_quality') = 'measured' THEN dur
  END AS measured_wall_duration_ns,
  cast(extract_arg(arg_set_id, 'debug.offset') AS INT) AS capture_offset,
  cast(extract_arg(arg_set_id, 'debug.start_ticks') AS INT) AS source_start_ticks,
  cast(extract_arg(arg_set_id, 'debug.end_ticks') AS INT) AS source_end_ticks,
  cast(extract_arg(arg_set_id, 'debug.raw_start_offset_ns') AS INT) AS source_wall_offset_ns,
  extract_arg(arg_set_id, 'debug.coordinate_source') AS coordinate_source,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  cast(extract_arg(arg_set_id, 'debug.real_timing') AS INT) AS real_timing,
  CASE
    WHEN extract_arg(arg_set_id, 'debug.timing_quality') = 'measured'
      THEN 'measured_wall_span'
    ELSE 'recorded_command_buffer'
  END AS evidence_kind,
  CASE
    WHEN extract_arg(arg_set_id, 'debug.timing_quality') = 'measured'
      THEN 'wall clock only; no mapping to cumulative GPU-busy time'
    ELSE 'capture record order only; wall timing unavailable'
  END AS clock_relation,
  arg_set_id
FROM slice
WHERE category = 'command_buffer';

CREATE PERFETTO VIEW gputrace_restore_interval AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.index') AS INT) AS restore_interval_id,
  cast(extract_arg(arg_set_id, 'debug.start_ticks') AS INT) AS source_start_ticks,
  cast(extract_arg(arg_set_id, 'debug.end_ticks') AS INT) AS source_end_ticks,
  cast(extract_arg(arg_set_id, 'debug.raw_start_offset_ns') AS INT) AS source_wall_offset_ns,
  cast(extract_arg(arg_set_id, 'debug.duration_ns') AS INT) AS source_duration_ns,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.evidence_kind') AS evidence_kind,
  'replay restore activity; not GPU execution' AS accounting_scope,
  arg_set_id
FROM slice
WHERE category = 'restore';

CREATE PERFETTO VIEW gputrace_profiler_stream AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.index') AS INT) AS stream_id,
  extract_arg(arg_set_id, 'debug.source') AS source,
  cast(extract_arg(arg_set_id, 'debug.ring_buffer_idx') AS INT) AS ring_buffer_index,
  cast(extract_arg(arg_set_id, 'debug.sample_count') AS INT) AS sample_count,
  cast(extract_arg(arg_set_id, 'debug.start_ticks') AS INT) AS source_start_ticks,
  cast(extract_arg(arg_set_id, 'debug.end_ticks') AS INT) AS source_end_ticks,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  cast(extract_arg(arg_set_id, 'debug.real_timing') AS INT) AS real_timing,
  'raw profiler stream aggregate; not a GPU encoder interval' AS evidence_kind,
  arg_set_id
FROM slice
WHERE category = 'profiler_stream';

CREATE PERFETTO VIEW gputrace_raw_profiler_sample AS
SELECT
  id,
  ts,
  name,
  cast(extract_arg(arg_set_id, 'debug.record_index') AS INT) AS source_record_index,
  cast(extract_arg(arg_set_id, 'debug.stream_index') AS INT) AS stream_id,
  extract_arg(arg_set_id, 'debug.stream_source') AS stream_source,
  cast(extract_arg(arg_set_id, 'debug.stream_ring_buffer_index') AS INT) AS stream_ring_buffer_index,
  cast(extract_arg(arg_set_id, 'debug.stream_sample_count') AS INT) AS stream_sample_count,
  cast(extract_arg(arg_set_id, 'debug.stream_machine_wide_samples') AS INT) AS stream_machine_wide_samples,
  extract_arg(arg_set_id, 'debug.stream_carrier') AS stream_carrier,
  cast(extract_arg(arg_set_id, 'debug.timestamp_ticks') AS INT) AS source_timestamp_ticks,
  extract_arg(arg_set_id, 'debug.timestamp_domain') AS source_timestamp_domain,
  extract_arg(arg_set_id, 'debug.coordinate_basis') AS coordinate_basis,
  cast(extract_arg(arg_set_id, 'debug.grc_gpu_cycles_raw') AS INT) AS grc_gpu_cycles_raw,
  cast(extract_arg(arg_set_id, 'debug.grc_sample_type_raw') AS INT) AS grc_sample_type_raw,
  cast(extract_arg(arg_set_id, 'debug.grc_encoder_id_raw') AS INT) AS grc_encoder_id_raw,
  cast(extract_arg(arg_set_id, 'debug.grc_kick_trace_id_raw') AS INT) AS grc_kick_trace_id_raw,
  cast(extract_arg(arg_set_id, 'debug.grc_kick_slot_index_raw') AS INT) AS grc_kick_slot_index_raw,
  cast(extract_arg(arg_set_id, 'debug.grc_source_id_raw') AS INT) AS grc_source_id_raw,
  cast(extract_arg(arg_set_id, 'debug.machine_wide') AS INT) AS machine_wide,
  cast(extract_arg(arg_set_id, 'debug.record_stride_bytes') AS INT) AS record_stride_bytes,
  cast(extract_arg(arg_set_id, 'debug.record_column_count') AS INT) AS record_column_count,
  cast(extract_arg(arg_set_id, 'debug.hardware_counter_columns') AS INT) AS hardware_counter_columns,
  extract_arg(arg_set_id, 'debug.record_format') AS record_format,
  extract_arg(arg_set_id, 'debug.counter_decode_status') AS counter_decode_status,
  extract_arg(arg_set_id, 'debug.counter_catalog_join') AS counter_catalog_join,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'gprwcntr';

CREATE PERFETTO VIEW gputrace_counter_catalog AS
SELECT
  id,
  cast(extract_arg(arg_set_id, 'debug.group_ordinal') AS INT) AS group_ordinal,
  cast(extract_arg(arg_set_id, 'debug.column_ordinal') AS INT) AS column_ordinal,
  extract_arg(arg_set_id, 'debug.recorded_name') AS recorded_name,
  extract_arg(arg_set_id, 'debug.classification') AS classification,
  extract_arg(arg_set_id, 'debug.source') AS source,
  extract_arg(arg_set_id, 'debug.semantics') AS semantics,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'counter_catalog';

CREATE PERFETTO VIEW gputrace_counter_trace_id AS
SELECT
  s.id,
  cast(extract_arg(s.arg_set_id, 'debug.row_ordinal') AS INT) AS row_ordinal,
  tid.int_value AS trace_id_int64,
  tid.display_value AS trace_id_uint64,
  cast(extract_arg(s.arg_set_id, 'debug.batch_id') AS INT) AS batch_id,
  cast(extract_arg(s.arg_set_id, 'debug.sample_index') AS INT) AS sample_index,
  extract_arg(s.arg_set_id, 'debug.source') AS source,
  extract_arg(s.arg_set_id, 'debug.semantics') AS semantics,
  extract_arg(s.arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(s.arg_set_id, 'debug.timing_quality') AS timing_quality,
  s.arg_set_id
FROM slice AS s
JOIN args AS tid
  ON tid.arg_set_id = s.arg_set_id AND tid.key = 'debug.trace_id'
WHERE s.category = 'counter_trace_id';

-- gputrace_stream_data_table retains the exact bytes of the five fixed-record
-- tables in streamData. Record boundaries follow record_size; unknown words
-- and relationships between tables are not decoded by this view.
CREATE PERFETTO VIEW gputrace_stream_data_table AS
SELECT
  id,
  extract_arg(arg_set_id, 'debug.table_name') AS table_name,
  extract_arg(arg_set_id, 'debug.source_key') AS source_key,
  cast(extract_arg(arg_set_id, 'debug.byte_count') AS INT) AS byte_count,
  extract_arg(arg_set_id, 'debug.raw_bytes_hex') AS raw_bytes_hex,
  extract_arg(arg_set_id, 'debug.table_sha256') AS table_sha256,
  cast(extract_arg(arg_set_id, 'debug.record_size') AS INT) AS record_size,
  cast(extract_arg(arg_set_id, 'debug.record_count') AS INT) AS record_count,
  cast(extract_arg(arg_set_id, 'debug.remainder_bytes') AS INT) AS remainder_bytes,
  extract_arg(arg_set_id, 'debug.source') AS source,
  extract_arg(arg_set_id, 'debug.semantics') AS semantics,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'stream_data_table';

-- gputrace_raw_profiler_sample_arg retains the unnamed hardware-counter
-- payload after the seven fixed GRC columns. counter_ordinal is only its
-- position in the recorded pass; it does not identify a counter or unit.
CREATE PERFETTO VIEW gputrace_raw_profiler_sample_arg AS
SELECT
  s.id AS sample_id,
  cast(extract_arg(s.arg_set_id, 'debug.stream_index') AS INT) AS stream_id,
  cast(extract_arg(s.arg_set_id, 'debug.record_index') AS INT) AS source_record_index,
  cast(replace(replace(a.key, 'debug.hardware_counter_', ''), '_raw', '') AS INT) AS counter_ordinal,
  a.int_value AS raw_value_int64,
  a.display_value AS raw_value_uint64,
  'ordinal only; counter name, unit, and interpretation unavailable' AS semantics
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category = 'gprwcntr'
  AND a.key GLOB 'debug.hardware_counter_[0-9]*_raw';

-- gputrace_track_event_arg is the lossless extension surface for low-volume
-- generic track events. event_id is a trace-processor-local join key, not a
-- persistent source identity. High-volume dispatch and raw-sample arguments
-- have dedicated views.
CREATE PERFETTO VIEW gputrace_track_event_arg AS
SELECT
  s.id AS event_id,
  s.category,
  s.name AS event_name,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category IN (
    'command_buffer', 'profiler_stream', 'live_command_buffer',
    'host_signpost', 'evidence_gap'
  )
  AND a.key GLOB 'debug.*';

CREATE PERFETTO VIEW gputrace_live_command_buffer AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_id') AS INT) AS command_buffer_id,
  extract_arg(arg_set_id, 'debug.capture_label') AS capture_label,
  extract_arg(arg_set_id, 'debug.final_label') AS final_label,
  cast(extract_arg(arg_set_id, 'debug.kernel_start_ns') AS INT) AS kernel_start_ns,
  cast(extract_arg(arg_set_id, 'debug.kernel_duration_ns') AS INT) AS kernel_duration_ns,
  extract_arg(arg_set_id, 'debug.kernel_timing_source') AS kernel_timing_source,
  extract_arg(arg_set_id, 'debug.run_id') AS run_id,
  extract_arg(arg_set_id, 'debug.sidecar_digest') AS sidecar_digest,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  'measured original-execution GPU interval from a trace-identified sidecar' AS evidence_kind,
  arg_set_id
FROM slice
WHERE category = 'live_command_buffer';

CREATE PERFETTO VIEW gputrace_host_signpost AS
SELECT
  id,
  ts,
  dur,
  name,
  extract_arg(arg_set_id, 'debug.event_id') AS event_id,
  extract_arg(arg_set_id, 'debug.join_basis') AS join_basis,
  extract_arg(arg_set_id, 'debug.run_id') AS run_id,
  extract_arg(arg_set_id, 'debug.host_digest') AS host_digest,
  extract_arg(arg_set_id, 'debug.trace_digest') AS trace_digest,
  extract_arg(arg_set_id, 'debug.host_clock') AS host_clock,
  extract_arg(arg_set_id, 'debug.clock_domain') AS gpu_clock,
  extract_arg(arg_set_id, 'debug.bridge_digest') AS bridge_digest,
  cast(extract_arg(arg_set_id, 'debug.max_error_ns') AS REAL) AS max_error_ns,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'host_signpost';

CREATE PERFETTO VIEW gputrace_function AS
SELECT
  evidence_kind,
  name AS function_name,
  count(*) AS dispatch_count,
  sum(CASE WHEN evidence_kind = 'measured_gpu_execution' THEN dur END) AS measured_duration_ns,
  sum(simd_groups) AS total_simd_groups,
  max(cast(coalesce(
    extract_arg(arg_set_id, 'function_simd_groups'),
    extract_arg(arg_set_id, 'debug.function_simd_groups')
  ) AS INT)) AS source_aggregate_simd_groups,
  max(coalesce(
    extract_arg(arg_set_id, 'function_simd_groups_source'),
    extract_arg(arg_set_id, 'debug.function_simd_groups_source')
  )) AS source_aggregate_simd_groups_basis,
  max(cast(coalesce(
    extract_arg(arg_set_id, 'shader_duration_ns'),
    extract_arg(arg_set_id, 'debug.shader_duration_ns')
  ) AS INT)) AS source_aggregate_duration_ns,
  max(cast(coalesce(
    extract_arg(arg_set_id, 'simd_group_share_pct'),
    extract_arg(arg_set_id, 'shader_share_pct'),
    extract_arg(arg_set_id, 'debug.simd_group_share_pct'),
    extract_arg(arg_set_id, 'debug.shader_share_pct')
  ) AS REAL)) AS work_share_pct,
  max(coalesce(
    extract_arg(arg_set_id, 'simd_group_share_source'),
    extract_arg(arg_set_id, 'shader_share_source'),
    extract_arg(arg_set_id, 'debug.simd_group_share_source'),
    extract_arg(arg_set_id, 'debug.shader_share_source')
  )) AS work_share_basis
FROM gputrace_dispatch
GROUP BY evidence_kind, function_name;

CREATE PERFETTO VIEW gputrace_pipeline AS
SELECT
  pipeline_id,
  pipeline_state,
  pipeline_address,
  name AS function_name,
  max(pipeline_identity_source) AS pipeline_identity_source,
  max(pipeline_identity_scope) AS pipeline_identity_scope,
  count(*) AS dispatch_count,
  sum(CASE WHEN evidence_kind = 'measured_gpu_execution' THEN 1 ELSE 0 END) AS measured_dispatch_count,
  sum(CASE WHEN evidence_kind = 'recorded_dispatch' THEN 1 ELSE 0 END) AS recorded_dispatch_count,
  sum(CASE WHEN evidence_kind = 'measured_gpu_execution' THEN dur END) AS measured_duration_ns,
  max(cast(coalesce(extract_arg(arg_set_id, 'allocated_registers'), extract_arg(arg_set_id, 'debug.allocated_registers')) AS INT)) AS allocated_registers,
  max(cast(coalesce(extract_arg(arg_set_id, 'uniform_registers'), extract_arg(arg_set_id, 'debug.uniform_registers')) AS INT)) AS uniform_registers,
  max(cast(coalesce(extract_arg(arg_set_id, 'spilled_bytes'), extract_arg(arg_set_id, 'debug.spilled_bytes')) AS INT)) AS spilled_bytes,
  max(cast(coalesce(extract_arg(arg_set_id, 'thread_invariant_spilled'), extract_arg(arg_set_id, 'debug.thread_invariant_spilled')) AS INT)) AS thread_invariant_spilled,
  max(cast(coalesce(extract_arg(arg_set_id, 'threadgroup_memory'), extract_arg(arg_set_id, 'debug.threadgroup_memory')) AS INT)) AS threadgroup_memory,
  max(cast(coalesce(extract_arg(arg_set_id, 'instruction_count'), extract_arg(arg_set_id, 'debug.instruction_count')) AS INT)) AS instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'alu_instruction_count'), extract_arg(arg_set_id, 'debug.alu_instruction_count')) AS INT)) AS alu_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'fp32_instruction_count'), extract_arg(arg_set_id, 'debug.fp32_instruction_count')) AS INT)) AS fp32_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'fp16_instruction_count'), extract_arg(arg_set_id, 'debug.fp16_instruction_count')) AS INT)) AS fp16_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'int32_instruction_count'), extract_arg(arg_set_id, 'debug.int32_instruction_count')) AS INT)) AS int32_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'int16_instruction_count'), extract_arg(arg_set_id, 'debug.int16_instruction_count')) AS INT)) AS int16_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'branch_instruction_count'), extract_arg(arg_set_id, 'debug.branch_instruction_count')) AS INT)) AS branch_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'device_load_instruction_count'), extract_arg(arg_set_id, 'debug.device_load_instruction_count')) AS INT)) AS device_load_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'device_store_instruction_count'), extract_arg(arg_set_id, 'debug.device_store_instruction_count')) AS INT)) AS device_store_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'device_atomic_instruction_count'), extract_arg(arg_set_id, 'debug.device_atomic_instruction_count')) AS INT)) AS device_atomic_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'texture_reads_instruction_count'), extract_arg(arg_set_id, 'debug.texture_reads_instruction_count')) AS INT)) AS texture_reads_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'texture_writes_instruction_count'), extract_arg(arg_set_id, 'debug.texture_writes_instruction_count')) AS INT)) AS texture_writes_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'threadgroup_load_instruction_count'), extract_arg(arg_set_id, 'debug.threadgroup_load_instruction_count')) AS INT)) AS threadgroup_load_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'threadgroup_store_instruction_count'), extract_arg(arg_set_id, 'debug.threadgroup_store_instruction_count')) AS INT)) AS threadgroup_store_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'threadgroup_atomic_instruction_count'), extract_arg(arg_set_id, 'debug.threadgroup_atomic_instruction_count')) AS INT)) AS threadgroup_atomic_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'wait_instruction_count'), extract_arg(arg_set_id, 'debug.wait_instruction_count')) AS INT)) AS wait_instruction_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'constant_calculation_temporary_register_count'), extract_arg(arg_set_id, 'debug.constant_calculation_temporary_register_count')) AS INT)) AS constant_calculation_temporary_register_count,
  max(cast(coalesce(extract_arg(arg_set_id, 'constant_calculation_phase_present'), extract_arg(arg_set_id, 'debug.constant_calculation_phase_present')) AS INT)) AS constant_calculation_phase_present,
  max(cast(coalesce(extract_arg(arg_set_id, 'compilation_time_ms'), extract_arg(arg_set_id, 'debug.compilation_time_ms')) AS REAL)) AS compilation_time_ms,
  max(coalesce(extract_arg(arg_set_id, 'metrics_source'), extract_arg(arg_set_id, 'debug.metrics_source'))) AS compiler_metrics_source
FROM gputrace_dispatch
GROUP BY pipeline_id, pipeline_state, pipeline_address, function_name;

CREATE PERFETTO VIEW gputrace_semantic_node AS
SELECT
  id,
  ts,
  dur,
  name,
  extract_arg(arg_set_id, 'debug.semantic_id') AS semantic_id,
  extract_arg(arg_set_id, 'debug.semantic_parent_id') AS semantic_parent_id,
  extract_arg(arg_set_id, 'debug.semantic_kind') AS semantic_kind,
  extract_arg(arg_set_id, 'debug.join_basis') AS join_basis
FROM slice
WHERE category = 'mlx_semantic_node';

CREATE PERFETTO VIEW gputrace_semantic_link AS
SELECT
  id,
  ts,
  dur,
  name,
  extract_arg(arg_set_id, 'debug.semantic_link_id') AS semantic_link_id,
  extract_arg(arg_set_id, 'debug.semantic_id') AS semantic_id,
  extract_arg(arg_set_id, 'debug.semantic_kind') AS semantic_kind,
  extract_arg(arg_set_id, 'debug.target_kind') AS target_kind,
  cast(extract_arg(arg_set_id, 'debug.target_index') AS INT) AS target_index,
  extract_arg(arg_set_id, 'debug.join_basis') AS join_basis
FROM slice
WHERE category = 'mlx_semantic';

-- gputrace_semantic_arg retains arbitrary sidecar attributes without making
-- their names part of the stable typed schema. Structural fields remain in
-- gputrace_semantic_node and gputrace_semantic_link.
CREATE PERFETTO VIEW gputrace_semantic_arg AS
SELECT
  s.id AS event_id,
  s.category AS event_kind,
  extract_arg(s.arg_set_id, 'debug.semantic_id') AS semantic_id,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category IN ('mlx_semantic_node', 'mlx_semantic')
  AND a.key GLOB 'debug.*'
  AND a.key NOT IN (
    'debug.semantic_id', 'debug.semantic_parent_id', 'debug.semantic_kind',
    'debug.semantic_link_id', 'debug.join_basis', 'debug.target_kind',
    'debug.target_index', 'debug.clock_domain', 'debug.timing_source',
    'debug.timing_quality'
  );

CREATE PERFETTO VIEW gputrace_counter_series AS
SELECT
  ct.id,
  ct.name,
  ct.unit,
  ct.description,
  count(c.id) AS sample_count,
  min(c.ts) AS first_sample_ts,
  max(c.ts) AS last_sample_ts
FROM counter_track AS ct
LEFT JOIN counter AS c ON c.track_id = ct.id
GROUP BY ct.id, ct.name, ct.unit, ct.description;

CREATE PERFETTO VIEW gputrace_unattributed_counter AS
SELECT
  id,
  name,
  extract_arg(arg_set_id, 'debug.pipeline_label') AS pipeline_label,
  extract_arg(arg_set_id, 'debug.attribution') AS attribution,
  extract_arg(arg_set_id, 'debug.attribution_reason') AS attribution_reason,
  extract_arg(arg_set_id, 'debug.metric_scope') AS metric_scope,
  extract_arg(arg_set_id, 'debug.source') AS source,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'counter_attribution';

CREATE PERFETTO VIEW gputrace_unattributed_counter_arg AS
SELECT
  s.id AS counter_id,
  substr(a.key, 7) AS key,
  substr(a.flat_key, 7) AS flat_key,
  a.value_type,
  a.int_value,
  a.real_value,
  a.string_value,
  a.display_value
FROM slice AS s
JOIN args AS a USING (arg_set_id)
WHERE s.category = 'counter_attribution'
  AND a.key GLOB 'debug.*'
  AND a.key NOT IN (
    'debug.pipeline_label', 'debug.attribution', 'debug.attribution_reason',
    'debug.metric_scope', 'debug.source', 'debug.clock_domain',
    'debug.timing_quality'
  );

CREATE PERFETTO VIEW gputrace_evidence_gap AS
SELECT
  id,
  name,
  extract_arg(arg_set_id, 'debug.family') AS family,
  extract_arg(arg_set_id, 'debug.reason') AS reason,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  arg_set_id
FROM slice
WHERE category = 'evidence_gap';

CREATE PERFETTO VIEW gputrace_raw_profiler_artifact AS
SELECT
  id,
  name,
  extract_arg(arg_set_id, 'debug.name') AS artifact_name,
  extract_arg(arg_set_id, 'debug.kind') AS artifact_kind,
  cast(extract_arg(arg_set_id, 'debug.file_index') AS INT) AS file_index,
  cast(extract_arg(arg_set_id, 'debug.size_bytes') AS INT) AS size_bytes,
  extract_arg(arg_set_id, 'debug.sha256') AS sha256,
  extract_arg(arg_set_id, 'debug.digest_algorithm') AS digest_algorithm,
  extract_arg(arg_set_id, 'debug.path_scope') AS path_scope,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  arg_set_id
FROM slice
WHERE category = 'raw_profiler_artifact';

CREATE PERFETTO VIEW gputrace_raw_profiler_timeline AS
SELECT
  id,
  extract_arg(arg_set_id, 'debug.name') AS artifact_name,
  cast(extract_arg(arg_set_id, 'debug.file_index') AS INT) AS file_index,
  cast(extract_arg(arg_set_id, 'debug.size_bytes') AS INT) AS size_bytes,
  extract_arg(arg_set_id, 'debug.sha256') AS sha256,
  extract_arg(arg_set_id, 'debug.timeline_header_magic') AS header_magic,
  cast(extract_arg(arg_set_id, 'debug.timeline_counter_count') AS INT) AS counter_count,
  cast(extract_arg(arg_set_id, 'debug.timeline_data_offset_bytes') AS INT) AS data_offset_bytes,
  cast(extract_arg(arg_set_id, 'debug.timeline_entry_count') AS INT) AS entry_count,
  cast(extract_arg(arg_set_id, 'debug.timeline_timestamp_raw') AS INT) AS timestamp_raw,
  extract_arg(arg_set_id, 'debug.timeline_timestamp_semantics') AS timestamp_semantics,
  arg_set_id
FROM slice
WHERE category = 'raw_profiler_artifact'
  AND extract_arg(arg_set_id, 'debug.kind') = 'timeline';

CREATE PERFETTO VIEW gputrace_unmatched AS
SELECT 'semantic_node' AS kind,
       extract_arg(arg_set_id, 'debug.mlx_semantic_unused_nodes') AS count
FROM slice
WHERE name = 'gputrace evidence manifest'
UNION ALL
SELECT 'dispatch', extract_arg(arg_set_id, 'debug.mlx_semantic_unmatched_dispatch')
FROM slice
WHERE name = 'gputrace evidence manifest'
UNION ALL
SELECT 'encoder', extract_arg(arg_set_id, 'debug.mlx_semantic_unmatched_encoder')
FROM slice
WHERE name = 'gputrace evidence manifest'
UNION ALL
SELECT 'command_buffer', extract_arg(arg_set_id, 'debug.mlx_semantic_unmatched_command_buffer')
FROM slice
WHERE name = 'gputrace evidence manifest';
