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

  `[V]` The exporter that *would* publish it is still in the tree and is
  unreachable. `generateCounterTracksFromPerfData` in
  `cmd/gputrace/cmd/timeline.go` builds ten tracks including `ALU Utilization`,
  and no production path calls it: `generateCounterTracks` reaches only
  `generateCounterTracksFromCounterArchive`, and the only remaining callers are
  three tests. This is the function that emitted `0.00` for all 23 encoders
  while Xcode reported 1.59, 1.87, 2.70 and so on, so its disconnection is the
  fix for that incident rather than an oversight.

  `[D]` Reconnecting it is not the way to close this gap. It reads
  `EncoderCounterMetrics`, which `PopulateEncoderMetricsFromPerfCounterStats`
  does populate and which `addDispatchKernelEvents` does consume — so wiring it
  back would produce tracks again immediately, and they would carry whatever
  that path yields with no unit resolution, no timestamp-domain proof, and no
  capture-matched Xcode comparison. That is the exact shape the parity
  integrity rules forbid: a value emitted but unvalidated is worse than no
  value.
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

## Two APS signatures were wrong, and one wrote to a wild address

`[V]` Established 2026-08-09 by disassembly, independently reproduced by the
session that maintains the bindings. Commit `1e95552` in this repository
describes the first of these as "fail-closed by accident". That undersells it;
the corrected account is here.

- `agxps_aps_parser_parse` takes five parameters and returns the profile
  pointer in `x0`. It was declared with four and an out-parameter. Two things
  followed. A successful parse returned a non-null pointer that the caller read
  as a nonzero error code, so `agxps.Parser.Parse` could never succeed — that
  part is the accidental fail-closed behaviour. But the null-parser branch does
  `str w8, [x4]`, and purego never set `x4`, so the error store went to
  whatever that register happened to hold: **a stray four-byte write to an
  arbitrary address**, not merely a misread return value.

- `agxps_aps_profile_data_get_counter_values` **does** fill the caller's
  buffer, with exactly `count` eight-byte words. Every word is a sample
  vector's `begin()` pointer, loaded from a 24-byte record indexed by counter
  ordinal; `get_counter_values_num` reads `(end-begin)>>3` from the same
  record. So the pair is (begin pointer, element count) and the declared bulk
  copy is a semantic defect rather than an ABI or memory-safety one. This is
  the more dangerous shape: nothing crashes, nothing is left uninitialised, and
  a caller asking "did the framework write my buffer" gets yes. The values are
  addresses.

  `[V]` `get_counter_names` is *not* affected — it copies the counter-ID vector
  element by element, so its `unsigned long long *out` is correct. Only
  `get_counter_values` does the record lookup.

`[V]` These are not generator heuristics and nothing was name-derived.
GTShaderProfiler ships no headers, so its signatures are hand-authored manifest
data that the generator rendered faithfully. `get_counter_values` even carried
evidence — "live capture" — which was true of the call shape and silent on the
meaning. The durable lesson is the manifest's own, extended: shape evidence is
not width evidence, and is not semantic evidence. Both entries now record the
disassembly address.

The replacement binding is `CounterValuesSlice(p, counterIndex) ([]uint64,
error)`: a function rather than a method, since `AGXPSProfileData` aliases
`uintptr`; bounded against `get_counter_num`; refusing a null begin pointer or
an over-ceiling count; copying rather than aliasing framework storage.

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
Presenting the bundle under the matching name crashes identically.

`[V]` **Refuted: that this is a bindings defect.** The natural suspect was
`cmd/extract_xcode_metrics`, which passes `nil` for `llvmHelperPath:` where
`internal/xcodebindings.ProcessStreamData` resolves a real one — and the
processor spawns a helper whose protocol is version-matched to the framework,
so a nil there is exactly the shape of bug that crashes later. It is not the
cause. The careful path crashes identically on the same capture, with the
helper path resolved, `responds:` checked for every selector, and the returned
processor checked non-nil.

Both entry points reach the same bus error inside `processStreamData` itself,
which is where the framework does its own work.

`[D]` What that does *not* establish is that the bindings are innocent, only
that the two bindings-shaped hypotheses available to test are refuted. Both
paths share one `initWithStreamData:llvmHelperPath:` and one `processStreamData`
declaration, so a defect in either would crash both. The honest limit is that
this path cannot be demonstrated working *anywhere* right now: it is exercised
only on captures carrying a `.gpuprofiler_raw`, exactly one of those survives,
and it is the one that crashes. The successful probe results recorded earlier in
this file came from captures that no longer exist and cannot be re-run.

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
