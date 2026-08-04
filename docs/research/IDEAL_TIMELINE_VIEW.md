# Ideal GPU Execution Timeline

## Decision

The ideal timeline answers the question that motivates a performance capture:

> I changed something. Did it help, and where?

It is a correlated, hierarchical view of GPU work and measured conditions. It
lets a user move from a capture-level regression to a command buffer, encoder,
kick, dispatch, pipeline, and eventually source-level diagnosis without
silently changing clock domains or inventing attribution.

This is an aspirational design. The current busy-first export is its safe
foundation, not its endpoint. A capability moves into the default only after
its stated evidence gate is met.

`[V]` marks a verified fact, `[D]` a conclusion derived from capture-backed
tests, and `[?]` a design hypothesis requiring validation.

## Target experience

```text
Capture summary: Xcode GPU Time | command-buffer active time | wall span
                    [each label names its domain and source]

Command buffers  | CB 0 ========================= |
Encoders         |   compute encoder 0 =====       |
Kicks            |      kick 81 === kick 82 ==     |
Dispatches/draws |       kernel A == kernel B ==   |
Selected counters| occupancy | ALU | bandwidth ... |
```

The upper hierarchy answers ordering and attribution. The lower lanes answer
which measured condition changed while that work ran. Selecting an interval
cross-highlights its known parents and children, pipeline, counter samples,
and source context. The details panel reports inclusive and exclusive duration
with its timing domain, execution-cost share with its denominator, selected
counter min/mean/max with coverage, and pipeline facts explicitly labelled
static compiler data.

A user should be able to begin with "did this change help?" and navigate to
the encoder, dispatch, pipeline, and measurement that explain the result. A
missing identity or clock join is displayed as `unknown`, never filled by a
heuristic.

This target shape is not a license to infer hierarchy from temporal proximity.
Command buffers, kicks, and counters join it only when their capture-local
identity, clock, ownership, and ABI are known. Clique and instruction-trace
expansion is a diagnostic drill-down from a kick or dispatch, not a default
row; it does not imply source-level cost before the source mapping exists.

## Current safe foundation

```text
Compute GPU execution (cumulative busy)
├── Compute encoders and dispatches
│   ├── encoder 0
│   │   └── dispatches strictly contained by encoder 0
│   ├── encoder 1
│   │   └── dispatches strictly contained by encoder 1
│   └── ...
├── Unattributed compute dispatches
│   └── dispatches not strictly contained by an encoder
├── Xcode parity and provenance
└── Measured encoder counters
    ├── Execution cost
    └── GPU cycles
```

The compute row is one execution hierarchy, not one track per pipeline. An
encoder is the visible parent slice. A strictly contained dispatch shares its
encoder's Perfetto track so the viewer renders it as a child. Pipeline state,
shader name, register data, and instruction data remain on the dispatch
selection panel rather than fragmenting the overview into sparse lanes.

A dispatch that fails strict containment is shown on the explicitly named
`Unattributed compute dispatches` track. It is never reparented merely to make
the picture tidier.

At the overview zoom, a user should see encoder cost and sequence, repeated or
long-running shaders, meaningful busy-time gaps, counter changes at encoder
boundaries, and the amount of work whose parent is not known. At dispatch
zoom, selection reports shader and pipeline identity, encoder index,
containment state, duration, static compiler facts, and timing provenance.

`[D]` The shipped Execution Cost lane has capture-matched Xcode oracles, but
is not exact: the 11-encoder capture's pinned maximum residual is 2.941
percentage points with 1.034 percentage points RMS, concentrated mostly in
encoder 9. The selection view must expose that residual rather than presenting
the figure as exact Xcode parity.

## Separate wall-clock scheduling view

Wall time answers:

> When did the capture submit and complete command-buffer work?

The wall view contains APSTimelineData command buffers, zero-duration command
buffer instants, and source/timing provenance. Raw profiler records are
available only with `--include-raw-samples` and are labelled as raw
diagnostics, not as decoded counters or encoder intervals.

Busy encoders, dispatches, and busy counters do not appear in the wall file;
wall command buffers do not appear in the busy file. Perfetto has one global
time axis. Combining the two domains without a measured conversion would put
one set of spans at invented positions.

`--clock both --format html` is therefore the current total-information view:
two independently zoomable panels with an explicit no-mapping statement.

Xcode Overview `GPU Time`, when requested with `--xcode-gpu-time`, is a
display-total/provenance value. It must not rescale, truncate, or position the
busy slices.

## Comparison and decision support

The comparison view is how the timeline turns measurement into a decision. It
serves compatible capture families such as eager, static, static-maskopt,
static-hwtl, Python, and Metal-debug variants. It shows baseline and candidate
in aligned lanes only after confirming compatible device, workload, capture
mode, and timing source.

Before presenting any delta, it reports unmatched encoders, pipelines, and
dispatches. Duration deltas, validated-counter deltas, and static compiler
differences are separate observations. A counter change is a diagnostic fact,
not a causal explanation by itself.

The ideal navigation path is: “did static-maskopt help?” → the changed encoder
→ the changed dispatch or pipeline → the validated condition that changed. A
missing join suppresses that step rather than being replaced with a heuristic.

## Source-level diagnosis

The most valuable endpoint is a source-level answer: not merely “encoder 9 is
expensive,” but “this kernel region is expensive.”

`[D]` Existing ordinary bundles provide sparse compiler source locations, but
not the stable source-to-instruction mapping and program-counter-attributed
cost required for that projection. See `SOURCE_LEVEL_COST.md`. The eventual
path requires a validated instruction edge and a capture or replay cost join;
a debug-info build may supply the missing instruction mapping. Until then,
GTLLVMHelper clique and instruction traces are shader diagnostics, not
line-level cost measurements.

## Objectives and evidence gates

| Objective | Best outcome it unlocks | Required evidence | Current status |
| --- | --- | --- | --- |
| Maintain nested busy execution | A compact, useful default Perfetto view | Strictly contained dispatches share the owning encoder track; trace_processor accepts the file | Shipped |
| Unprofiled dispatches in Perfetto | Order and identity rendering for raw API traces | Perfetto x-axis is strictly time (`[D]`); unprofiled dispatches must emit zero-duration instants (`Phase: "i"`, `[D]`); heuristic bars permitted only in text/HTML (`[D]`). Live exporter in `cmd/gputrace/cmd/timeline.go` populates unprofiled fallback encoders and kernels as Phase `"i"` zero-duration instants (`[V]`). Measured timing paths remain untouched (`[V]`). Verified via `TestUnprofiledRawTraceEmitsPhaseIInstantEvents` on `verify-dbg.gputrace` fixture (`[V]`). | Implemented (`[V]`) |
| Decode additional encoder counters | Counter lanes for occupancy, instruction mix, bandwidth, cache, and limits | Decoded source and unit; meaningful values including valid zeroes; capture-matched Xcode oracle or equivalent value validation; compatible timestamp domain | Blocked on workload duration for hardware counters; GRC_GPU_CYCLES encoder cost shares established (`[V]`) |
| Correlate wall command buffers to busy work | One truthful command-buffer to encoder hierarchy | Per-command-buffer busy-origin anchor and content-bearing CB identity (`[V]`). Profiled export automation unblocked and verified (`[V]`). `streamData` omits CB/encoder string labels (`[V]`); `APSTimelineData` uses Xcode replayer clock, not live GPU uptime (`[V]`). | HOLD — missing shared CB identity & live clock anchor (`[V]`). Direct label & live clock join refuted on `parity-asymmetric` (`[V]`). |
| Attribute dispatches to encoders | Correct dispatch nesting under its owning encoder | A partition cross-checked against an independently stored per-command index | Established |
| Add timestamped kicks | Submission and profiler-detail drill-down | GTMioKickTrace field semantics, a measured start/end clock, and an ownership join. Structural track partition (6,304 kick indexes) proven (`[V]`), but timing clock unproven (`[V]`). | HOLD — field semantics and timing unproven (`[V]`) |
| Add External Process and host annotations | Xcode-like external spans plus userland context | A capture-side concurrent signpost collection (`.logarchive` or `log stream`) and a stable join from os_signpost data to a command buffer or kick. Absent from supplied `.gputrace` bundles (`[V]`). `xctrace Logging` has signposts but no GPU work (`[V]`); `Metal System Trace` drops signposts (`[V]`). `xcrun xctrace list templates` exposes no standard combined GPU+Logging CLI template (`[V]`). | HOLD — requires custom Instruments document/template incorporating both Metal System Trace and Logging instruments for combined collection (`[D]`). |
| Add memory-side timeline counters | Memory and cache lanes | Plaintext metric, unit, scope, and compatible clock or ownership; no per-encoder interpolation | Current series is scope=2/index=0 and unaligned |
| Add flows | Causal submission/dependency arrows | Stable producer and consumer identifiers from the archive, not temporal proximity | Not established |
| Compare captures | Actionable baseline/candidate timeline | Capture-family match, explicit unmatched-work report, and separate duration/counter/static-fact comparisons | Future |
| Add source-level cost | Source-line or instruction-level diagnosis | Debug-info build, stable source/instruction mapping, and capture or replay cost join. Compiler source locations decoded (`gather_front.h:19`, `[V]`), but binary/instruction-to-dispatch cost edge is missing (`[V]`). Positional source slice attribution prohibited (`[D]`). | HOLD — missing binary/instruction-to-dispatch cost edge (`[V]`). Next: debug-enabled capture profiled from same raw bundle (`[D]`). |

External Process and host annotations share one capture-pipeline gate. `[D]`
The measured kick-accessor scan provides no host-tag join. `[V]`
`DYCaptureEngine.launch_dictionary` is empty across the 14 locally inspected
captures. Xcode obtains the relevant signpost data outside the `.gputrace`
bundle. A concurrent signpost capture is therefore a higher-value experiment
than another archive-only parser pass.

`[V]` `GTMioShaderProfilerResult` partitions every GPUCommand record exactly
once into contiguous `GTMioShaderProfilerEncoder` ranges, and each command's
independently stored `encoderInfoIndex` equals its owning encoder's `index`.
The range partition and the per-command index are separate fields, so their
agreement is a cross-check rather than a restatement. Verified on both
captures: 11 encoders over 466 commands on static-tokens, 23 over 958 on
staticmask.

`[V]` **The `commandBufferIndex`-to-`APSTimelineData` correspondence is
refuted as a general rule.** On static-tokens the indexes are exactly `0..10`
against 11 `APSTimelineData` rows, which looks like a join. On
staticmask-perfdata2 the same code yields 23 distinct `commandBufferIndex`
values against 24 `APSTimelineData` command-buffer rows. The counts do not
agree, so the two are not the same enumeration.

The static-tokens agreement was never evidence: a dense zero-based index over
11 items matches a count of 11 whether or not the orders correspond. That is
why one capture could not settle it and a second one refuted it.

`[V]` The obvious repair — join on a field both sides carry instead of on a
count — was tried and there is no such field. `GTMioShaderProfilerGPUCommand`
exposes `commandBufferIndex`, `encoderObjectId`, `functionIndex`,
`pipelineStateObjectId`, and `timingInfo`. `APSTimelineData` exposes only
indexed start and end ticks. The two share no address, label, or object ID.

`[V]` `timingInfo.time` cannot substitute for the missing identifier either: it
sums to zero for every processed command-buffer group on both captures — all 11
on static-tokens and all 23 on staticmask — while every `APSTimelineData` wall
span is nonzero. The processed model carries the identity structure; the
archive carries the time. Nothing observed so far carries both.

The wall-to-busy join is therefore blocked on missing data rather than on
undone analysis. Further archive-only parsing passes are not the way through
it.

That is the *identity* half of a busy-to-wall correlation, and only that half.
The processed model exposes no timestamps, and `commandBufferIndex` is not yet
joined to the archive's wall command-buffer records. It authorizes a
dispatch-to-encoder ownership claim; it does not authorize a command-buffer
timeline lane, a `ClockSnapshot`, or any placement of busy work on the wall
axis. Reproduce with `TestProcessStreamData`.

`[V]` The static-tokens capture's 6,304 timeline kick indexes partition into
three generated top-kick-track lanes with no duplicates or out-of-range values.
That is a stable grouping and ordering join to the timeline kick array. The
raw `GTMioKickTrace` fields remain opaque and have no established clock, so the
join may be exported only as unpositioned metadata; it must not become a timed
Perfetto lane.

## Counter presentation

Counter lanes are grouped by question rather than archive order:

| Group | Default candidate | Drill-down after validation |
| --- | --- | --- |
| Work and cost | Execution Cost, GPU Cycles | active cores, SIMD groups in flight |
| Compute | ALU utilization | instruction throughput, FP16/FP32, integer, control flow |
| Memory | device bandwidth | read/write split, L1 accesses, miss rate, LLC/MMU |
| Sampling | valid samples | pass, group, dropped or failed chunks |

Each rendered point carries `scope`, `source`, `coverage`, clock domain, unit,
and validity. A counter that is machine-wide or encoder-aggregate stays at that
scope; it is not smoothed into a dispatch series. An undecoded, unaligned, or
unitless counter is omitted rather than rendered as zero. A decoded, genuinely
idle counter remains visible at zero.

`[V]` `internal/counter/encodercost.go` derives encoder cost from
`GRC_GPU_CYCLES` and documents its residual. The older
`internal/counter/execution_cost.go` estimates pipeline cost from sample-count
share. That known violation of the no-sample-count allocation rule must be
replaced or explicitly labelled an estimate before it appears as a measured
cost lane.

## Data contract

Every exported observation has these conceptual fields:

```text
Observation {
  interval: [start, end) or sample timestamp
  clock_domain: busy | wall | profiler | unknown
  scope: command_buffer | encoder | kick | dispatch | clique | machine_wide
  subject_id: stable capture-local ID
  metric: name or raw identifier
  value, unit, aggregation
  source: archive section or compiler record
  validity: measured | validated-derived | static | raw-diagnostic | unsupported
  coverage: samples, pass, dropped_chunks
}
```

The absence of a value or join is `unknown`, never a guessed Xcode name or
synthetic zero. Static compiler data is explicitly labelled static, and a
private API is usable only after ABI evidence and a repeatable capture check.

## Integrity rules

- Never compare, add, or align effective GPU time, busy offsets, command-buffer
  active time, profiler ticks, and wall span without naming their domains.
- Do not distribute encoder cost by record count, dispatch count, or samples.
  If an allocation model is needed, label it an estimate and preserve any
  unallocated remainder.
- Do not interpolate across sampling-pass boundaries or invalid counter chunks.
- Do not show implementation artifacts such as `GPRWCNTR`, lane numbers, or
  opaque archive keys in the default view.
- Every trace records framework build, GPU identity, parser version, source,
  validity, and excluded/unaligned data families.

## Acceptance criteria

The busy export is ready when a user can identify the dominant encoders and
shaders, see which dispatches are unattributed, and inspect validated counter
changes from one Perfetto screen. It must satisfy all of these checks:

- trace_processor reports no JSON parser failures;
- strictly contained dispatches share their encoder track;
- busy and wall files contain no events from the opposite clock domain;
- every visible counter is source-backed and has a known unit; and
- no displayed span relies on an inferred clock conversion or invented parent.

The full correlated view is ready only when every added hierarchy level meets
its objective's evidence gate above. Unsupported data remains visibly
unsupported until then.
