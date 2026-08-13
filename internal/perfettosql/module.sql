-- gputrace.perfettosql/v1
-- Load this file after opening a native gputrace Perfetto trace.

CREATE PERFETTO VIEW gputrace_capture AS
SELECT
  id,
  extract_arg(arg_set_id, 'debug.schema') AS schema,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'debug.dispatch_count') AS dispatch_count,
  extract_arg(arg_set_id, 'debug.encoder_count') AS encoder_count,
  extract_arg(arg_set_id, 'debug.output_complete') AS output_complete
FROM slice
WHERE name = 'gputrace evidence manifest';

CREATE PERFETTO VIEW gputrace_dispatch AS
SELECT
  id,
  ts,
  dur,
  name,
  cast(extract_arg(arg_set_id, 'dispatch_index') AS INT) AS dispatch_id,
  cast(extract_arg(arg_set_id, 'encoder_index') AS INT) AS encoder_id,
  cast(extract_arg(arg_set_id, 'pipeline_id') AS INT) AS pipeline_id,
  extract_arg(arg_set_id, 'pipeline_state') AS pipeline_state,
  extract_arg(arg_set_id, 'timing_source') AS timing_source,
  extract_arg(arg_set_id, 'timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'encoder_containment') AS parent_basis,
  NULL AS function_attribution,
  NULL AS coordinate_source,
  cast(extract_arg(arg_set_id, 'simd_groups') AS INT) AS simd_groups,
  NULL AS grid_size,
  NULL AS threadgroup_size,
  extract_arg(arg_set_id, 'source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'source_line') AS INT) AS source_line,
  cast(extract_arg(arg_set_id, 'sample_count') AS INT) AS sample_count,
  cast(extract_arg(arg_set_id, 'sampling_density') AS REAL) AS sampling_density,
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
  coalesce(
    extract_arg(arg_set_id, 'debug.pipeline_state'),
    extract_arg(arg_set_id, 'debug.pipeline_address')
  ) AS pipeline_state,
  extract_arg(arg_set_id, 'debug.timing_source') AS timing_source,
  extract_arg(arg_set_id, 'debug.timing_quality') AS timing_quality,
  extract_arg(arg_set_id, 'debug.clock_domain') AS clock_domain,
  extract_arg(arg_set_id, 'debug.encoder_attribution') AS parent_basis,
  extract_arg(arg_set_id, 'debug.function_attribution') AS function_attribution,
  extract_arg(arg_set_id, 'debug.coordinate_source') AS coordinate_source,
  cast(extract_arg(arg_set_id, 'debug.simd_groups') AS INT) AS simd_groups,
  extract_arg(arg_set_id, 'debug.grid_size') AS grid_size,
  extract_arg(arg_set_id, 'debug.threadgroup_size') AS threadgroup_size,
  extract_arg(arg_set_id, 'debug.source_file') AS source_file,
  cast(extract_arg(arg_set_id, 'debug.source_line') AS INT) AS source_line,
  NULL AS sample_count,
  NULL AS sampling_density,
  NULL AS profiling_sample_share_estimate_pct,
  'recorded_dispatch' AS evidence_kind,
  arg_set_id
FROM slice
WHERE category = 'dispatch';

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
