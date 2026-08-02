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
| Q1. Do command-buffer and encoder labels survive streamData and the processed model? | Find the exact non-ordinal label on each model object, not merely matching counts. | Raw capture half established; profiler-model half pending export. |
| Q2. Do Metal GPU timestamps bridge APSTimelineData and busy offsets? | A transform reproduces every ground-truth/APSTimeline span and busy offset without fitted placement. | Pending profiled capture. |
| Q3. Do host signposts reach Xcode External Process and join GPU work? | A concurrent system-log record appears in Xcode and joins by an explicit shared identifier. | Pending concurrent capture. |
| Q4. Which counter-stream epoch/domain field is wrong? | The selected transform reproduces the known ground-truth time window across the whole sample population. | Pending profiled counter capture. |
| Q5. Do `Counters_f_*.raw` rows carry pipeline-to-encoder identity? | Every row joins through a content-bearing label or identifier, not row position. | Pending profiled counter capture. |

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

The attempt to create the corresponding profiled export reached Xcode's
Performance state, but its Export control remained disabled. An explicit
source-bound recovery/finalization attempt then stopped at the accessibility
guard `cannot establish selected Summary right-pane bounds`. This is an
automation HOLD, not a negative result about labels or timestamps. Preserve the
source bundle and ground truth; retry profiling from a verified Xcode Summary
window rather than replaying or substituting another capture.

## Publication rule

No result from this instrument is published as a counter lane, a merged
wall/busy span, or an External Process event until the relevant question is
answered by an explicit identity and clock/units check. Missing evidence stays
unknown.
