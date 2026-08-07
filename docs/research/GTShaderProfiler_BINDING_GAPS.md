# GTShaderProfiler Binding Gaps

This note records the private Xcode binding surface needed to close the
remaining Xcode parity gaps in gputrace output.

The current module imports `github.com/tmc/apple/private/xcode/gtshaderprofiler`
from `github.com/tmc/apple v0.5.5`. This checkout's `go.work` selects a newer
local apple worktree containing the verified bulk accessors and
`GTMioCounterData` slice helpers. Until that worktree is released and this
module is bumped, these private-binding paths are intentionally local-only:
`GOWORK=off` against v0.5.5 does not provide the same surface.

## Useful Bound Bindings

Run `gputrace xcode-bindings --json` to inspect these classes and selectors on
the local Xcode installation. The command only checks runtime availability; it
does not instantiate profiler objects or parse capture data.

- `GTShaderProfilerStreamData` exposes archived stream data entry points and
  properties for `encoderInfoData`, `gpuCommandInfoData`,
  `pipelineStateInfoData`, and `pipelinePerformanceStatistics`.
- `XRGPUAPSDataProcessor` exposes APS/RDE raw and derived counter processing,
  including shader loading, counter loading, timestamp conversion, and raw or
  derived counter buffer accessors.
- `GTMioCounterData` exposes counter metadata: name, index, sample count,
  sample interval, scope, timestamps, and values.
- `GTMioShaderBinaryData` exposes shader binary cost, source mapping, ISA, trace,
  and register-pressure accessors, including
  `LiveRegisterForInstructionAtIndex`.

## Current Probe Results

On the qwen-native trace, `gputrace xcode-parity --json` loads stream data
through `GTShaderProfilerStreamData.dataFromArchivedDataURL:` and reports:

- 436 GPU command records, 8 pipeline states, and 8 functions.
- APS timeline and counter dictionaries contain `ReplayerGPUTime`, but both
  values are `0`.
- APS timeline and counter dictionaries contain `Binaries` with 734 entries.
- The APS counter dictionary contains `Derived Counter Sample Data` with 16
  groups and an empty `Derived Counters Info Data` dictionary.
- Nested sampling shows each sampled derived-counter group is an array of 5
  arrays. The first sampled group contains NSData payloads sized 40448,
  443520, 230208, 192192, 193600, 80640, 41856, 34944, 35200, and 80896
  bytes across the sampled children.

Neither the dispatch occupancy gap nor the dispatch ALU utilization gap is
closed. [V] This section previously claimed both were, on the grounds that the
encoder counter fallback carried a value into every kernel event and pprof
sample "with counter-source provenance". The value it carried was zero, and the
zero came from an `EncoderCounterMetrics` that nothing had written to -- not
from `Counters_f_12.raw`, which gputrace does not decode.

[V] Xcode reports ALU Utilization of 1.59, 1.87, 1.58, 2.12, 1.47, 1.39, 2.10,
1.50, 2.03, 2.70, 0.08, 0.02, 1.91, 2.47, 1.91, 2.00, 1.50, 1.78, 1.58, 1.97,
1.69, 3.35 and 0.48 percent for the 23 encoders of
`qwen25-05b-staticmask-warm-tokens2-4-rep1`, in both its CSV export and its live
Counters inspector. gputrace emitted 0.00 for all 23.

A field carrying a value is not parity, and a value nobody compared against
Xcode is not evidence. Treat a gap as closed only when a nonzero value has been
checked against Xcode's for the same encoder.

Standing disposition, checked 2026-08-06. Every row is either closed or carries
a reason it cannot be:

| Gap | State | Why |
| --- | --- | --- |
| `high_register` | **closed** `[V]` | stream-parent enumeration; 801 binaries, 44,900 instructions, max 122 |
| `occupancy_pct` | cannot be closed `[V]` | not archived at all; requires counter sampling at capture time |
| `alu_utilization_pct` | cannot be closed now `[V]` | record layout unestablished, *and* no capture survives to score it on |
| effective GPU time | cannot be closed `[V]` | `ReplayerGPUTime` archived as zero; fallback is labelled |

`occupancy_pct` and effective GPU time are closed as far as they can be: the
data is absent from the archive, so no decoding effort reaches them. The
remaining two rows are detailed below.

The exporter gaps are:

- `alu_utilization_pct`: `Derived Counter Sample Data` is present in stream
  data but is not decoded. `Counters_f_12.raw` is named by
  `GPUCounterGraph.plist` as the ALU Utilization file but its record layout is
  not established, so no offset can be read from it. It is additionally blocked
  by the crash recorded below, which binds independently.
- `occupancy_pct`: not archived anywhere in the trace bundle. Xcode's
  Occupancy is a GPU performance counter sampled at capture time; the string
  does not appear anywhere in `.gpuprofiler_raw`. Xcode's separate *max
  theoretical occupancy* is computed by the Metal compiler and is likewise not
  archived - only its inputs (register counts, threadgroup memory) are. A
  static residency model cannot fill the gap either: there is no published
  max-resident-threads-per-core denominator for any Apple family, and on
  Apple9 (M3/M4) registers and threadgroup memory are allocated dynamically
  from L1, so a per-family table is the wrong model rather than merely an
  unmeasured one. Closing this requires counter sampling at capture time.
- `high_register`: [V] the safe adapter enumerates the processed stream
  parent's `shaderBinaries` collection rather than constructing a binary from
  an `NSData` blob. On `qwen25-05b-static_tokens_2_to_3-wperfdata`, it read
  801 binaries, 44,900 instruction records, and a maximum live-register count
  of 122. This is a binary-level compiler metric, not source-level cost.
- effective GPU time: `ReplayerGPUTime` is archived as zero for this trace, so
  gputrace keeps reporting the command-buffer active-time fallback.

## Generated Signature Risks

The generated surface is present, but some signatures need a narrow adapter
before gputrace should call them in normal export paths.

- `GTMioCounterData.Values` and `Timestamps` are pointer-valued selectors, not
  Objective-C arrays: their runtime encodings are `^d` and `^Q`. The generated
  helpers deep-copy `SampleCount` doubles and uint64s while the owner is live.
  On `qwen25-05b-static_tokens_2_to_3-wperfdata`, `malloc_size` reported
  1,589,248 bytes for each pointer, above the 1,573,392 bytes required by
  196,674 elements. That settles the read bound for this capture, not metric
  units, timestamp clock, or scope semantics.
- `XRGPUAPSDataProcessor.GetBufferAtRDESourceIndexRdeBufferIndexBufferLength`
  and `GetBufferAtUSCIndexBufferLength` model output buffers as Go strings.
  A gputrace adapter should pass explicit byte storage and lengths.
- `XRGPUAPSDataProcessor` raw and derived counter accessors return timestamp and
  count metadata separately from caller-owned buffers. Wrappers should allocate
  buffers, validate returned counts, and name the counter source.
- `GTMioShaderBinaryData` must not be constructed from a `Binaries` NSData byte
  pointer with a nil parent object. That isolated path produced garbage and
  crashed. The implemented adapter instead enumerates the processed stream
  parent's owned `shaderBinaries` collection; no offline binary decoder is
  needed for live-register counts.

## No capture on this machine can exercise the derived-counter route

`[V]` Checked 2026-08-06. This is upstream of every remaining exporter gap
below, so it is recorded before them rather than inside one of them.

`alu_utilization_pct` needs `Derived Counter Sample Data` decoded, and the route
to it runs through `GTShaderProfilerStreamDataProcessor.processStreamData`.
That call **crashes with a bus error** on the only capture available to try it
on, before any gputrace counter code runs:

    $ go run ./cmd/extract_xcode_metrics \
        ~/gputrace-fixtures/parity-asymmetric-perfdata.gputrace
    Gen: 16   Type: G16C    Rev: B1    Num Cores: 40 ...
    signal: bus error

`[V]` The crash is inside the framework call, not in our reader: tracing places
it between `processStreamData` entry and return. Reproduced on repeat runs.

`[V]` The bundle is not the problem. It is a complete profiler capture — 40
shards each of `Counters_f`, `Profiling_f`, and `Timeline_f`, plus a 2.0 MB
`streamData` — and gputrace's own reader parses it without complaint,
reporting 4 command buffers, 3 encoders, 11 dispatches and 3 pipelines.

`[V]` **Refuted:** that the outer bundle name failing to match the inner
`parity-asymmetric.gputrace.gpuprofiler_raw` prefix defeats `_setupDataPath`.
Presenting the bundle under the matching name crashes identically. The cause is
still unidentified; this is recorded so the same hypothesis is not re-tried as
though it were untested.

`[V]` It is the *only* candidate. Of the surviving fixtures, only
`parity-asymmetric-perfdata.gputrace` contains a `.gpuprofiler_raw` directory
at all; `verify-dbg.gputrace` and
`qwen25-05b-python-metaldebug_tokens_2_to_3.gputrace` do not, and the three
captures the parity oracles were built from no longer exist (see
`XCODE_PARITY.md`, "Capture inventory").

`[D]` So `alu_utilization_pct` is blocked twice over, and the second blocker
binds even if the first is solved. Establishing the `Counters_f_12.raw` record
layout would still leave the value unscoreable, because scoring it requires a
capture with a matching Xcode oracle and none exists. Per the parity integrity
rules a value that is emitted but unvalidated is worse than no value, so the
correct output remains no value.

## Implementation Direction

Keep the risky Objective-C calls behind an internal adapter that can be probed
independently from the timeline and pprof exporters. Export paths should keep
reporting metric provenance so missing Xcode-equivalent values are visible in
Perfetto, the web UI, and pprof comments rather than silently appearing as zero.

On this machine, `gputrace xcode-bindings --json` reports all four target
classes and all 42 checked selectors present. The remaining gaps are adapter
work rather than missing Objective-C bindings.
