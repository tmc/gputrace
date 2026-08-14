# MLX GPU Trace Rendering in Perfetto

## Status

This document specifies the target representation of Apple Metal GPU traces
produced by MLX programs in Perfetto. It is a design, not a description of the
current output.

`gputrace timeline --format perfetto` now writes native Perfetto protobuf with
GPU compute slices, generic hierarchy tracks, clock-qualified counter-packet
support, and an evidence-manifest event. `--format chrome` retains Chrome Trace
JSON. Strict MLX sidecars, dependency-closed logical-byte budgets, and the
local viewer are implemented. APS per-encoder GPU cycles and derived cost are
encoder details, not counter series: their counter clock has no verified
mapping to cumulative busy time. The streamData Metal device name, plugin, and
GPU generation are retained with archive provenance. Rolling windows, driver
and MLX runtime environment capture,
native-label conflict handling and the MLX plugin remain proposed. A versioned
exporter-owned PerfettoSQL projection is available through `--sql-out`. The
viewer is specified separately in
[PERFETTO_VIEWER_SPEC.md](PERFETTO_VIEWER_SPEC.md).

Implementation ownership is repository-local:

- `internal/perfetto` owns the pinned protobuf schema and deterministic writer;
- `cmd/gputrace/cmd` owns canonical evidence projection and CLI integration;
- `internal/perfettosql` owns the stable SQL projection;
- `internal/perfettoviewer` owns local UI serving and pinned-UI admission; and
- `tools/perfetto-native-validate.sh` owns external parser and SQL validation.

Go tests exercise the writer, projection, SQL text, viewer admission, budgets,
and loss receipts in CI. Release qualification additionally requires running
`tools/perfetto-native-validate.sh` with the `trace_processor_shell` revision
named by `internal/perfetto.SchemaRevision`. The current CI workflow does not
install that external binary, so it does not satisfy the external parser and
standard-table acceptance criteria by itself. A retained validation receipt
must record the trace digest, SQL digest, trace-processor version, and query
results before a release claims those criteria.

Confidence markers used in this document are:

- `[V]`: verified by the current implementation, a checked trace, or an
  upstream interface;
- `[D]`: a design decision;
- `[?]`: a hypothesis that requires a decisive test.

Notebook and reverse-engineering suggestions are hypotheses until verified.
In particular, a counter filename is not evidence of a counter's identity.

## User questions

The view should let an MLX developer answer, in order:

1. Which model operation or generation step was running?
2. Which Metal command buffers, encoders, and dispatches implement it?
3. Which kernels dominate measured GPU execution?
4. Is a change faster, and is the comparison valid?
5. Did dispatch geometry, compilation statistics, or measured hardware
   conditions change?
6. Which relationships and measurements are unavailable?

The overview must remain useful without MLX labels, source maps, or decoded
hardware counters. Additional evidence enriches the same model; it does not
replace the measured execution record.

## Principles

### Preserve evidence classes

Every exported value belongs to exactly one class:

| Class | Examples | Rendering |
| --- | --- | --- |
| Measured execution | replay dispatch duration, encoder duration, command-buffer timestamp | timed slice in its measured clock domain |
| Recorded structure | command order, pipeline identity, debug group | hierarchy or instant when time is absent |
| Static compiler data | register count, instruction count, threadgroup memory | slice arguments and details, never a time series |
| Semantic annotation | request, token, layer, MLX operation | named semantic tracks joined by explicit identity |
| Derived diagnostic | cost share, small-threadgroup warning, comparison delta | visibly labeled derived field with formula and inputs |
| Unavailable evidence | missing parent id, counter binding, or clock join | explicit gap or unmatched record, never zero |

The exporter must retain the class and provenance in machine-readable form.
A visually plausible interval is not a substitute for a measured interval.

### Keep clock domains separate

`[V]` Current profiled traces expose cumulative GPU-busy offsets for detailed
encoder and dispatch work and APSTimelineData wall timestamps for command
buffers. No verified conversion joins those domains.

`[D]` The default view is the detailed GPU-busy view. The wall view is a
separate trace or a second independently controlled Perfetto panel. The
exporter must not align, scale, stretch, or interpolate one domain onto the
other without source-backed clock snapshots.

Counter samples belong only to a clock domain established by their source.
They must not be placed under busy-time dispatches merely because their spans
look similar.

### Fail closed

When identity, parentage, time, unit, or counter decoding is ambiguous, the
exporter reports the ambiguity and omits the asserted relationship. It must
not:

- pick the nearest encoder by time;
- divide an encoder evenly among its dispatches;
- emit an unavailable counter as a flat zero;
- infer an MLX operation from a similar kernel name;
- imply that wall-time gaps are GPU idle time;
- claim causal explanations from correlated measurements.

## Canonical evidence model

All output formats should be projections of one internal model. JSON, native
Perfetto, the text tree, and future SQL views must agree on identities,
relationships, timings, and unmatched records.

```text
Capture
├── EvidenceManifest
├── SemanticNode*             optional MLX hierarchy
├── CommandBuffer*            wall domain
│   └── EncoderRef*           only when a stable join exists
├── Encoder*                  busy domain
│   └── Dispatch*             strict source-backed containment
│       └── Pipeline
├── CounterSeries*            domain declared per series
└── Diagnostic*
```

Minimum stable identities:

```text
CaptureID       trace UUID plus content identity
SemanticID      sidecar or native-label identity
CommandBufferID capture-local command-buffer identity
EncoderID       capture-local encoder identity
DispatchID      capture-local dispatch identity or stable record ordinal
PipelineID      capture-local pipeline identity
SeriesID        counter catalog identity plus decoder provenance
```

An ordinal is acceptable only when the format guarantees ordering and the
manifest records the source record set. It must not be presented as a native
Metal identifier.

Each timed item records:

```text
clock_domain
start
duration
timing_source
timing_quality       measured or approximate
```

Each relationship records its basis, such as `record-parent-id`,
`strict-time-containment`, `native-debug-label`, or `sidecar-explicit-id`.
Temporal proximity alone is not a relationship basis.

## MLX semantic hierarchy

The preferred semantic hierarchy is:

```text
run → request → token or step → layer → operation
```

Nodes may be absent. The exporter preserves the hierarchy supplied by the
producer and does not synthesize missing levels. Common operation attributes
include:

- stable semantic id and parent id;
- display name and operation kind;
- model and layer name;
- token index, prompt/decode phase, or training step;
- input and output shape;
- dtype;
- MLX and application build identities;
- runtime library and Metal library digests.

### Evidence carriers

Carriers are preferred in this order:

1. native Metal debug groups or labels containing a versioned semantic id;
2. a versioned sidecar with an explicit trace identity and dispatch or encoder
   references;
3. unstructured labels rendered as labels but not parsed into hierarchy.

Native labels remain useful even when replay timing is absent. A sidecar may
add application semantics, but it must not add timings or Metal relationships
that are absent from the trace.

All carriers normalize into one canonical semantic record. Native labels do
not silently override a sidecar. When two carriers assert incompatible names,
parents, or target links, the exporter retains both assertions as conflicting
evidence, omits the disputed canonical relationship, and reports the conflict.

### Sidecar contract

The proposed command shape is:

```text
gputrace timeline TRACE --format perfetto --sidecar semantic.json
gputrace timeline TRACE --format perfetto --open --sidecar semantic.json
```

The sidecar schema should contain:

```json
{
  "schema": "gputrace.mlx-semantics/v1",
  "trace": {
    "uuid": "...",
    "content_digest": "sha256:..."
  },
  "producer": {
    "name": "...",
    "version": "..."
  },
  "nodes": [
    {"id": "op", "parent_id": "layer", "kind": "operation", "name": "matmul"}
  ],
  "links": [
    {
      "id": "op-dispatch",
      "semantic_id": "op",
      "target": {"kind": "dispatch", "index": 17}
    }
  ]
}
```

Each link names a semantic id and exactly one source-backed target identity.
Version 1 targets a command buffer, encoder, or dispatch. A future schema may
target a native label after the trace decoder exposes a stable occurrence
identity; the current label-to-string maps are not sufficient. The schema does
not permit time ranges as a substitute for target identity. Version 1 uses the
zero-based source-record index within each target kind; the index is
capture-local and is valid only with the exact UUID and content digest in the
same sidecar. It is not presented as a native Metal identifier.

Validation is strict:

- unknown schema versions are rejected;
- UUID and digest disagreement is rejected;
- duplicate semantic or link identities are rejected;
- dangling parent and target references are rejected;
- one source item linked to incompatible semantic nodes is reported as
  ambiguous unless the schema explicitly permits a many-to-one relationship;
- unused semantic nodes and unmatched trace targets are counted and exposed.

The JSON evidence model and native manifest expose used and unused node counts
plus matched and unmatched target counts by target kind.

An MLX runtime semantic receipt is not itself a sidecar. A receipt may describe
arrays, evaluations, runtime libraries, and matching native label text, but it
does not establish the trace UUID, trace content digest, or occurrence-level
GPU targets required by this contract. `--sidecar` rejects such a receipt with
a specific error instead of binding it by filename or label similarity. A
producer may transform a receipt into v1 only when it can add those identities
and explicit links.

`--sidecar` never silently degrades to filename matching. A future
`--sidecar=auto` may search beside the trace, but it must apply the same trace
identity checks and print the selected path.

## Default track layout

The default should be compact enough for an overview and rich on selection:

```text
MLX semantics
├── request 7 / decode
│   ├── token 42
│   │   ├── layer 0
│   │   │   └── attention
│   │   └── layer 1
│   └── token 43
GPU execution (cumulative busy)
├── compute encoder 18
│   ├── rmsbfloat16
│   └── steel_gemm_fused_...
├── compute encoder 19
│   └── sdpa_vector_...
└── unattributed dispatches
Encoder counter details
├── GPU cycles (capture-backed aggregate)
└── execution cost (derived share)
Validated GPU counters
└── unavailable until a series passes the clock and decoder gates
Diagnostics
└── unmatched and unavailable evidence
```

The semantic hierarchy and GPU hierarchy may be displayed adjacent to one
another when they lack a shared clock. A connecting relation is shown only
when an explicit identity join exists. Visual indentation alone must not imply
that a semantic span owns a GPU interval.

### Lane allocation

Encoders that overlap in the selected domain are assigned to the smallest
available lane by an interval-partitioning algorithm. Non-overlapping encoders
reuse lanes. A dispatch strictly contained by one encoder shares that
encoder's track, giving Perfetto a real nested slice. Dispatches with no unique
parent use `Unattributed compute dispatches`.

Record index must not determine lane number. Lane packing affects presentation
only and never changes identity or parentage.

### Slice names

Names should be recognizable and stable:

- semantic slice: the shortest meaningful MLX name, such as
  `layer 12 / attention`;
- encoder slice: explicit label when present, otherwise `compute encoder N`;
- dispatch slice: Metal function name, otherwise `unnamed dispatch N`;
- pipeline details: full pipeline and library identity in arguments rather
  than the visible name.

Long generated MLX kernel names may be elided visually by the UI, but the
trace stores the full name and a normalized grouping key. Normalization must
not merge different functions in the evidence model.

### Untimed work

Recorded dispatches without measured timing are zero-duration instant events.
In Chrome JSON compatibility output they use phase `i`. In native Perfetto
they use an instant track event or the closest native GPU representation that
does not imply duration. They remain ordered and selectable.

## Slice details

Selecting a dispatch should expose four clearly separated sections.

### Identity

- full function name;
- dispatch, encoder, pipeline, library, and capture ids;
- parent relationship and its basis;
- MLX semantic path and join basis, when present.

### Execution

- measured duration and clock domain;
- timing source;
- encoder-inclusive and dispatch-exclusive duration when both are known;
- dispatch grid and threads per threadgroup;
- execution-cost share and denominator, if derived.

### Static compilation facts

- allocated and uniform registers;
- spilled bytes;
- threadgroup memory;
- total, ALU, branch, integer, FP16, and FP32 instruction counts when present;
- compiler and Metal library identity.

Static facts are arguments, not counter samples. If a statistic is attached to
a pipeline and reused by many dispatches, the UI may display it on every
selection but the trace model should intern the pipeline metadata.

Register counts and instruction mix are compiler facts, not measured
occupancy, utilization, or limiter values. The UI may use them as inputs to an
explicitly named diagnostic, but it must not label the raw facts themselves as
occupancy or a bottleneck.

### Diagnostics and availability

- warnings with stable ids, formulas, and inputs;
- counter sample coverage over the selected interval;
- unmatched semantic nodes or GPU records;
- fields omitted because their decoder, unit, clock, or identity is unknown.

Warnings such as `small_threadgroup` are opt-in derived diagnostics, not
measured bottlenecks. A proposed threshold such as four SIMD groups must be
validated per GPU family before becoming a default diagnostic.

## Hardware counters

Counter rendering follows [research/COUNTER_LANES_DESIGN.md](research/COUNTER_LANES_DESIGN.md).
`[V]` `GPUCounterGraph.plist` provides display metadata and vendor-counter
recipes, but does not by itself bind every derived metric to the obfuscated raw
series. `Counters_f_*.raw` files are capture passes, not one file per displayed
counter.

A counter track is emitted only when all of these are established:

1. stable metric identity and display name;
2. source raw series and decoder version;
3. value formula and unit;
4. timestamp domain and conversion;
5. parser health and sample coverage.

Each series descriptor records:

```text
name
unit
group
source
clock_domain
decoder_version
catalog_path_and_digest
formula
coverage
confidence
```

Raw kick samples should be emitted at their measured density unless the user
requests downsampling. Downsampling records its method and source sample
count. Pipeline compilation statistics never appear in the counter group.

An export contains either the selected raw samples for its retained window or
one declared downsampled series, not duplicate full raw and downsampled tracks.
When full raw samples are kept as a separate artifact, the manifest records its
digest, counter catalog and decoder identity, sample count, clock domain, and
coverage. The artifact is evidence storage, not an invisible dependency of the
visible trace.

Unknown metrics are listed as unavailable. A counter with incomplete or
ambiguous decoding is not emitted, even if a candidate series resembles an
expected graph.

Sampling coverage is distinct from a zero value. A short dispatch or workload
may receive no hardware sample; that interval is `not sampled`, not zero. An
all-zero decoded series may also be a valid measurement, so an all-zero test
alone cannot establish decoder failure. The decoder must report whether the
series was absent, unreadable, sampled with zero values, or excluded by an
export policy. Only a source-backed sampled-zero series may be rendered as a
zero line.

Private-framework decoding is one possible backend, not part of the exported
schema. A series records whether it came from a checked pure parser, an Xcode
private-framework computation, or another decoder. The manifest pins the
Xcode build and framework identity for private results. Backends must satisfy
the same identity, formula, unit, clock, health, and coverage gates.

The current APS counter archive has capture-backed encoder ids and stable
execution ordinals across replay passes. It supports per-encoder aggregate GPU
cycles and a derived capture share. It does not establish a mapping from APS
counter timestamps to cumulative GPU-busy timestamps. The exporter therefore
places those aggregates in encoder details with
`counter_attribution_basis = Encoder Infos execution ordinal`, reports the
clock gap in the manifest, and emits no native counter samples for them.

Constrained native exports report considered, retained, and dropped item and
framed-byte counts per evidence class. They also report retained descriptor
skeleton count and the first and last dropped stable identities. These fields
are ordinary manifest debug annotations, so stock Perfetto and
`trace_processor_shell` can inspect them without a custom decoder.

## Native Perfetto representation

The native writer should emit binary Perfetto protobuf. Chrome JSON remains a
separate compatibility format.

### Packet mapping

| Evidence | Perfetto representation |
| --- | --- |
| capture and GPU metadata | trace metadata and `GpuInfo` where fields have Apple-backed meanings |
| measured compute dispatch | `GpuRenderStageEvent` categorized as compute |
| encoder hierarchy | GPU stage or track hierarchy backed by stable identity and time |
| semantic hierarchy | process-free `TrackDescriptor` hierarchy plus track events |
| pipeline/static facts | interned data and slice debug annotations |
| measured GPU counter | `GpuCounterDescriptor` and `GpuCounterEvent` |
| explicit dependency | flow or GPU wait id with stable endpoints |
| untimed recorded dispatch | instant track event |
| evidence manifest | trace metadata and a versioned annotation payload |

The exact protobuf fields must be pinned to a Perfetto revision and proven by
`trace_processor_shell`. Apple concepts should not be forced into Android or
Vulkan-specific fields whose semantics do not match. Generic track events are
preferred over semantically false native GPU fields.

A flow is emitted only when both endpoints have verified coordinates in the
same clock domain or a source-backed `ClockSnapshot` conversion. An identity
join across independently displayed busy and wall views remains an identity
relation; drawing a flow arrow does not solve the clock mismatch.

### Stable grouping

Track UUIDs are deterministic within one export and derived from namespaced
capture identities. They must not depend on map iteration, filesystem path, or
lane assignment. The same input and exporter version produces the same track
and event identities.

The native trace should populate standard GPU tables where semantically valid
and also provide an exporter-owned SQL module with a stable logical view:

```sql
gputrace_capture
gputrace_manifest_arg
gputrace_command_buffer
gputrace_semantic_node
gputrace_semantic_link
gputrace_semantic_arg
gputrace_dispatch
gputrace_dispatch_arg
gputrace_encoder
gputrace_encoder_arg
gputrace_function
gputrace_pipeline
gputrace_profiler_stream
gputrace_raw_profiler_sample
gputrace_raw_profiler_sample_arg
gputrace_aps_data_blob
gputrace_aps_data_key
gputrace_stream_data_archive_blob
gputrace_stream_data_archive_key
gputrace_counter_catalog
gputrace_counter_trace_id
gputrace_counter_encoder_sample
gputrace_counter_encoder_aggregate
gputrace_track_event_arg
gputrace_live_command_buffer
gputrace_host_signpost
gputrace_counter_series
gputrace_unattributed_counter
gputrace_unattributed_counter_arg
gputrace_evidence_gap
gputrace_unmatched
```

`gputrace_command_buffer` is a wall-domain projection. Profiled traces expose
measured APSTimelineData spans and their source ticks. Capture-only traces
expose command-buffer record identity and byte offset with unavailable timing.
It does not join either representation to cumulative GPU-busy dispatch time.

`gputrace_capture` provides typed columns for common identity, environment,
clock, coverage, timing-summary, and retention queries. Encoder and dispatch
spans, command-buffer active time and wall span, restore timing, display
duration, and Xcode Effective GPU Time are separate columns with their source
fields. Effective GPU Time remains `NULL` when Xcode did not report it.
For profiled traces, environment columns retain `metalDeviceName`,
`metalPluginName`, and `gpuGeneration` directly from the streamData archive.
GPU generation is nullable so a recorded zero remains distinct from absence.
The fields remain `NULL` with an availability reason when streamData is absent.
The archive projection also retains version, source trace name, Unix timestamp,
the three raw profiling-mode values, capture-range location and length,
data-source completeness flags, and blit-call count. Numeric and Boolean fields
are nullable so zero and false remain evidence. The private profiling enums and
capture-range units are not interpreted or promoted to capture/replay modes.
Command-buffer, encoder, GPU-command, pipeline, and function tables additionally
report their exact byte length, declared record size, computed record count,
trailing remainder bytes, availability, and integrity. A nonzero remainder is
reported as incomplete evidence rather than silently truncating the table.
Each complete table is retained losslessly as one untimed hexadecimal payload,
together with a whole-table SHA-256 digest. The recorded size and count delimit
rows; any trailing fragment remains in the payload and is counted separately.
`gputrace_stream_data_table` exposes these bytes without decoding unknown words
or asserting relationships between table ordinals.
Absence and malformed evidence are not conflated. A missing archive key is
reported as absent; a present reference that does not resolve to `NSData`
retains a decode error in canonical JSON and the manifest but emits no raw
table row. A valid empty `NSData` remains available with its empty-input digest.
The manifest also inventories top-level `APSData`, `APSTimelineData`,
`APSCounterData`, `shaderProfilerData`, `gpuTimelineData`, and batch-filtered
counter arrays. Counts describe archive entries only, not decoded samples. A
present empty array is retained as zero; an absent or malformed key is `NULL`
with an availability reason.

For each family, the manifest separately reports the number of recoverable
`NSData` payload blobs and the number of top-level entries that were not data
blobs. These are decoding-integrity counts, not record or sample counts. On the
matched wavefront fixtures, every nonempty family entry is a recoverable blob:
the debug trace reports `46/46` APS, `70/70` timeline, and `79/79` counter
entries/blobs; the release trace reports `48/48`, `77/77`, and `83/83`.
The capture-only fixture reports `NULL` for both entry and blob counts because
it has no streamData archive. A malformed family also remains unavailable;
the exporter does not turn it into an empty family.

When an `APSCounterData` archive decodes, the manifest also reports its exact
GPRWCNTR record coverage and integrity: payload blobs, decoded samples,
capture-attributed samples, machine-wide samples, remaining unattributed
samples, known encoder IDs, per-group encoder aggregates, pass-column groups,
trace-ID rows, and stride-mismatch blobs. The matched debug/release fixtures
both decode 176 GPRWCNTR blobs, 6,992 capture-attributed samples, 736 known
encoder IDs, 368 per-group aggregates, 25 pass-column groups, 23 trace-ID rows,
and no stride mismatch. Their total decoded sample counts differ (208,392 and
239,884), as do their machine-wide and unattributed populations. These totals
describe decoder coverage; they neither map the counter clock to the busy or
wall timeline nor strengthen the documented ordinal encoder attribution.

The `APSData` inventory goes one level deeper without decoding private trace
payloads. It reports how many decoded dictionaries contain exact archive keys
for counter configuration, shader-profiler data, post-processing frame
markers, APS trace-data files, and trace-ID tables, together with dictionary
and malformed-blob totals. The debug fixture has 46 dictionaries: 1 counter
configuration, 2 shader-profiler carriers, 2 frame markers, 40 trace-file
carriers, and 1 trace-ID metadata dictionary. The release fixture has the same
shape except for 4 shader-profiler carriers, for 48 dictionaries total. Both
have zero malformed blobs. These are independent key-presence counts; the
private `APSTraceDataFile` payloads remain uninterpreted.
Source and projected counts are also separate: source counts inventory the
pre-filtered capture, while projected counts inventory only events placed on
the selected clock. Clock filtering never changes source counts or copies
cross-domain events onto the selected axis.
Raw APSTimelineData clock-conversion inputs are retained as `absolute_time`,
`timebase_numer`, and `timebase_denom`. Their declared domain is `wall`, and
the manifest records the source and formula. They permit reproduction of the
wall-domain tick conversion only; `clock_mapping` remains `none`, and they do
not authorize alignment with cumulative GPU-busy offsets. If any input is
unavailable, the scalar columns remain `NULL` and
`clock_conversion_availability` records the refusal.
APSTimelineData's separate `Continuous Time` scalar is also retained when
present. Its typed column carries an explicit statement that its relationship
to exported clocks is unverified. It is raw evidence only: the exporter does
not subtract it, convert it, or use it to align events. Missing or zero values
remain `NULL` with an availability reason.
The raw APSTimelineData `PState` scalar follows the same rule. Its source and
availability are explicit, using a nullable integer so a recorded zero remains
distinguishable from absence. The exporter does not label it as
frequency, voltage, or a stable operating-point identifier without further
evidence.
APSTimelineData `Restore Timestamps` ranges are retained as measured wall-clock
intervals on a separate replay-restore track. They do not contribute to GPU
execution totals and are not joined to the cumulative GPU-busy axis. The
manifest reports source and projected interval counts, and
`gputrace_restore_interval` exposes raw ticks, converted wall offsets,
durations, and provenance.
`gputrace_manifest_arg` is the
lossless key/value projection of the same manifest. It keeps dynamic
per-evidence-class loss receipts and future manifest fields queryable without
silently dropping them from an older typed view.

`gputrace_dispatch` exposes Xcode's source-backed workload type and view
classifications as typed columns. `gputrace_dispatch_arg` and
`gputrace_encoder_arg` retain every native event argument as key/value rows,
including typed fields. This is the forward-compatible extension surface for
uncommon or newly added evidence; users do not need to join Perfetto's generic
argument table or wait for a typed schema revision.

`gputrace_raw_profiler_sample_arg` retains every pass-specific payload value
after the seven fixed GRC columns. Values are identified only by their recorded
zero-based ordinal. Counter names, units, derived meanings, and clock joins
remain unavailable until separately proven. `raw_value_uint64` is the exact
unsigned decimal representation retained by Perfetto; `raw_value_int64` is
the corresponding signed SQL integer projection.

`gputrace_counter_catalog` preserves the recorded APSCounterData pass-column
catalog as untimed evidence. Each row carries its group ordinal, column
ordinal, exact recorded name, and fixed-GRC or pass-specific classification.
Pass-specific names remain opaque identifiers: the catalog does not establish
units, decoded value series, encoder attribution, or a clock mapping. Catalog
availability and decoded-series availability are reported separately.

`gputrace_counter_trace_id` retains the exact recorded rows of the
APSCounterData TraceId-to-batch and TraceId-to-sample-index tables as untimed
evidence. The row ordinal is the documented positional relationship to encoder
execution order. The TraceId value itself is not equated with a GRC encoder or
kick ID, and the table establishes no timing or command-buffer relationship.
The exact unsigned decimal value is retained separately from Perfetto's signed
SQL integer projection.

`gputrace_counter_encoder_aggregate` retains every decoded APSCounterData row
whose GRC encoder ID occurs in `Encoder Infos`. Rows preserve encoder ID,
optional consistently observed kick ID, pass group, execution ordinal,
optional TraceId-derived batch and sample index, sample and encoder-end counts,
summed GRC GPU cycles, and the measured raw counter timestamp range. Full-range
unsigned identifiers, cycles, timestamps, and duration have exact decimal
columns alongside signed SQL projections. The source row count spans all pass
groups and is not a distinct Metal encoder count. These rows remain untimed:
the counter clock is unaligned, and neither encoder ID nor ordinal is promoted
to a Metal encoder foreign key. Under a byte budget they are optional evidence
with considered, retained, and dropped counts in the normal loss receipt.

`gputrace_counter_encoder_sample` retains the source records behind those
aggregates. It preserves source blob and record ordinals, encoder group and
execution ordinal, every fixed GRC field, and the hardware-counter vector as
an ordered JSON array. The source archive currently exposes 25 pass-column
groups but 16 encoder groups in the reference traces, with no verified
sample-blob foreign key between them. Therefore values remain anonymous: the
view does not manufacture counter names, units, Metal encoder identity, or a
busy/wall clock coordinate. The source count remains in `gputrace_capture`
when a constrained export retains only a representative subset.

`gputrace_aps_data_blob` identifies each raw APSData NSData entry by source
ordinal, byte count, and SHA-256 digest, then records whether its nested archive
decoded to a root dictionary. `gputrace_aps_data_key` retains that dictionary's
root keys in stable lexical order with structural value kinds such as data,
array, dictionary, string, number, and bool. The full digest covers every
private value; the structural projection copies recoverable values without
interpreting their meaning. Source blob, key, and recursive node counts remain
in `gputrace_capture` under sampling.

`gputrace_stream_data_archive_blob` and
`gputrace_stream_data_archive_key` generalize that evidence contract across
APSData, APSTimelineData, and APSCounterData. Family, ordinal, byte count,
digest, dictionary status, key identity, and structural value kind remain
queryable independently of higher-level decoder success. APSData-specific
views are filtered projections of these generic rows. The manifest reports
total and per-family source counts plus total embedded bytes before sampling.
For root values whose archive representation is directly recoverable, key rows
also retain the recorded scalar type and canonical JSON, NSData byte count and
digest, or container cardinality. Opaque representations carry an explicit
descriptor error. No field name is promoted into private semantic meaning.
The manifest reports pre-sampling counts for each descriptor class.

`gputrace_stream_data_archive_node` recursively projects the nested dictionary
and array graph. Each row carries an RFC 6901 path, parent path, depth, child
relation and ordinal, optional keyed-archive object index, structural value
descriptor, and one of `leaf`, `expanded`, `reference`, or `depth_limit`.
Dictionary children are lexical and array children retain source order. Object
references are expanded once, so cycles remain explicit without recursion; the
depth bound and per-family node budget fail visibly through status and manifest
counts.
Nodes are JSON debug annotations on their owning blob event, not separate
untimed tracks. This keeps the ordinary Perfetto UI compact while PerfettoSQL
can expand every retained node. Under an explicit byte budget the whole blob
packet is the atomic optional unit, and the normal loss receipt reports dropped
blob rows while the manifest still describes the full source graph.

`gputrace_shader_binary` gives every recorded `/Binaries` entry its
capture-local unique ID, byte count, and SHA-256 digest. The binary bytes remain
in the source archive; this view content-identifies them without treating the
payload as a metallib, executable, or instruction stream.
`gputrace_shader_binary_content_audit` groups those entries only by exact
SHA-256 identity and reports occurrence, family, blob, and logical decoded-byte
counts. It distinguishes unique entries, repetition within one family, and
repetition across families, while independently checking that equal digests
carry equal decoded sizes. `logical_repeated_entry_bytes` is not recoverable
file-size savings: keyed archives may share backing objects. Content equality
does not establish pipeline compatibility, execution-cost equality, use by a
dispatch, or any timing relationship. Constrained exports may retain no binary
rows at all; the manifest and loss receipt remain authoritative for the source
count and audit completeness.
`gputrace_program_address_mapping` pivots the nine fields recorded on each
`/Program Address Mappings` entry and joins `binaryUniqueId` to the matching
binary dictionary entry in the same family and blob. Unsigned source numbers
remain available as exact JSON beside convenient SQL integer projections. The
view reports the join as `matched` or `unmatched`; it does not turn encoder IDs,
draw indices, addresses, or binary types into dispatch, function, source-line,
or timing attribution. Pre-sampling binary, mapping, matched, and unmatched
counts remain in `gputrace_capture` when an output budget drops blob packets.

`gputrace_encoder_program_mapping` projects one additional relationship:
recorded `encIndex` equality to the streamData `Encoder Infos` execution
ordinal. It exposes the matched encoder event and timing provenance while
retaining the separately recorded `encID` as an opaque mapping-namespace value.
This is an encoder-level set of recorded program mappings, not proof that every
binary or address was used by every dispatch in that encoder.
`gputrace_program_encoder_identity_audit` reports distinct-set equality counts
between mapping `encID`, APSCounterData encoder IDs, APSCounterData kick IDs,
mapping `encIndex`, and encoder execution ordinals. It inventories equality; it
does not promote equal values in the opaque ID namespaces into foreign keys.

Recorded profiling configuration is available without scanning the complete
recursive archive projection. `gputrace_stream_data_configuration` and
`gputrace_aps_option` retain scalar leaves, exact archive paths, source types,
and canonical JSON. In particular, recorded time offsets remain configuration
values; they are not a verified clock mapping. `gputrace_counter_info`,
`gputrace_limiter_counter_group`, and `gputrace_limiter_sample_counter` retain
the source counter flags, ordered limiter groups, and ordered sampling list.
They assign no units, values, pass columns, or limiter formulas.

`gputrace_limiter_catalog_audit` reports exact name equality between the
sampling list, group map, Counter Info, and the separately decoded pass-list
catalog. A match is only a spelling match. An unmatched pass-list name remains
explicit and is not treated as missing or zero counter data. The manifest
keeps pre-sampling counts for all five projections; under an output budget the
ordinary loss receipt remains authoritative for which optional blob packets
were retained.

`gputrace_profiler_configuration` provides a compact projection of scalar
leaves recorded beneath `apsProfilingConfig`, `Timebase`, `Perf Info`,
`Frame Consistent Perf Info`, and `Kick State Trigger Options`. Every row keeps
its source family, blob ordinal, exact archive path, source type, and canonical
JSON. `gputrace_profiler_configuration_audit` reports the recorded row and
distinct-path shape by blob and section. Recorded periods, masks, timebase
components, and feature flags are not assigned units or runtime effects by
these views. Equal values across archive families do not prove that clocks or
counter streams are aligned. The manifest retains the full pre-sampling row
count when an output budget drops the optional blob packets.

`gputrace_stream_data_root_scalar` exhaustively projects every decoded scalar
key at the root of every nested archive dictionary. It is not a whitelist:
future private-schema keys become queryable without an exporter update. Each
row retains the source family and blob, RFC 6901 path, lexical key ordinal,
recorded name and scalar type, canonical JSON, typed SQL value, and blob
digest. Timing-looking names such as `Absolute Time`, `Continuous Time`, and
`ReplayerGPUTime` remain untimed source facts in the `none` clock domain. The
view does not assign units, align clocks, create flows, or infer runtime
effects. Its pre-sampling source count remains in the manifest.

`gputrace_root_scalar_equality_audit` first counts distinct canonical JSON
values within each family, then compares the single recorded values across
families. It distinguishes `single_family`, `multiple_values_within_family`,
`equal_across_families`, and `different_across_families`; it does not hide
duplicates behind an aggregate representative. These are neutral equality
states, not PASS/FAIL or diagnoses of corruption, drift, or runtime behavior.
For example, independently recorded absolute-time values may legitimately
differ while `PState` is equal. The view reports `complete_export` only when
the loss receipt says the export is complete; otherwise its audit scope is
`retained_rows_only`.

`gputrace_profiler_carrier` inventories fields recorded together in one nested
archive blob: `APSTraceDataFile`, `Source`, `SourceIndex`, `RingBufferIndex`,
and `Serial`, plus the blob byte count and digest. Missing fields remain
`NULL`. These are opaque capture-local values; same-blob containment does not
join a carrier to an encoder, dispatch, or clock. The view also reports the
count and total bytes of embedded NSData payloads in that blob.

`gputrace_profiler_carrier_sequence` orders carriers with a recorded serial by
family and recorded source, then exposes the previous serial and ring index,
their arithmetic deltas, and neutral transition classes. The aggregate
`gputrace_profiler_carrier_sequence_audit` reports observed cardinality,
ranges, duplicates, and non-unit transitions. `observed_unit_steps` describes
only the retained recorded order. A non-unit transition is not assigned a
duration or diagnosed as lost samples, ring overflow, GPU idle time, or a
stall. Under output sampling the audit is `retained_rows_only`, because the
exporter's own dropped carriers necessarily create apparent gaps.

`gputrace_embedded_profiler_artifact` content-identifies each such payload by
exact archive path, structural kind, byte count, SHA-256 digest, and owning
carrier digest. Payload bytes remain in the source capture. Shader binaries are
excluded because `gputrace_shader_binary` owns that evidence class. Other
payloads are not claimed to be decoded merely because a structural name such
as `ShaderProfilerData` or `Derived Counter Sample Data` was recorded. Source
carrier, payload, and payload-byte counts remain in the manifest under output
sampling.

`gputrace_embedded_profiler_artifact_content_audit` groups payload entries only
by exact SHA-256 identity. It reports family, blob, and structural-kind
multiplicity, logical decoded-entry bytes, and digest-size consistency. Equal
bytes may appear under different structural kinds; that is content equality,
not semantic interchangeability. Logical repeated-entry bytes are not physical
storage savings, and content identity establishes no decoder success, counter
mapping, execution use, or timing relationship. A constrained export may
retain no artifact rows; the manifest and loss receipt remain authoritative.

When an APSCounterData TraceId table covers an encoder execution ordinal,
`gputrace_encoder` also exposes its recorded batch ID and sample index. This is
the same positional relationship used for capture-backed execution-cost
annotation. It is not equality between TraceId and GRC encoder or kick IDs and
does not map the counter clock to the busy clock. Uncovered ordinals remain
`NULL`.

Pipeline identity is explicitly capture-local. Profiled dispatches use the
pipeline ID and address from `streamData` `pipelineStateInfoData`; capture-only
dispatches retain the address from their capture record and leave the absent
pipeline ID `NULL`. The typed dispatch and pipeline views expose the numeric
address, identity source, and scope. `gputrace_pipeline` also reconciles total,
measured, and recorded-only dispatch counts and sums duration only for measured
GPU execution. It does not treat an address or pipeline ID as a cross-trace
identity, and it does not synthesize private Xcode function or library object
IDs that normal export never observed.

The opt-in wall export retains GPRWCNTR records in
`gputrace_raw_profiler_sample`, including their original mach-absolute tick,
source record ordinal, stream index, raw header fields, coordinate basis, and
decode status. Samples come only from exact APSTimelineData
`ShaderProfilerData` fields and retain source, ring-buffer, stream-size, and
machine-wide coverage metadata. They are not joined to the APSCounterData pass
catalog because the ShaderProfilerData carrier exposes no pass-group identity.
`gputrace_track_event_arg` is the lossless key/value extension
surface for low-volume generic track events, including command buffers and
profiler streams. Its `event_id` joins within one loaded trace only and is not
a source or cross-trace identity.
`gputrace_profiler_stream` contains source-backed aggregate stream spans. Both
views explicitly distinguish raw profiler input from decoded counters and GPU
encoder intervals. Busy-domain dispatches do not receive direct sample counts
from wall-domain timestamp overlap; only the separately labeled estimated
scaled-window attribution may appear there.

Pipeline-scoped counter rows without a capture-backed encoder identity are
untimed evidence, not counter samples. `gputrace_unattributed_counter` exposes
their label, source, scope, and refusal reason;
`gputrace_unattributed_counter_arg` retains arbitrary metric values.
`gputrace_evidence_gap` records source-backed evidence families that cannot be
projected without inventing an identity or clock relationship. These rows use
the `none` clock domain and never contribute GPU duration.

Function-name provenance is explicit: measured dispatches use the
`gpuCommandInfoData` function name, while capture-only dispatches retain the
capture parser's attribution basis. `gputrace_function` keeps summed
per-launch SIMD work separate from a source-reported function aggregate. The
latter uses one repeated source fact per function, never a sum across dispatch
rows, and remains `NULL` when the trace carries no such aggregate.

`gputrace_live_command_buffer` retains exact nanosecond intervals reported by
`MTLCommandBuffer.GPUStartTime/GPUEndTime` during the original execution. Its
`kernel_start_ns` and `kernel_duration_ns` columns separately retain
`MTLCommandBuffer.kernelStartTime/kernelEndTime`; they are evidence fields on
the same live clock, not dispatch intervals or a nested containment claim. Its
trace-identified sidecar receipt, clock-sample count, projected count, and
unmatched count are typed capture fields. `gputrace_host_signpost` retains only
events admitted by the strict host-correlation receipt and exposes its join
basis, both artifact digests, clock domains, bridge digest, and maximum error.
Neither view weakens the identity or clock gates enforced before export.

`gputrace_raw_profiler_sample` retains the variable GPRWCNTR record stride and
the seven source-defined fixed columns: timestamp, GPU cycles, sample type,
encoder id, kick trace id, kick slot index, and source id. These are raw record
facts. Columns after the fixed prefix remain uninterpreted hardware counters;
their values are not named, joined, or promoted to counter tracks.

Semantic node rows include their semantic parent id. Semantic link rows retain
the sidecar link id and exact target kind/index, so consumers can reconstruct
the validated join without interpreting names. `gputrace_semantic_arg` keeps
arbitrary sidecar attributes as key/value rows. Attributes deliberately appear
on both the untimed declaration and any timed projection for useful selection
details; consumers distinguish them with `event_kind` rather than summing both.

Each semantic node is emitted as an explicitly untimed declaration on its
hierarchical track. Target links are separate events and inherit timing only
from their explicitly identified GPU target in the selected clock domain.
Thus unused nodes remain queryable without assigning them fabricated GPU time.
Links whose targets belong to the other measured clock domain are not placed
on the selected axis. The manifest reports them by target kind as unprojected,
separately from invalid, unmatched, or budget-dropped evidence.

`gputrace timeline --format perfetto --sql-out gputrace.sql` writes these
versioned views. They operate on the native packet tables and can be loaded by
`trace_processor_shell` after the trace.

### Packet sequences and interning

One writer owns each Perfetto packet sequence. Interned strings and descriptors
are scoped to that sequence and are referenced only after definition. The
current writer uses one sequence, emits its incremental-state-cleared flag and
interned values before events, and never resets or reuses intern ids. A future
writer that resets must emit the required incremental-state reset before
reusing intern ids. Sequence ids and intern ids use checked allocation; wrap
cannot silently reuse live state.

Offline export preflights the exact framed byte count and writes one packet at
a time, so it buffers no complete trace and needs no latency flush policy.
Different events have very different encoded sizes, so an event-count
threshold would not be a memory bound. Packet boundaries and write timing must
not change event identity or ordering.

## Resource budgets and loss

Offline conversion of a finite `.gputrace` is lossless by default. A caller may
set an explicit output budget; the current writer reports deterministic
identity-hash retention through the evidence-manifest event. A future
continuous recorder may use a rolling retention window. Neither mode may
silently discard evidence.

### Output budgets

The exporter reserves enough budget for descriptors, required dependency
skeletons, the evidence manifest, and the final loss receipt before admitting
optional events. The configured boundary is the number of logical
uncompressed protobuf bytes written to the trace stream. File offsets and
compressed storage sizes are reported separately when applicable.

Every retained event has dependency closure. If it refers to an encoder,
pipeline, semantic ancestor, descriptor, or interned value, the exporter
retains at least a minimal skeleton for that identity. A skeleton contains the
stable id, kind, display name when available, provenance, and an explicit
`details_dropped` marker. A drop policy may remove optional details or samples;
it may not leave dangling references.

Required retention order is:

1. schema, sequence state, descriptors, and manifest;
2. dependency skeletons for retained evidence;
3. errors, explicit triggers, boundaries, and loss records;
4. rare event classes and unmatched or conflicting evidence;
5. representative ordinary events;
6. optional annotations and dense samples.

Representative sampling uses a stable hash of capture identity and event
identity. Fixed ordinal stride sampling is not the default because it can
alias periodic token or layer behavior. Boundaries, errors, triggers, rare
classes, and required ancestors are retained regardless of the sample.

### Rolling windows

Protobuf packets already flushed to an output stream cannot be retracted. A
rolling window is therefore implemented before final emission, using an
in-memory ring, a segmented spool, or a separate continuous-profile recorder
that freezes a window and then writes the trace. The ordinary streaming writer
does not claim rolling-window semantics.

### Loss receipt

Stock Perfetto must remain useful when loss occurs. The exporter emits the
human-readable summary as supported trace metadata or debug annotations and
exposes detailed loss through exporter-owned SQL/plugin data. A custom protobuf
extension is permitted only when its schema and decoder revision are pinned;
it cannot be the sole loss report.

The receipt records:

```text
policy and policy version
logical byte boundary
events and bytes considered, retained, and dropped by evidence class
dependency skeletons retained
first and last dropped identities when available
sampling algorithm and seed derivation
counter samples retained and raw-artifact reference
whether output is complete, sampled, truncated, or windowed
```

The receipt itself is never subject to the optional-event budget.

## Collection and projection boundary

The Perfetto writer is a pure deterministic projection of provenance-bound
records. It does not load private frameworks, run replay, collect signposts, or
read live Metal state.

Collection adapters own Metal capture, headless replay and `streamData`, public
timestamps, dated Xcode Cost extraction, signposts, and retained raw counter
files. Other packages may expose analogous evidence contracts. All adapters
normalize evidence before it reaches the writer, so Chrome JSON, native
Perfetto, text, and JSON reports apply the same identity and loss rules.

## MLX Perfetto UI plugin

A custom plugin is a presentation layer over the canonical evidence model. It
must remain optional: the native trace is still useful in an unmodified
Perfetto UI.

### Overview page

The overview shows:

- trace identity, device, MLX build, capture mode, and timing provenance;
- total measured GPU execution by kernel and MLX semantic path;
- named, unnamed, matched, unmatched, and untimed dispatch counts;
- top kernels by duration and call count;
- counter families available and unavailable;
- explicit warnings when wall and busy views cannot be joined.

### Kernel table

The sortable kernel table includes:

```text
kernel
semantic path
calls
total duration
mean
p50
p90
maximum
execution share
registers
spilled bytes
threadgroup geometry
counter coverage
```

Selecting a row filters and highlights its timeline occurrences. Aggregates
state their timing source and omit percentiles when event timing is absent.

### Selection panel

The panel renders the four detail sections above and links to:

- owning semantic node;
- parent encoder;
- all occurrences of the same pipeline;
- raw source record or gputrace JSON identity;
- matching item in a loaded comparison trace.

### Comparison

Comparison accepts two independently validated captures. Before showing
deltas it checks device, OS/Xcode family, workload identity, capture mode,
timing source, and sidecar schema. Incompatibilities remain visible and require
an explicit expert override.

Comparison does not byte-compare complete environment manifests. It computes a
versioned projection:

- exact gates: workload identity, device and driver identity, runtime build,
  capture mode, and timing source;
- compatibility gates: the capabilities queried for the analysis and their
  results;
- informational fields: observation time and physical memory, unless memory
  capacity is a declared experimental variable.

Each environment field records retrieval source, parser version, and
availability. Metal `supportsFamily` results are observations over a declared
query catalog, not a complete device-family inventory; the catalog revision
and digest accompany the results.

An override across exact gates labels the result `cross-environment, not
causally attributable`. Such a comparison can answer a deliberate
cross-device question, but it is not presented as a controlled regression.

The current `diff` command applies this projection before producing deltas.
Missing exact evidence is incompatible even when both sides omit the same
field. `--allow-cross-environment` is the explicit override and includes the
label and mismatch list in full and quick JSON. Workload, driver, and MLX
runtime identity remain unavailable in ordinary captures until collection
adapters record them.

Matching proceeds from strongest to weakest stable identity:

1. semantic id and operation path;
2. pipeline/library digest and function identity;
3. exact function name plus verified dispatch signature.

Unmatched and ambiguous work is reported before matched deltas. The UI never
silently drops unmatched dispatches from totals. Duration, static compiler,
and counter differences are separate columns; the plugin does not label a
counter delta as the cause of a duration delta.

## Search and PerfettoSQL

The standard UI should support searching the full kernel name, semantic path,
pipeline id, and diagnostic id. The plugin should ship saved queries for:

- top kernels by measured duration;
- kernels grouped by MLX operation and layer;
- dispatches without encoder attribution;
- semantic nodes without GPU attribution;
- pipelines with spills;
- longest dispatches with dispatch geometry;
- counter coverage gaps;
- unmatched work between two captures.

Every saved query is tested against a pinned `trace_processor_shell` version.
SQL results must reconcile with the JSON evidence report for counts and summed
durations.

## Evidence manifest

Every export carries a manifest containing at least:

```text
schema and exporter version
input UUID and content digest when already verified for a strict sidecar;
otherwise explicit digest unavailability (ordinary export does not hash the
whole multi-gigabyte capture bundle merely to render it)
input path for diagnostics only
device and OS/Xcode identity when available
capture and replay mode
clock domain
timing source and quality
counts of semantic nodes, command buffers, encoders, and dispatches
counts of matched, unmatched, ambiguous, and untimed items
counter catalog and decoder provenance
emitted Perfetto packet families
unavailable evidence families and reasons
sidecar identity and digest, when used
Perfetto schema revision
environment schema, retrieval provenance, and queried-family catalog
resource policy and loss receipt
retained raw-artifact identities and digests
ordered streamData string count and source-array semantics
pipeline compiler diagnostic count, source, and static-evidence semantics
```

The canonical model and native trace retain the complete ordered streamData
strings NSArray, including empty values. One untimed evidence row carries each
source index and value. The index is not classified or joined to a pipeline,
function, source file, or clock unless an independently decoded source table
establishes that relationship.

Static pipeline compiler evidence is emitted once per pipeline on an untimed
evidence track. The typed view retains exact optimization remarks, cached
status, and nullable signed compile-stage nanosecond fields from streamData or
capture store sections. A recorded `-1`, zero, or false is distinct from an
absent field. Compiler YAML may identify optimization passes and source
locations; it is not measured source-line execution cost. Under an explicit
native-export byte budget these verbose events are optional and any omission
is reported by the normal loss receipt.

The raw Remarks YAML remains authoritative. A second typed view,
`gputrace_pipeline_compiler_remark`, projects one searchable row per YAML
document: document index, remark kind, compiler pass, name, function, parse
status, and recorded source coordinate. A positive line is `complete`; an
explicit `Line: 0` compiler sentinel is retained as
`unresolved_source_location`; absence is `no_source_location`; and a malformed
document is isolated without aborting later documents. The manifest reports
source document count, recorded location count, resolved count, unresolved
count, malformed count, and Passed/Missed/Analysis counts. Source counts
describe the input even when a constrained export retains fewer SQL rows; the
loss receipt reports the projected-row difference exactly.

The ordered one-line scalar entries beneath a remark's `Args` key are retained
in canonical JSON and expanded by
`gputrace_pipeline_compiler_remark_arg`. Each row preserves its source order,
name, complete raw line, raw scalar spelling, decoded string value, and parse
status. Duplicate names and empty strings are evidence and remain distinct;
quoted numbers are not promoted to numeric metrics. Unsupported multiline or
non-scalar entries are retained as malformed raw rows rather than silently
discarded. Under an explicit byte budget arguments are retained or omitted
with their parent remark. The manifest's source argument count therefore may
exceed the typed argument view count in an incomplete export.

Untimed detail instants are deterministically sharded across bounded evidence
tracks. This avoids Trace Processor's same-timestamp argument-depth limit
without assigning synthetic timestamps. Shards are a storage and presentation
mechanism only; their track identity has no runtime or compiler meaning.

Pipeline statistics preserve exact source-key presence separately from their
legacy scalar values. A recorded zero or false is emitted; an absent key is
omitted and becomes SQL `NULL`. The one-row-per-pipeline compiler view also
retains the sorted key list as JSON so opaque dictionary members can be
observed without decoding or classifying their payloads.

Native Perfetto and timeline JSON exports content-identify every regular file
directly beneath the resolved `.gpuprofiler_raw` directory. Each untimed
artifact row contains only its basename, recognized family, optional file
index, byte size, and SHA-256; host directory paths and symlink targets are not
retained. The manifest records the artifact count, aggregate bytes, digest
algorithm, scope, and a deterministic SHA-256 over the sorted artifact
inventory. This hashes the profiler evidence directory, not the entire
multi-gigabyte capture bundle. A read failure makes the artifact identity
unavailable rather than publishing a partial inventory.

`Timeline_f_*.raw` artifacts additionally retain the named fields from their
fixed 128-byte headers: raw magic, counter count, data-section byte offset,
entry count, and raw profiler-sampling timestamp. These values are file-format
evidence, not decoded counter samples. Magic is retained as raw identity rather
than validated against one observed constant. The timestamp is not converted
or joined to command-buffer wall time or cumulative GPU-busy time.

When capture mode, replay mode, a counter catalog or decoder, or a separate raw
artifact identity cannot be proved from the input, the manifest records that
field as unavailable with a reason. It does not omit the field or infer it
from a filename.

The input path must not participate in stable identity and may be omitted for
privacy. Labels and semantic attributes may contain model or application data;
the local viewer must not upload them.

## CLI shape

The existing `timeline` subcommand owns conversion, semantic attachment,
resource policy, the embedded evidence manifest and loss receipt, SQL views,
and viewer launch. These are not separate top-level commands.

Current commands and options:

```text
gputrace timeline TRACE --format perfetto --clock busy -o trace.pftrace
gputrace timeline TRACE --format chrome --clock busy -o trace.json
gputrace timeline TRACE --format perfetto --open [--sidecar semantics.json]

--clock busy|wall
--sidecar FILE
--max-output-bytes N
--sql-out FILE
--kernel NAME
--kernel-occurrence N
--time-start SECONDS
--time-end SECONDS
```

The evidence manifest and loss receipt are embedded in native output. They do
not require a separate command or sidecar file. Counter selection, counter
sampling, diagnostic selection, and a separate manifest file remain possible
future `timeline` flags; they are not current CLI promises.

`--max-output-bytes` is an explicit lossy-export request and uses the logical
protobuf-byte definition and dependency-closed policy above. Zero or omission
means lossless finite conversion. Rolling-window controls belong to a future
continuous recorder, not this command.

`--format perfetto` changes meaning only with a compatibility notice: it will
be native binary output, while `--format chrome` retains the current JSON.
`--clock both` is not a single native trace until a verified clock mapping is
available. The local viewer may implement `both` as two panels.

The default output is clean MLX-compatible output without requiring MLX. It
uses native labels when present, accepts a sidecar only when explicitly named,
and otherwise renders the Metal hierarchy faithfully.

Lossless busy-clock exports include a presentation-only dispatch track beneath
each encoder. These slices copy the measured native dispatch coordinates and
are marked as duplicates; `gpu_slice` remains the accounting source for totals
and SQL. Explicitly constrained exports omit this redundant presentation layer
before dropping source evidence.

## Delivery plan

### Slice 1: canonical model and manifest

- Define stable evidence types independent of Chrome JSON.
- Project current busy and wall exports from the model.
- Emit the manifest, environment projection, unmatched counts, and loss state.
- Define dependency skeletons and deterministic retention policy.
- Add parity tests between text/JSON and Chrome output.

### Slice 2: native GPU execution

- Pin the minimum Perfetto protobuf definitions and revision.
- Emit metadata, measured compute dispatches, encoder hierarchy where proven,
  and untimed instants.
- Implement sequence-owned interning, reset semantics, and byte-bounded flush.
- Validate standard GPU tables with `trace_processor_shell`.
- Keep Chrome JSON behavior unchanged.

### Slice 3: MLX semantic input

- Define and version the sidecar schema.
- Validate trace identity and all references strictly.
- Add `--sidecar` to timeline and the local viewer.
- Emit semantic tracks and explicit joins with JSON/native parity tests.

### Slice 4: counters

- Promote only counter decoders whose identity, formula, unit, clock, and
  parser health are proven.
- Emit native GPU counter descriptors and samples.
- Surface unavailable metrics, sample coverage, and retained raw artifacts.

### Slice 5: local viewer and plugin

- Serve the pinned Perfetto UI as specified by
  [PERFETTO_VIEWER_SPEC.md](PERFETTO_VIEWER_SPEC.md).
- Add the MLX overview, kernel table, selection panel, and saved SQL queries.
- Verify remote UI fallback without making it the reproducible default.

### Slice 6: comparison

- Add compatibility gates and deterministic matching.
- Compare versioned environment projections rather than raw manifests.
- Show matched, unmatched, and ambiguous totals.
- Cross-link corresponding selections without merging clock domains.

## Validation workloads

The fixture corpus must exercise behavior, not just file size:

| Workload | Required coverage |
| --- | --- |
| long dense autoregressive decode | high event count, repeated token/layer periods, budget sampling |
| sparse mixture-of-experts decode | rare operations and semantic attribution |
| long-context prefill | dense counter samples and large dispatches |
| complete training steps | long duration and broad operation vocabulary |
| speculative decode | nested request, branch, token, and operation semantics |
| unlabeled Metal workload | truthful non-MLX fallback and no sidecar assumptions |

Each class includes lossless and constrained-budget exports when applicable.
Periodic decode fixtures must demonstrate that stable hash sampling does not
collapse every retained event onto the same token or layer phase.

## Acceptance criteria

The rendering design is implemented when all of the following hold:

- current profiled fixtures produce exactly one event per source dispatch;
- every timed event identifies its clock domain, source, and quality;
- raw captures render untimed dispatches as instants, not heuristic bars;
- strict encoder containment and unattributed counts match the canonical JSON;
- the same sidecar and trace produce deterministic semantic identities;
- conflicting native-label and sidecar assertions remain visible and do not
  produce a disputed canonical relationship;
- wrong-trace, duplicate, dangling, unmatched, and ambiguous sidecar fixtures
  have explicit test outcomes;
- native output parses with zero relevant parser errors;
- every retained reference resolves after constrained-budget export;
- constrained output reserves and emits a stock-Perfetto-visible loss summary
  and a machine-readable receipt within the declared logical byte boundary;
- repeated packet writes preserve all interned references and deterministic
  event ordering; any future incremental-state reset has the same gate;
- standard Perfetto GPU queries and exporter-owned SQL views return expected
  fixture counts;
- static pipeline facts do not appear as measured counter series;
- unknown or unhealthy counters are omitted and reported as unavailable;
- visible counter tracks do not duplicate a retained raw artifact, whose
  digest, decoder, clock, sample count, and coverage are recorded;
- busy and wall evidence never share an axis without a verified clock join;
- an unmodified Perfetto UI provides a useful GPU view;
- the optional plugin adds MLX navigation without changing trace truth;
- all aggregate tables reconcile with the canonical evidence report;
- comparison ignores informational observation timestamps, applies the
  versioned environment projection, and labels exact-gate overrides as
  cross-environment and not causally attributable;
- repeated exports are byte-stable apart from explicitly documented metadata.

## Open questions

1. Which native Perfetto GPU packet fields are semantically portable to Apple
   compute work, and which require generic track events?
2. Can the trace supply a stable command-buffer-to-encoder identity, or must
   those hierarchies remain separate?
3. Can native MLX-C labels carry semantic ids through capture and replay
   without truncation or reordering?
4. What content identity can be computed cheaply enough for strict sidecar
   validation on large trace bundles?
5. Which APS counter clock and conversion are verified across several Apple
   GPU generations?
6. Should the MLX Perfetto plugin live in this repository, a pinned Perfetto
   fork, or a separately versioned package?
7. What subset of semantic attributes is safe to show by default when model
   prompts, tensor values, or application labels may be sensitive?

## References

- [Local Perfetto viewer specification](PERFETTO_VIEWER_SPEC.md)
- [Current timeline design](research/PERFETTO_TIMELINE_DESIGN.md)
- [Ideal GPU execution timeline](research/IDEAL_TIMELINE_VIEW.md)
- [Shader source attribution](SHADER_SOURCE_ATTRIBUTION_SPEC.md)
- [Counter lanes design](research/COUNTER_LANES_DESIGN.md)
- [Stream data format](STREAMDATA_FORMAT.md)
- [Perfetto GPU data sources](https://perfetto.dev/docs/data-sources/gpu)
- [Perfetto track events](https://perfetto.dev/docs/instrumentation/track-events)
- [Perfetto UI plugins](https://perfetto.dev/docs/contributing/ui-plugins)
- [PerfettoSQL syntax](https://perfetto.dev/docs/analysis/perfetto-sql-syntax)
