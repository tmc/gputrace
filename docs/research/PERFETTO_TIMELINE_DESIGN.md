# Perfetto Timeline Design

## Status

This document specifies the next exporter iteration. It is a design, not a
claim that the listed counters or correlations are already decoded.

The reference capture is
`/Users/tmc/tmp/mlx-go-fast/profiled/qwen25-05b-python-producer-tokens1-3-perfdata.gputrace`.
Its APSTimelineData command buffers span 2.979 seconds of wall time. Its
encoder and dispatch records describe about 23.5 milliseconds of cumulative
GPU-busy time. These are different clocks.

## Goals

- Produce a JSON trace that Perfetto trace_processor accepts without parser
  errors or unexpected synthetic overflow tracks.
- Present compute work in the same useful shape as the Xcode GPU timeline:
  encoder spans with dispatches beneath them, plus measured counter tracks.
- Preserve source and timing provenance on every exported datum.
- Add structure only when the capture provides the corresponding identity,
  timestamp, or measurement.

## Non-goals

- Do not place encoders inside command buffers. Command-buffer timestamps are
  wall-clock intervals, while encoder and dispatch timestamps are cumulative
  busy time.
- Do not synthesize occupancy, bandwidth, power, frequency, or duration.
- Do not emit a flow edge without a capture correlation ID.
- Do not publish all-zero decoded-counter tracks as measurements.
- Do not place API-call records: this capture's decoded calls have no timestamps
  and represent none of its 864 dispatches.
- Do not place buffer-access records: their attribution covers one of the 21
  encoders and is explicitly incomplete.

## Current shape

The exporter emits these independent lanes:

1. Command buffers, using APSTimelineData hardware tick deltas relative to the
   capture absolute time.
2. Compute encoders and dispatches, using streamData cumulative busy time.
   A dispatch shares its encoder lane only when its interval is strictly
   contained by the encoder interval; otherwise it remains on an explicitly
   named fallback lane.
3. GPRWCNTR sample and encoder-profile events.
4. Timing and Xcode-metrics provenance events.

Zero-microsecond command buffers are instant events, not complete events with a
missing `dur` field. This keeps the JSON valid for strict readers without
inventing a duration.

## Proposed track model

Use stage-oriented names where the capture identifies the stage:

```
GPU trace
├── Command buffers (wall clock)
├── Encoders / Compute (cumulative busy)
│   └── Dispatches / Compute (strictly contained only)
├── GPRWCNTR profiles
└── Measured counters
```

The visual nesting is only encoder to dispatch. Command buffers remain a
parallel wall-clock track. A future render or blit encoder gets a separate
stage track only after parsing gives it a stable type and timestamp span.
Lane numbering is an implementation detail of the overlap packer, not a
user-visible stage name.

## Counter plan

Xcode displays measured series for active cores, occupancy, shader-launch
limits, bandwidth, ALU/F32/F16 utilization, cache, MMU, residency, and SIMD
groups in flight. The exporter should add a series only after its source column
and unit are decoded and validated against the Xcode export.

For each candidate counter:

1. Identify the archived column and record stride.
2. Decode a nonzero sample series from the reference capture.
3. Verify timestamp clock, unit, and value scale against an Xcode export that
   joins to the same capture.
4. Emit a Perfetto counter track with source and unit arguments.
5. Add a fixture that rejects an all-zero or mismatched-stride series.

Step 3 is a hard publication gate. The surviving Xcode oracle does not share
join keys with this reference capture, so it can support a candidate name or
unit but cannot validate a candidate value. A counter without a joinable oracle
stays unpublished.

## Optional flow plan

Flows are useful in Chrome and Android reference traces, but gputrace must not
infer them from temporal proximity. Add a flow only when the archive exposes a
stable producer and consumer identity, such as an API submission identifier
that exactly joins to a command-buffer record. The flow arguments must identify
the joining field and its source section.

## Validation gates

For every exporter change:

1. `go test ./...` and `go vet ./...` pass.
2. Generate the reference Perfetto JSON with `gputrace timeline`.
3. Load it with `trace_processor_shell` and inspect `stats`, `slice`,
   `counter`, `flow`, and `track` tables.
4. Require zero error-severity parser statistics. The names that matter for a
   JSON trace, confirmed present in trace_processor v57.2 and currently zero on
   our export, are:

       select name, value from stats
       where name in ('json_tokenizer_failure',
                      'json_parser_failure',
                      'flow_no_enclosing_slice');

   `flow_no_enclosing_slice` only becomes meaningful if the flow plan is ever
   taken up; it is listed so that adding flows cannot quietly skip a gate.

   Query the installed trace_processor's `stats` table rather than assuming a
   name. An earlier draft gated on `slice_spill_overlapping_complete_event`,
   which does not exist in v57.2 -- nothing matches `%spill%` or `%overlap%`.
   A gate on a nonexistent statistic passes unconditionally while reading as
   verified, which is the defect this document exists to avoid, relocated into
   the test harness.
5. Assert export coverage for the reference capture: 30 command buffers, 21
   encoders, and 864 dispatches.
6. Pin the currently unattributed 108 of 864 dispatches. Any increase requires
   an attribution explanation and fixture update, not a rendering-only change.
7. Compare emitted counter values and visible hierarchy against Xcode only when
   the two records share a documented clock and identity.

## Evidence

`~/tmp/gputrace-perfetto-reference/perfetto-reference-structure.md`, generated
by `tools/perfetto-reference.sh`, records the SQL queries and results from
Perfetto's Chrome and Android example traces and the current gputrace export.
Those traces show that nested slices, counters, and flows are supported shapes;
they do not prove that every shape is sourceable from an Apple GPU capture.
