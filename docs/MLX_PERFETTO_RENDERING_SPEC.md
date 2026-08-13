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
mapping to cumulative busy time. Rolling windows, richer environment capture,
native-label conflict handling and the MLX plugin remain proposed. A versioned
exporter-owned PerfettoSQL projection is available through `--sql-out`. The
viewer is specified separately in
[PERFETTO_VIEWER_SPEC.md](PERFETTO_VIEWER_SPEC.md).

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
gputrace_semantic_node
gputrace_dispatch
gputrace_pipeline
gputrace_counter_series
gputrace_unmatched
```

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
otherwise explicit digest unavailability (ordinary export does not hash a
multi-gigabyte bundle merely to render it)
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
```

The input path must not participate in stable identity and may be omitted for
privacy. Labels and semantic attributes may contain model or application data;
the local viewer must not upload them.

## CLI shape

Proposed commands and options:

```text
gputrace timeline TRACE --format perfetto --clock busy -o trace.pftrace
gputrace timeline TRACE --format chrome --clock busy -o trace.json
gputrace timeline TRACE --format perfetto --open [--sidecar semantics.json]

--clock busy|wall
--sidecar FILE
--counters default|all|none
--counter-sampling raw|downsampled
--diagnostics default|none
--manifest FILE
--max-output-bytes N
--sql-out FILE
--kernel NAME
--kernel-occurrence N
--time-start SECONDS
--time-end SECONDS
```

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
- [Counter lanes design](research/COUNTER_LANES_DESIGN.md)
- [Stream data format](STREAMDATA_FORMAT.md)
- [Perfetto GPU data sources](https://perfetto.dev/docs/data-sources/gpu)
- [Perfetto track events](https://perfetto.dev/docs/instrumentation/track-events)
- [Perfetto UI plugins](https://perfetto.dev/docs/contributing/ui-plugins)
- [PerfettoSQL syntax](https://perfetto.dev/docs/analysis/perfetto-sql-syntax)
