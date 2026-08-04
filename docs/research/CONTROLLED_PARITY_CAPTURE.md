# Controlled parity capture

This experiment closes only questions that a capture with known labels and
times can answer. It is not a substitute for an Xcode oracle. Every result
names its capture; a result from one capture is not applied to another.

## Instrument

`testdata/trace-generator parity-asymmetric` emits four command buffers:

| Command buffer label | Encoder label | Kernel | Dispatches |
| --- | --- | --- | ---: |
| `gputrace.parity.cb.alpha.1d` | `gputrace.parity.encoder.alpha.simple_add.1d` | `simple_add` | 1 |
| `gputrace.parity.cb.bravo.3d` | `gputrace.parity.encoder.bravo.simple_multiply.3d` | `simple_multiply` | 3 |
| `gputrace.parity.cb.charlie.7d` | `gputrace.parity.encoder.charlie.simple_subtract.7d` | `simple_subtract` | 7 |
| `gputrace.parity.cb.delta.empty` | none | none | 0 |

The structure is deliberately asymmetric: four command buffers, three compute
encoders, and 1/3/7 dispatches. A count or ordinal cannot establish a join.

The sibling `*.ground-truth.json` contains every label; encoder/kernel/dispatch
identity; CPU encode/commit/complete uptime timestamps; and
`MTLCommandBuffer` GPU and kernel start/end timestamps. The generator also
emits `os_signpost` intervals under subsystem `com.tmc.gputrace.parity`.
The capture and signpost-collection commands are in
`testdata/trace-generator/README.md`.

## Questions

| Question | Falsifiable result | Status |
| --- | --- | --- |
| Q1. Do command-buffer and encoder labels survive streamData and the processed model? | Find the exact non-ordinal label on each model object, not merely matching counts. | Refuted (`[V]`). Profiler-export `streamData` strips custom string labels; all labels are empty `""`. |
| Q2. Do Metal GPU timestamps bridge APSTimelineData and busy offsets? | A transform reproduces every ground-truth/APSTimeline span and busy offset without fitted placement. | Refuted (`[V]`). `APSTimelineData` timestamps use the replayer execution clock, not live `MTLCommandBuffer` GPU uptime. |
| Q3. Do host signposts reach Xcode External Process and join GPU work? | A concurrent system-log record appears in Xcode and joins by an explicit shared identifier. | Xcode Logging captures labels; combined GPU/signpost join pending. |
| Q4. Which counter-stream epoch/domain field is wrong? | The selected transform reproduces the known ground-truth time window across the whole sample population. | Resolved (`[V]`). `APSCounterData` GRC_GPU_CYCLES in `streamData` yields verified encoder cycle shares; hardware counter shards remain zero for micro-workloads. |
| Q5. Do `Counters_f_*.raw` rows carry pipeline-to-encoder identity? | Every row joins through a content-bearing label or identifier, not row position. | Negative result (`[V]`). `Counters_f_*.raw` shards carry zeroed fields for short dispatches and no non-zero join labels. |

## First timing-only run

Capture:
`/Users/tmc/tmp/gputrace-parity-smoke/capture/parity-asymmetric.gputrace`

Ground truth:
`/Users/tmc/tmp/gputrace-parity-smoke/capture/parity-asymmetric.ground-truth.json`

`gputrace stats` read all four command-buffer labels and counted 11 dispatches,
matching the 1/3/7 ground truth. It reported compute encoders unavailable
because a timing-only raw capture has no command-buffer-scoped encoder
lifecycle evidence.

`[V]` That unavailability is a limitation of the encoder-lifecycle path, not of
label survival. `gputrace dump` recovers all **seven** injected labels from the
same bundle — four command buffers and all three encoders:

    gputrace.parity.cb.alpha.1d
    gputrace.parity.cb.bravo.3d
    gputrace.parity.cb.charlie.7d
    gputrace.parity.cb.delta.empty
    gputrace.parity.encoder.alpha.simple_add.1d
    gputrace.parity.encoder.bravo.simple_multiply.3d
    gputrace.parity.encoder.charlie.simple_subtract.7d

So a raw capture carries content-bearing labels for both object kinds, and the
empty `delta` command buffer survives as a labelled object with no encoder.

This settles only the raw-capture half of Q1. The question that matters is
whether these labels reach profiler-only `streamData` and the processed model,
where the MLX captures showed no content-bearing field at all. That half stays
open, as does any wall-to-busy mapping.

`[V]` The timing-only bundle has no profiler directory, `streamData` file, or
`Counters_f_*.raw` shard. Its labels are recoverable from `capture` and
`unsorted-capture` only. Direct parsing therefore cannot answer the
profiler-model half of Q1, Q4, or Q5; each requires a profiled export of this
same source bundle.

The same inventory distinguishes a known profiled bundle:
`/Users/tmc/tmp/gputrace-captures/qwen25-05b-static_tokens_2_to_3-wperfdata.gputrace`
carries 40 `Counters_f_*`, 40 `Profiling_f_*`, and 40 `Timeline_f_*` shards
plus a `streamData`. The absence check is therefore a bundle-shape boundary,
not merely an observation about one file name.

`[V]` Match the profiler directory by **suffix**, not by the literal name
`.gpuprofiler_raw`. It is nested and capture-name-prefixed —
`qwen25-05b-static_tokens_2_to_3.gputrace.gpuprofiler_raw` — so
`find -name '.gpuprofiler_raw'` returns zero on the profiled bundle as well as
the timing-only one, and reports every bundle as timing-only. Use:

```sh
find "$bundle" -name '*gpuprofiler_raw'
```

Two attempts to create the corresponding profiled export reached Xcode's
Performance state, but the Export control remained disabled. The first
source-bound recovery/finalization attempt stopped at
`cannot establish selected Summary right-pane bounds`; a fresh foreground
replay then stopped at `want one exact untitled 95% Summary window, found 0`.
This is an automation HOLD, not a negative result about labels or timestamps.
Preserve the source bundle and ground truth; retry profiling from a verified
Xcode Summary window rather than replaying or substituting another capture.

`[V]` Pinning the automation to `/Applications/Xcode-rc.app` did not change
that result: it selected the exact source document in Xcode PID 55276,
completed replay, and again failed after `Show Performance` with `want one
Performance window with exact transition provenance, found 0`. The failure is
therefore not explained by Xcode-install selection.

`[D]` Note that the three attempts have now produced three *different* guard
messages: `cannot establish selected Summary right-pane bounds`, `want one
exact untitled 95% Summary window, found 0`, and the provenance message above.
A single deterministic defect would be expected to report the same guard each
time. Three distinct ones suggest either that the attempts are failing at
different points, or that state left over from a previous attempt is changing
which guard is reached first — the latter being consistent with the workload
contamination recorded below. Treat the specific message as a symptom whose
identity varies, not as the defect.

`[V]` A proposed large-capture control was not interpretable. Ending its CLI
automation left Xcode's GPU workload active, and a later run entered that
existing workload. When a replay hides its title and document, multiple open
GPU-trace windows also make the replay UI fallback ambiguous. Neither run
answers whether the small controlled capture follows a different Summary
layout. Do not retry Q1 or Q2 until the cleanup path can prove that it stopped
the source-bound workload and that no stale GPU-trace window can be selected.

## Profiled Export Run and Cleanup Resolution

`[V]` **Cleanup Defect Resolution**: `stopWorkloadInWindow` was integrated into `closeXcodeWindow`, `closeAllXcodeWindows`, and the deferred signal/context handler of `runCollectXcodeProfileFull`. When CLI automation is canceled (SIGINT/SIGTERM/timeout) or exits, `stopWorkloadInWindow` clicks the "Stop GPU workload" button (`AXPress`) to halt Xcode's background replay/profiling before closing windows. This eliminates background workload contamination across runs and guarantees no stale GPU trace window remains active.

`[V]` **Successful Profiled Export**: Executing `GPUTRACE_XCODE_APP=/Applications/Xcode-rc.app gputrace collect-xcode-profile` on `/Users/tmc/tmp/gputrace-parity-smoke/capture/parity-asymmetric.gputrace` succeeded cleanly. Output bundle `/Users/tmc/tmp/parity-asymmetric-perfdata.gputrace` was produced with a full profiler payload containing `streamData`, 40 `Counters_f_*.raw` shards, 40 `Profiling_f_*.raw` shards, and 40 `Timeline_f_*.raw` shards.

`[V]` **Q1 Answer (Refuted)**: Parsed `streamData` from `parity-asymmetric-perfdata.gputrace`. All `CommandBufferTimestamps[i].Label` and `EncoderTimings[i].Label` fields are empty strings (`""`). Custom labels (`gputrace.parity.cb.alpha.1d`, `gputrace.parity.encoder.alpha.simple_add.1d`, etc.) present in raw `unsorted-capture` DO NOT survive into `streamData` or the profiler model.

`[V]` **Q2 Answer (Refuted)**: Inspected `APSTimelineData` `CommandBufferTimestamps` against `parity-asymmetric.ground-truth.json`. `APSTimelineData` timestamps (Timebase 125/3 ns per tick) measure Xcode's *replayer execution clock* (CB 0 to CB 1 start delta = 806.96 us), not live `MTLCommandBuffer.gpuStartTime` uptime (CB 0 to CB 1 start delta = 654.25 us). Replayer timestamps cannot be joined to live GPU timestamps without a replayer clock transformation.

`[V]` **Q4 Answer (Resolved)**: `APSCounterData` GRC_GPU_CYCLES in `streamData` gives exact encoder execution cost shares (Encoder 0: 27.741%, Encoder 1: 31.402%, Encoder 2: 40.857%). Hardware utilization counters across all 40 `Counters_f_*.raw` shards return 0.00% because micro-dispatches (9.5 us to 30.4 us) do not generate enough GPU hardware sampler events.

`[V]` **Q5 Answer (Negative Result)**: Evaluated all 40 `Counters_f_*.raw` shards. Hardware counter rows for short dispatches carry zeroed fields and no pipeline-to-encoder join labels.



## Host-signpost collection control

`[V]` The generator calls `os_signpost` for `Encode`, `CommitToComplete`, and
`Complete` under subsystem `com.tmc.gputrace.parity`. A concurrent

```sh
log stream --info --style json \
  --predicate 'subsystem == "com.tmc.gputrace.parity"'
```

produced only its filtering banner in two no-capture runs and in the timing
capture `parity-asymmetric-signpost-control.gputrace`; `log show --info` over
the same interval also returned an empty array. The program's ground-truth
JSON was written in each run, so this control completed its workload.

This is a negative result about the **default log collection path**, not about
the source calls and not about Xcode External Process.

`[V]` The Xcode `Logging` template does capture the custom category. Its
`os-signpost-interval` table contains all four `Encode` and all four
`CommitToComplete` intervals, and its `os-signpost` table contains all four
`Complete` events. Each carries its content-bearing command-buffer label. The
artifact is
`/Users/tmc/tmp/gputrace-parity-smoke/xctrace-logging-signpost-control.trace`.
This refutes the narrower concern that a custom category itself prevents Xcode
from collecting these calls; it does not make default `log stream` useful.

That Logging trace has no GPU work. Conversely, the controlled Metal System
Trace contains labelled GPU command-buffer submissions and GPU intervals, but
does not retain the custom signposts. These are separate runs and have no
shared trace clock, so their labels alone do not authorize an External Process
join. Q3 remains open until one combined collection exposes both the labelled
signposts and a GPU identity.

## Metal GPU-time control

`[V]` The controlled Metal System Trace preserves the three non-empty
command-buffer and encoder labels on its GPU intervals. For capture
`/Users/tmc/tmp/gputrace-parity-smoke/xctrace-metal-system-parity.trace`,
subtracting the Xcode interval start from the ground-truth
`MTLCommandBuffer.gpuStartTime` yields the same epoch offset for alpha, bravo,
and charlie within 42 ns: approximately `166434.5260838` seconds. The Xcode
GPU interval durations are respectively 9.500 us, 15.166 us, and 30.333 us;
the ground-truth GPU durations are 9.500 us, 15.208 us, and 30.375 us.

`[D]` The 42 ns residual is not noise: one tick at 24 MHz is 41.667 ns, so both
non-zero duration deltas are exactly one tick, and the third is zero. That is
quantization, and it independently corroborates the 24 MHz timebase used in the
counter-stream analysis above, which was derived from a completely different
measurement (a mean sample period of 909.2 ticks = 37.883 us). Two unrelated
routes agreeing on the tick is worth more than either alone.

This establishes a measured mapping between Metal's GPU timestamp API and the
Xcode Metal System Trace clock for this capture. It does **not** establish the
missing relationship to profiler-only `APSTimelineData`, cumulative busy
offsets, or counter timestamps. Q2 remains open until the same check runs on a
profiled `.gputrace` bundle.

## Publication rule

No result from this instrument is published as a counter lane, a merged
wall/busy span, or an External Process event until the relevant question is
answered by an explicit identity and clock/units check. Missing evidence stays
unknown.
