# Parity with Xcode-represented data

What Xcode shows for a capture, what gputrace reproduces, and what stands
between the two. This is the maintained scoreboard: update it when a measured
number changes, and record how the new number was obtained.

`[V]` marks a verified fact, `[D]` a conclusion derived from a capture-backed
test, and `[?]` a hypothesis not yet tested.

`XCODE_PARITY_LOOP.md` is the procedure for running an iteration. This file is
the state that procedure moves.

## How parity is measured

`internal/parity` joins Xcode's own Counters-tab exports to gputrace's output
for the same capture, per encoder, per column. The join key is the encoder's
cumulative end offset in microseconds, which Xcode buries as the leading number
of the encoder display name and `encoderInfoData` publishes directly.

A column gputrace does not emit is reported `NOT PRODUCED`. It is never
reported as a match and never defaulted to `0.00`. Reproduce with:

```bash
GPUTRACE_PARITY_TRACE=~/tmp/gputrace-captures/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace \
  go test ./internal/parity/ -run TestParity -v
```

The oracle is not the universe. `GPUCounterGraph.plist` defines 456 counters;
Xcode's exports expose 234 of them for this capture, and the Timeline's
Occupancy filter shows at least one more (`SIMD Groups Inflight per Core`) that
no export column carries. Every count below is against what Xcode *exports*,
not against what it measures.

## Standing, 23-encoder capture, 2026-08-01

Capture `qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace`,
oracle `testdata/xcode-oracle/`, 234 distinct columns.

| Status | Columns | Meaning |
| --- | --- | --- |
| MATCH | 0 | value agrees with Xcode |
| MISMATCH | 1 | value produced, disagrees |
| NOT PRODUCED | 83 | gputrace emits nothing |
| ORACLE SUSPECT | 7 | Xcode's own column is defective |
| NO SIGNAL | 143 | zero or empty for every encoder |

`[D]` The 143 no-signal columns are graphics counters on a compute-only
workload; they are not a parity gap. The real surface is **83 columns of real
signal that gputrace does not produce**, plus the one column it produces and
gets wrong.

`[V]` gputrace also produces two per-encoder values Xcode's Counters tab has no
column for: `Dispatches` (inferred, `gpuCommandInfoData` records bucketed into
`encoderInfoData` offsets) and `Encoder Duration us` (runtime, successive
differences of cumulative end offsets).

### The one mismatch: Execution Cost

`[D]` 18 of 23 encoders disagree; 5 agree exactly. Maximum residual **0.911
percentage points** at encoder `10974` (gputrace 8.829% vs Xcode 9.740%),
RMS **0.277 pp** over all 23 encoders. Source: `APSCounterData`
`GRC_SAMPLE_TYPE` 4/5 encoder spans, GPU cycles summed per ordinal
(`internal/counter/encodercost.go`).

`[D]` The same method scores **2.941 pp** worst-case against the 11-encoder
capture (`testdata/xcode-oracle-static-tokens2to3/`), concentrated in encoder 9.
Two captures disagreeing by 3x on worst-case residual is the reason both oracles
are kept. A single capture would have reported this method as near-exact.

Execution Cost has no `GPUCounterGraph.plist` entry, so its definition is not
recoverable from the catalog; the residual is currently unexplained rather than
attributed.

## Known defects blocking parity

### Counter-file rows are per pipeline, not per encoder `[D]`

`Counters_f_*.raw` parsing produces 18 rows for a 23-encoder capture. 18 is
exactly the pipeline count. `PopulateEncoderMetricsFromBinaryParsing` returns
one row per pipeline, and the `Counters.csv` exporter indexes that result by
encoder position — so pipeline data is published under encoder labels whenever
the two counts happen to allow it.

This is the highest-value fix on the list: it is a silent mislabeling, not a
missing feature, and it currently gates every counter-file column. No
counter-file column is published today, which is the correct fail-closed
behavior, but the indexing bug remains latent.

### The counter stream sits in neither published timebase `[V]`

Attributing the 137 counter series per encoder requires joining the
`Counters_f_*.raw` sample clock to `encoderInfoData` offsets. It does not join,
and the reason is a measured disagreement, not a missing field.

For `qwen25-05b-python-producer-tokens1-3-perfdata.gputrace` the archive
publishes its own timebase record:

    absoluteTime    5044475728398
    continuousTime  5181167935604
    offset          136692207206     (continuous - absolute)

    sysTS[0]        5180152293797    span 62161289 ticks = 2590.054 ms
    cb[0].start     5044483113510    span 71500426 ticks = 2979.184 ms

Applying the archive's own offset to the first counter sample:

    sysTS[0] - offset  = 5043460086591
    cb[0].start - that = 1023026919 ticks = 42.626 s   (@24 MHz)

The clock rate is not the error: the mean sample period is 909.2 ticks =
37.883 us at 24 MHz, self-consistent with the sample count over the span. The
two windows have comparable spans and describe the same ~3 s of activity, so
the transform is wrong rather than the data. `sysTS` is not anchored to
`continuousTime` by subtracting `continuousTime - absoluteTime`.

**Do not close this as "no shared anchor exists."** That was concluded once by
comparing `cb[0].start` to the profiler ring start (they agree to 49.8 us) —
but both come from `APSTimelineData` and were already known to share a clock,
so the comparison says nothing about the counter stream. The open question is
which of {domain, sign, epoch field} is misidentified.

Reproduce with `TestStreamDataTimebaseProbe` (`GPUTRACE_PROBE_STREAMDATA`) and
`TestCounterFileParse` (`GPUTRACE_PROBE_COUNTERS`).

### 20 oracle columns have no catalog entry `[V]`

`Execution Cost`, `F32 Limiter`, `FS Last Level Cache Bytes Read`, and 17
`* Bandwidth` columns appear in Xcode's exports but are absent from
`GPUCounterGraph.plist`. Their units are unresolved, so even a matching number
could not be labelled correctly. Any parity claim on these columns needs a unit
source first.

## Defects in the oracle itself

Xcode's own exports are not uniformly trustworthy. `internal/parity` flags
these and refuses to score them:

| Column | Defect |
| --- | --- |
| Device Atomic Bytes Written | byte-identical to Device Atomic Bytes Read in every row |
| Kernel ALU Performance | byte-identical to Kernel ALU Instructions in all 23 rows: a raw count under a Giga Ops/Second label |
| Kernel Invocations | 0 for two encoders that have non-zero Execution Cost and real dispatches |
| L1 Cache Utilization | byte-identical to L1 Cache Limiter in every row |
| Predicated Texture Thread Reads | constant across all encoders: carries no per-encoder information |
| Predicated Texture Thread Writes | byte-identical to Predicated Texture Thread Reads |
| Texture Write Utilization | byte-identical to Texture Write Limiter in every row |

`[V]` Both exports of the same tab are deterministic, and 16 repeated header
names are byte-identical across tabs, so these are defects in what Xcode
computes, not export noise.

## Integrity rules for parity work

- Never re-export the counter tabs from a *different* capture to fill a gap in
  an existing oracle. That silently swaps the workload underneath the numbers,
  producing a match rate that looks measured and is not. No number is the
  correct outcome.
- Never join figures across captures. Three kick counts exist from three
  sources — 6304 (streamData processors), 3792 (one `Counters_f_0.raw` of 40),
  12706 (two `Profiling_f` files) — and none of them is a discrepancy.
- A column is `NOT PRODUCED` until it is joined, unit-resolved, and scored. A
  value that is emitted but unvalidated is worse than no value.
- Record how a number was established, not only what it is.

## Order of work

1. Fix the per-pipeline/per-encoder indexing in
   `PopulateEncoderMetricsFromBinaryParsing`. It is a live mislabeling and it
   gates the whole counter-file surface.
2. Resolve the counter-stream timebase. It converts 137 decoded series from
   unjoinable to scoreable, which is the only path to a large fraction of the
   83 unproduced columns.
3. Explain the Execution Cost residual, or label the shipped figure with it.
4. Find a unit source for the 20 uncatalogued columns.
5. Score the 11-encoder oracle in CI. `TestParity` currently hardcodes
   `oracleDir` to `testdata/xcode-oracle`, so the second oracle is only
   measured by hand.

## Capture inventory

| Capture | Encoders | Dispatches | GPU time | Oracle |
| --- | --- | --- | --- | --- |
| `...staticmask-warm-tokens2-4-rep1-perfdata3` | 23 | 958 | 9.161 ms | `testdata/xcode-oracle/` |
| `...staticmask-warm-tokens2-4-rep1-perfdata2` | 23 | 958 | 9.161 ms | md5-identical inputs to perfdata3 |
| `qwen25-05b-static_tokens_2_to_3-wperfdata` | 11 | 466 | 5.330 ms | `testdata/xcode-oracle-static-tokens2to3/` |

All live in `~/tmp/gputrace-captures/`. `[V]` A 2026-08-01 note recorded these
as lost to a reboot and declared the oracle permanently unjoinable; that was
wrong. The captures were on the persistent volume and the per-encoder join
succeeds, which is how the standing table above was produced. Captures written
to `/tmp` do not survive; these were not.
