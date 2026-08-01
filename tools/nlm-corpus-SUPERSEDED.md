# Superseded claims in this corpus — read before answering "what are we missing"

This corpus is a historical record. It contains documents describing defects
that have since been fixed, alongside documents describing the fix. Nothing here
is dated in a way a reader can rank, so a question of the form "what is gputrace
currently missing?" will surface bugs that no longer exist.

The following are **FIXED IN THE CODE** as of 2026-08-01 (`ee41dff`). Any
document in this corpus describing them as open is superseded:

- **GPRWCNTR fixed 168-byte stride.** The derived stride now wins, with a
  regression test that fails when a stride does not divide its blob. 168
  survives only as an RDE_0 test fixture.
- **Execution Cost keyed by pipeline ID.** Now attributed per encoder via
  `GRC_ENCODER_ID` and published by `internal/parity/observe.go`.
- **The `GRC_ENCODER_ID` join itself.** Already implemented in
  `internal/counter/counterarchive.go`, including machine-wide `0xFFFFFFFF`
  handling. It is not a proposal.
- **Encoderless attribution, JIT kernel diagnosis, kernels without
  unsorted-capture, the GPRWCNTR record parse.** All landed.

The following are **KNOWN WRONG** and must not be repeated as fact:

- `docs/research/COUNTER_FILE_MAPPING.md` — falsified. `Counters_f_12.raw` is
  not "ALU Utilization"; it holds 137 counters. The file carries its own
  retraction.
- Any occupancy formula involving a divisor (e.g. "divide SIMD Groups Inflight
  per Core by 96"). No such constant exists in the archive or the framework. A
  fabricated occupancy formula was deliberately deleted from this codebase; do
  not reintroduce one.

The following is **OPEN** and has been wrongly closed once:

- The `Counters_f_*.raw` timebase. Applying the archive's own
  `continuousTime - absoluteTime` offset leaves a 42.6 s residual against
  `cb[0].start`, while both windows span a comparable ~3 s of the same capture.
  It was once declared "no shared anchor exists" on the basis of `cb[0].start`
  agreeing with the profiler ring start — but both come from `APSTimelineData`
  and already shared a clock, so that comparison never tested the counter
  stream. Encoder identity does **not** appear per row in `Counters_f_*.raw`;
  `GRC_ENCODER_ID` lives in `APSCounterData`, a different source, and does not
  resolve this.

## What this corpus is good for

Asking about **Xcode** and **Perfetto**, not about gputrace's current state:
what Xcode's timeline shows, what track structure would express GPU work well,
which of the 274 oracle columns belong in a timeline versus a table. Those
answers do not go stale.

For gputrace's current state, read the code, not this corpus.
