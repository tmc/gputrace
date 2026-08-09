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

`[V]` That bundle no longer exists on this machine, so the command above skips
rather than scores. See "Capture inventory" below before reading any count in
this file as a live measurement.

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
exactly the pipeline count. `PopulateEncoderMetricsFromBinaryParsing` retains
its historical name, but returns one row per pipeline rather than one row per
encoder.

`f04175b` removed the unsafe `Counters.csv` exporter path that indexed those
rows by encoder position. It now writes encoder identity with blank metric
columns and reports the withholding on stderr. This stays safe even when the
pipeline and encoder counts happen to agree.

The remaining problem is an identity join, not an exporter bounds check. A
counter-file metric may be published only after a capture-backed mapping shows
which encoder owns its pipeline row, and after its timestamp, unit, and
capture-matched Xcode residual are established.

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

#### A zero-offset control capture: the windows are disjoint with no transform

`[V]` Measured 2026-08-06 on `~/gputrace-fixtures/parity-asymmetric-perfdata.gputrace`,
the only surviving bundle with a `.gpuprofiler_raw`. This capture is useful
precisely because it is degenerate:

    absoluteTime    1219573087880
    continuousTime  1219573087880
    offset          0

`[V]` The two epoch fields are **equal**, so every candidate transform built
from `continuousTime - absoluteTime` is the identity here. Whatever is wrong
cannot be the offset, the sign, or the choice between those two fields, because
all three collapse to the same arithmetic on this capture.

`[V]` The windows are still disjoint:

    counter sysTS union   1219561582008 .. 1219562479774   (37.4 ms)
    command buffers       1219578246670 .. 1219578292956   ( 1.9 ms)

The first command buffer starts 15,766,896 ticks — **656.9 ms** — after the last
counter sample. Subtracting nothing still leaves them non-overlapping, so the
42.626 s residual on the qwen capture is not by itself evidence that the
transform was wrong. Non-overlap reproduces with the transform removed.

`[V]` **Refuted: the 40 shards are a time partition.** All 40 were parsed. Every
one reports the same `sysTS` start (`1219561582008`) and an end within 2,816
ticks (117 us) of every other. They partition the *counters*, not the timeline;
a later shard does not cover a later window. This is the whole population, not
a sample, because this file has already had to retract one verdict reached from
a single shard.

`[?]` The reframed question, offered as a hypothesis and not a finding: if the
counter pass and the timing pass are separate GPU replays, no anchor between
them exists by construction, and the join would have to be built *within* the
counter pass rather than across the two. That would explain non-overlap on both
captures without any field being misidentified.

`[D]` This does not close item 2, and it is not the "no shared anchor exists"
conclusion this file warns against — that one was reached by comparing two
series already known to share a clock. This is a measurement showing the
premise of the search may be wrong. Testing it needs a capture whose counter
window and CB window can be established independently, which no surviving
bundle provides.

#### Refuted: kick_software_id as the encoder join `[V]`

`kick_software_id` is not the encoder-sequence-ID join, on this capture. All 40
`Counters_f_*.raw` files were parsed and each reports 0 hits and 11 misses for
the full streamData encoder set `[1441, 1444, 1447, 1451, 1477, 1512, 1546,
1583, 1617, 1622, 1626]`.

The all-files scope is the load-bearing part. An earlier run of the same
experiment against `Counters_f_0.raw` alone also returned zero hits, but that
file holds roughly 1/40 of the kick population — every one of the 40 files is
~32.7 MB, 1,314,193,408 bytes total — so a miss there was the expected result
whether or not the join existed. A partial-shard zero is not a negative
result. Only the union is.

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
- Search the whole population before recording a negative. `Counters_f_*.raw`
  is 40 shards; a zero from one of them is not evidence of absence, and this
  file has already had to retract one verdict reached that way.
- Record how a number was established, not only what it is.

## Order of work

1. Decode `Counters_f_*.raw` at all, then establish a capture-backed
   pipeline-to-encoder identity join for its rows.

   `[V]` Corrected 2026-08-09. This item previously read "binary parsing
   produces 18 pipeline rows for 23 encoders (`[V]`)" and held pending "a
   non-positional owner join". Both halves were wrong, and the marker was
   `[V]` on a claim nobody had traced through the code.

   `[V]` No counter row is decoded. `parseCounterFileWithMetrics`
   (`internal/counter/counter.go:239`) frames records, validates that at least
   one parsed, and then returns `nil` for the metrics slice — the parsed
   records are discarded at the return. `ParsePerfCounters`'s aggregation loop
   therefore never runs and `ShaderMetrics` is empty.

   `[V]` The 18 rows are not counter rows. `enhanceFromStreamData`
   (`counter.go:815`) appends `streamData.Pipelines` when the pipeline count
   exceeds the shader-metric count; since the latter is always zero, that
   branch is not a fallback but the only path that ever populates
   `ShaderMetrics`. The 18 is `len(streamData.Pipelines)` — compiler statistics
   — so it is a fact about where the rows came from, not a clue about raw-row
   identity. Raw series transport does work across all 40 shards (`[V]`); that
   is transport, not decode.

   `[V]` The prohibited join is already being performed. `EncoderIndex: i` in
   `PopulateEncoderMetricsFromPerfCounterStats`
   (`internal/counter/sampling.go:657`) assigns a pipeline row's slice index as
   an encoder index. `internal/parity/observe.go` fails closed on exactly this
   and is why parity never scored these columns, but
   `cmd/gputrace/cmd/timeline.go:923` and
   `internal/export/pprof_enhanced.go:485` consume the same function unguarded.
   The block is commented "From binary parsing (gputrace-44 validated
   approach)" while the values come from streamData, which is how a wrong
   provenance label kept this in place.

   `[V]` **Refuted 2026-08-09: the framework will not supply the owner.**
   Disassembly of `agxps_trace_mtl_command_encoder_add_kick` shows it
   bounds-checks a caller-supplied `(encoderIndex, kickIndex)` pair and stores
   it. The exported profile-data accessor population carries kick, clique,
   tile, timestamp and counter accessors and **no encoder or pipeline
   accessor**. Xcode knows the encoder association because Xcode supplied it at
   trace-build time; APS decoding does not recover one. This kills the
   hypothesis that Xcode's own decoder resolves owner, clock and unit together
   — it resolves clock and unit, but the owner was never in the counter data.

   `[V]` **Refuted 2026-08-09: kick ids do not bridge to the GPRWCNTR stream,
   and cannot.** `GPRWCNTRSample` carries both `EncoderID` and `KickTraceID`,
   which makes `Counters_f` `kick_id` → `KickTraceID` → `EncoderID` the obvious
   candidate. It fails twice over on `parity-asymmetric-perfdata`.

   The decisive reason is that `kick_id` is **parser-local, not
   capture-global**: a fresh parser per shard yields 126 distinct ids, valued
   0..125 with repeats, across 927 kick records. A small dense counter restarted
   per parser is an array index, not an identifier, so it cannot be a foreign
   key into anything regardless of what it is compared against.

   The population check agrees: those 126 ids intersect the 49 distinct GPR
   `KickTraceID`s zero times, and the packed-pair reading is empty in both
   halves — 69 high-32 values against `EncoderID`, 129 low-32 against
   `KickTraceID`. The capture is not signal-free (3,389 timeline GPRWCNTR
   samples, 48 attributed CounterArchive rows), so this is a real negative and
   not an artifact of an unsampled workload.

   `[V]` **A retracted intermediate result, kept because the failure mode
   recurs.** This section first recorded "927 distinct `kick_id`s for 927
   kicks", read as a promising unique foreign key, and separately "39 of 40
   shards wrap once", read as invalidating first/last range arithmetic. Both
   were artifacts of one stateful APS parser being reused across all 40 shards
   in `TestCounterFileFanout`: the reuse carried the previous shard's boundary
   into the next, inventing both the descent and the id spread. A fresh parser
   per shard gives zero descents in 40/40 and sample counts one lower on the 39
   affected arrays. The reuse was in committed code, not in a scratch probe, and
   it produced numbers that looked like findings rather than like errors.

   `[V]` What did **not** change under fresh parsing: every shard still starts
   at system timestamp 1219561582008 and ends within 2,816 ticks (117.3 µs) of
   the others. The shards are parallel views of one window, not time partitions.
   That conclusion was reached with the contaminated probe and survives its
   correction.

   `[D]` The refutation is bounded to one capture and must not be generalized
   to the method. That capture's dispatches fall below the sampler threshold,
   and its 927 `kick_id`s against 49 GPR kick ids is a large enough asymmetry to
   suggest two different id spaces rather than a failed join within one. Two
   captures are what caught that the Execution Cost residual was 2.941 pp and
   not the 0.911 pp one trace showed; one capture has never been enough here.

   Next step: HOLD — a non-positional owner join, resolved metric/unit/scope,
   timestamp-domain proof, and capture-matched Xcode oracle comparison are all
   still required (`[D]`), but they are the problems that appear *after* a
   decode exists. Today there is no decoded row to own, clock, or unit.
2. Resolve the counter-stream timebase (`[V]`). It converts 137 decoded series from
   unjoinable to scoreable, which is the only path to a large fraction of the
   83 unproduced columns (`[V]`).
3. Execution Cost residual (`[V]`): Current encoder-share estimate disagrees with Xcode on 18 of 23 encoders (0.911 pp worst-case on 23-encoder oracle, 2.941 pp worst-case on 11-encoder oracle) (`[V]`). `Execution Cost` has no `GPUCounterGraph.plist` entry, so its definition is not recoverable from the catalog (`[V]`). Selection views must report the residual rather than claiming exact Xcode parity (`[D]`). Next step: HOLD — definition uncatalogued; preserve explicit residual warning (`[D]`).
4. Uncatalogued oracle columns (`[V]`): 20 oracle columns (e.g. `Execution Cost`, `F32 Limiter`, `FS Last Level Cache Bytes Read`, `* Bandwidth`) appear in Xcode exports but are absent from `GPUCounterGraph.plist` (`[V]`). Prior catalog selector probes have ruled out catalog-based unit recovery (`[V]`). Next step: HOLD — catalog route probed and ruled out; requires a proven unit source (`[D]`).
5. Score the 11-encoder oracle in CI — **DONE, and it was not a path problem**
   (`[V]`). `TestParity` now takes its oracle from `GPUTRACE_PARITY_ORACLE`.
   The hardcoded `oracleDir` was the smaller half: `LoadOracle` globbed `*.txt`
   and assumed every match was an encoder tab, so the second oracle directory
   failed to load at all (`[V]`). It also holds Xcode's Shaders tab, which is
   keyed by kernel function and pipeline state rather than by encoder, and the
   encoder-list check rejected the directory with "the files are not from one
   capture" — a wrong cause, and one that would have sent a reader hunting for
   a capture mixup that never happened (`[V]`).

   Non-encoder-keyed tabs are now skipped and named in `Oracle.Skipped`; a tab
   that mixes row spaces, or a directory with no encoder-keyed tab at all,
   stays an error. `TestBothOraclesLoad` pins both: 23 encoders / 234 columns
   skipping nothing, and 11 encoders / 230 columns skipping `shaders.txt`
   (`[V]`). The 23-encoder side is byte-unchanged, so the standing table above
   is not affected.

   `[D]` Loading is not scoring. Both oracles load, but neither can be scored
   until a matching capture exists — see "Capture inventory".

## Capture inventory

| Capture | Encoders | Dispatches | GPU time | Oracle |
| --- | --- | --- | --- | --- |
| `...staticmask-warm-tokens2-4-rep1-perfdata3` | 23 | 958 | 9.161 ms | `testdata/xcode-oracle/` |
| `...staticmask-warm-tokens2-4-rep1-perfdata2` | 23 | 958 | 9.161 ms | md5-identical inputs to perfdata3 |
| `qwen25-05b-static_tokens_2_to_3-wperfdata` | 11 | 466 | 5.330 ms | `testdata/xcode-oracle-static-tokens2to3/` |

`[V]` **None of these three bundles still exists.** Checked 2026-08-06:
`~/tmp/gputrace-captures/` contains only a `.DS_Store`, and no `.gputrace`
bundle matching either capture name is present anywhere under `~`. Only derived
artifacts survive — per-probe logs and diff JSON under `~/tmp/`.

The consequence is stated plainly because it applies to every number above:
**the standing table cannot currently be reproduced or regressed.** `TestParity`
skips without `GPUTRACE_PARITY_TRACE`, and there is no bundle to point it at.
The oracle side of the join is safe — `testdata/xcode-oracle/` and
`testdata/xcode-oracle-static-tokens2to3/` are in git — but the gputrace side
cannot be recomputed, so the table is a record of a 2026-08-01 measurement
rather than a check that runs.

This paragraph has now been wrong in both directions. A 2026-08-01 note declared
the captures lost when they were present, and the correction to that note
asserted `[V]` that they lived in `~/tmp/gputrace-captures/` — which stayed in
the file after they stopped doing so. A `[V]` on a claim about the filesystem
decays; it records that someone looked once, not that the file is there now.
Re-check presence before relying on any row above, and date the check.

`[D]` Regenerating these is not a re-run of a script. The parity rules forbid
substituting a different capture, so a replacement needs the same workload, the
same device, the same capture mode, and a fresh Xcode Counters-tab export to
serve as its own oracle. Until that exists, treat every count in this file as
historical.

The surviving bundles under `~/gputrace-fixtures/` — `gputrace-parity-smoke`,
`parity-asymmetric-perfdata.gputrace`, and `raw-sources/` — have no matching
Xcode oracle, so they exercise the parity machinery without scoring it.
