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
  extract_arg(arg_set_id, 'dispatch_index') AS dispatch_id,
  extract_arg(arg_set_id, 'encoder_index') AS encoder_id,
  extract_arg(arg_set_id, 'pipeline_id') AS pipeline_id,
  extract_arg(arg_set_id, 'pipeline_state') AS pipeline_state,
  extract_arg(arg_set_id, 'timing_source') AS timing_source,
  extract_arg(arg_set_id, 'encoder_containment') AS parent_basis,
  arg_set_id
FROM gpu_slice;

CREATE PERFETTO VIEW gputrace_pipeline AS
SELECT
  pipeline_id,
  pipeline_state,
  name AS function_name,
  extract_arg(arg_set_id, 'allocated_registers') AS allocated_registers,
  extract_arg(arg_set_id, 'uniform_registers') AS uniform_registers,
  extract_arg(arg_set_id, 'spilled_bytes') AS spilled_bytes,
  extract_arg(arg_set_id, 'threadgroup_memory') AS threadgroup_memory,
  extract_arg(arg_set_id, 'instruction_count') AS instruction_count
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
