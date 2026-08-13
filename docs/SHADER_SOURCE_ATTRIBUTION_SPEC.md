# Shader source attribution

This document specifies when `gputrace` may project GPU cost onto Metal source
in Perfetto and pprof. It applies to MLX and to other Metal producers.

The central rule is simple: source text, debug lines, and measured cost are
different evidence. A source location does not make a cost line-attributed.

## Goals

The supported navigation path is:

```text
MLX operation
  -> Metal command buffer and encoder
  -> compute dispatch
  -> pipeline and shader function
  -> shader binary and instruction range
  -> source file and line
```

Every edge must have a source-backed identity. Temporal proximity, list
position, fuzzy function-name matching, and proportional distribution of a
kernel total are not attribution edges.

The same canonical attribution records feed both outputs:

- Perfetto renders measured dispatches and, when proven, instruction or source
  frames beneath them.
- pprof renders the same frames and values as call stacks and samples.

The exporters must not disagree about attribution level, timing source, or
loss.

## Evidence levels

Each dispatch has exactly one source-attribution level.

| Level | Required evidence | Permitted output |
| --- | --- | --- |
| `dispatch` | dispatch identity and measured or explicitly approximate dispatch cost | shader or pipeline frame only |
| `declaration` | exact pipeline-to-function identity plus source artifact identity and exact declaration location | whole dispatch cost at the declaration, labeled `kernel_total_at_declaration` |
| `instruction` | dispatch-to-binary identity, instruction-address or range identity, and measured instruction cost | instruction frames with binary offsets |
| `source_line` | all instruction evidence plus a verified debug-line mapping for the identical binary | measured source-line frames |

An exporter may move only downward in this table when every additional edge is
present and verified. Missing or ambiguous edges retain the coarser level and
produce an explicit unmatched receipt.

`declaration` is navigation, not line-level measurement. The full dispatch
cost may appear at the declaration coordinate so `pprof -list` can open the
file, but the profile and Perfetto arguments must state that the granularity is
the whole kernel.

## Debug MLX producer

An MLX qualification build must retain Metal line tables and source text. For
the CMake MLX build this currently means `MLX_METAL_DEBUG=ON`, which supplies
`-gline-tables-only -frecord-sources`. A producer using another build system
must record equivalent compiler and linker flags.

The build receipt contains:

- MLX source commit and dirty state;
- Metal source-tree digest;
- compiler path, version, SDK build, target, and complete Metal flags;
- metallib digest and size;
- a section inventory proving the expected source and line-table sections;
- the capture executable digest and code-signing identity.

The source archive and line table must be read from the captured or
content-identical metallib. A source checkout found through
`GPUTRACE_SHADER_SEARCH_PATHS` is auxiliary navigation evidence unless its
digest is bound by the build receipt.

Debug information is necessary but not sufficient for `source_line` evidence.
It supplies `instruction -> line`; it does not supply measured instruction
cost or prove which captured dispatch used that binary.

## Canonical attribution record

The internal model should carry one record per retained dispatch-cost unit:

```text
Attribution {
    capture_id
    dispatch_id
    pipeline_id
    function_id
    binary_digest
    instruction_begin
    instruction_end
    source_artifact_digest
    source_path
    source_line
    source_column
    cost_value
    cost_unit
    cost_source
    attribution_level
    quality
}
```

Optional fields are absent, not zero-filled. `quality` is one of `measured`,
`derived`, or `approximate`; `source_line` requires `measured` cost and verified
identity joins. Static compiler instruction counts remain static facts and are
never emitted as elapsed time or sampled execution cost.

## Required joins

### Dispatch to pipeline

Use the captured pipeline-state identifier or another archive-backed identity.
Do not join by kernel name, ordinal, encoder position, or timing proximity.

### Pipeline to binary and function

The device-resource graph must bind the pipeline to one captured shader binary
and function. Duplicate names in different binaries remain distinct. A binary
digest mismatch refuses the source join.

### Cost to instruction

`source_line` requires a measured cost source carrying a program counter,
instruction offset, or bounded instruction range in the same shader binary.
Pipeline totals, encoder samples, `GRC_SOURCE_ID`, static instruction counts,
and Xcode Cost percentages without instruction identity do not satisfy this
edge.

### Instruction to source

Decode the debug line table from the content-identical binary. Overlapping,
out-of-range, missing, or ambiguous ranges remain instruction-level evidence.
Macro expansion and `#line` remapping retain both physical archive location and
logical source location when available.

## Perfetto projection

Dispatch time remains a native GPU slice only when its timing source qualifies
under the rendering specification. Attribution is attached as frames or debug
annotations beneath that dispatch; it does not create a new time axis.

- `dispatch`: pipeline and function annotations.
- `declaration`: source artifact and declaration coordinate, with
  `attribution_granularity=kernel_total_at_declaration`.
- `instruction`: binary digest and instruction range.
- `source_line`: logical source file, line, column, and measured cost.

Source-line children reconcile exactly to their owning measured cost after
declared integer rounding. Unattributed residue is retained as an explicit
child. The exporter never scales source weights to force reconciliation.

## pprof projection

The pprof sample types and units match the canonical cost source. Function and
location stacks are constructed from the same attribution records used by
Perfetto.

`--source-lines` does not authorize estimation. When only declaration evidence
exists, it emits one whole-kernel sample at the declaration and adds profile
comments and labels disclosing that granularity. True per-line samples are
emitted only for `source_line` records.

Profiles include comments naming the trace identity, metallib digest, debug
receipt digest, timing source, attribution level, unmatched counts, and loss
receipt. `go tool pprof -top` totals must equal the canonical report for the
selected sample type.

## Qualification campaign

Use one deterministic MLX workload and one deliberately asymmetric Metal
kernel whose expensive source region is known in advance.

1. Build release and debug metallibs from the same MLX source tree.
2. Capture the same workload with each metallib and retain executable,
   environment, source, and metallib identities.
3. Profile replay from each original capture without changing its identity.
4. Confirm release capture stops at `dispatch` or `declaration` as its evidence
   permits.
5. Confirm the debug capture exposes source and line tables.
6. Establish or refuse the measured instruction-cost edge independently.
7. Export Perfetto and pprof from the same canonical record set.
8. Validate native Perfetto tables with the pinned `trace_processor_shell` and
   pprof totals with `go tool pprof`.

Controls include:

- same function name in two distinct metallibs;
- debug source from the wrong MLX commit;
- changed metallib with unchanged source path;
- stripped line table;
- line table present but no instruction-cost samples;
- unknown and out-of-range instruction addresses;
- partial sample loss and unattributed residue;
- release/debug workload-output mismatch;
- capture/profile UUID mismatch;
- fuzzy-name match that would otherwise select the wrong kernel.

## Acceptance criteria

Source-line attribution is ready only when:

- a retained debug MLX capture contains content-identified source and line
  tables;
- the captured dispatch, pipeline, function, and binary joins are exact;
- a measured instruction-addressed cost stream is independently decoded and
  bound to that binary;
- line samples plus explicit residue reconcile with their owning measured cost;
- Perfetto and pprof contain identical attribution levels, identities, units,
  and totals;
- wrong-binary, wrong-source, ambiguous, stripped, missing-cost, and loss
  controls fail closed;
- repeated exports are byte-stable apart from documented observation metadata;
- neither exporter presents declaration placement or estimated weights as
  measured line cost.

Until those gates pass, shader-source output is useful navigation and
pipeline-level diagnosis, not source-line performance attribution.

## Related documents

- [MLX GPU Trace Rendering in Perfetto](MLX_PERFETTO_RENDERING_SPEC.md)
- [Source-level cost and the Heat Map](research/SOURCE_LEVEL_COST.md)
- [Metric provenance](METRIC_PROVENANCE.md)
- [Ideal GPU execution timeline](research/IDEAL_TIMELINE_VIEW.md)
