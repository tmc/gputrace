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
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  cast(extract_arg(arg_set_id, 'debug.timing_approximate') AS INT) AS timing_approximate,
  cast(extract_arg(arg_set_id, 'debug.command_buffer_count') AS INT) AS command_buffer_count,
  cast(extract_arg(arg_set_id, 'debug.encoder_count') AS INT) AS encoder_count,
  cast(extract_arg(arg_set_id, 'debug.dispatch_count') AS INT) AS dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.untimed_dispatch_count') AS INT) AS untimed_dispatch_count,
  cast(extract_arg(arg_set_id, 'debug.observed_cs_label_count') AS INT) AS observed_cs_label_count,
  cast(extract_arg(arg_set_id, 'debug.unique_cs_label_count') AS INT) AS unique_cs_label_count,
  extract_arg(arg_set_id, 'debug.cs_label_semantics') AS cs_label_semantics,
  cast(extract_arg(arg_set_id, 'debug.raw_profiler_samples') AS INT) AS raw_profiler_samples,
  extract_arg(arg_set_id, 'debug.environment_schema') AS environment_schema,
  extract_arg(arg_set_id, 'debug.environment_os') AS environment_os,
  extract_arg(arg_set_id, 'debug.environment_arch') AS environment_arch,
  extract_arg(arg_set_id, 'debug.environment_exporter_runtime') AS environment_exporter_runtime,
  cast(extract_arg(arg_set_id, 'debug.environment_device_id') AS INT) AS environment_device_id,
  extract_arg(arg_set_id, 'debug.environment_device_availability') AS environment_device_availability,
  extract_arg(arg_set_id, 'debug.environment_driver_availability') AS environment_driver_availability,
  extract_arg(arg_set_id, 'debug.environment_mlx_runtime_availability') AS environment_mlx_runtime_availability,
  extract_arg(arg_set_id, 'debug.environment_workload_availability') AS environment_workload_availability,
  extract_arg(arg_set_id, 'debug.environment_capability_catalog_availability') AS environment_capability_catalog_availability,
  extract_arg(arg_set_id, 'debug.capture_mode_availability') AS capture_mode_availability,
  extract_arg(arg_set_id, 'debug.replay_mode_availability') AS replay_mode_availability,
  extract_arg(arg_set_id, 'debug.counter_catalog_availability') AS counter_catalog_availability,
  extract_arg(arg_set_id, 'debug.counter_decoder_availability') AS counter_decoder_availability,
  extract_arg(arg_set_id, 'debug.raw_counter_artifact_availability') AS raw_counter_artifact_availability,
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
  cast(extract_arg(arg_set_id, 'command_buffer_index') AS INT) AS command_buffer_id,
  cast(extract_arg(arg_set_id, 'capture_offset') AS INT) AS capture_offset,
  extract_arg(arg_set_id, 'capture_structure_source') AS capture_structure_source,
  extract_arg(arg_set_id, 'timing_source') AS timing_source,
  extract_arg(arg_set_id, 'timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'encoder_containment') AS parent_basis,
  NULL AS function_attribution,
  NULL AS coordinate_source,
  cast(extract_arg(arg_set_id, 'cumulative_us') AS INT) AS source_cumulative_us,
  cast(extract_arg(arg_set_id, 'simd_groups') AS INT) AS simd_groups,
  extract_arg(arg_set_id, 'grid_size') AS grid_size,
  extract_arg(arg_set_id, 'threadgroup_size') AS threadgroup_size,
  extract_arg(arg_set_id, 'geometry_source') AS geometry_source,
  extract_arg(arg_set_id, 'source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'source_line') AS INT) AS source_line,
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
  extract_arg(arg_set_id, 'debug.source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'debug.source_line') AS INT) AS source_line,
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
  'aggregate details only; counter sample timestamps are not joined to the busy clock' AS counter_clock_relation,
  arg_set_id
FROM slice
WHERE category = 'encoder';

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

CREATE PERFETTO VIEW gputrace_function AS
SELECT
  evidence_kind,
  name AS function_name,
  count(*) AS dispatch_count,
  sum(CASE WHEN evidence_kind = 'measured_gpu_execution' THEN dur END) AS measured_duration_ns,
  sum(simd_groups) AS total_simd_groups,
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
  name AS function_name,
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
GROUP BY pipeline_id, pipeline_state, function_name;

CREATE PERFETTO VIEW gputrace_semantic_node AS
SELECT
  id,
  ts,
  dur,
  name,
  extract_arg(arg_set_id, 'debug.semantic_id') AS semantic_id,
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
  extract_arg(arg_set_id, 'debug.semantic_id') AS semantic_id,
  extract_arg(arg_set_id, 'debug.semantic_kind') AS semantic_kind,
  extract_arg(arg_set_id, 'debug.target_kind') AS target_kind,
  extract_arg(arg_set_id, 'debug.join_basis') AS join_basis
FROM slice
WHERE category = 'mlx_semantic';

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
