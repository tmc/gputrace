# Design: capturing Xcode's Counters lanes in the gputrace timeline

Status: design, not implemented.
Date: 2026-08-01.
Scope: the lanes Xcode's Counters panel draws — 27 rows collapsed, **84 leaf
series expanded**. The full tree, and the delivery order over it, is §11; read
that before costing anything, because the collapsed count understates the work
by 3×.

Confidence markers follow the repo convention: `[V]` verified against a binary
or a measurement, `[D]` derived by a check that could fail, `[?]` inferred.
Anything unmarked is a design proposal, i.e. not yet true of anything.

## 1. Where we actually are

Measured from four traces in `~/tmp/gputrace-wall-view-verify/staticmask`,
queried with `trace_processor_shell`:

The timeline export emits **10 counter tracks**, none of which is one of the
lanes above.

| track | samples | what it really is |
|---|---|---|
| GPU Cycles | 46 | measured, per encoder, from `CounterArchive.EncoderCosts()` |
| Execution Cost | 46 | measured %, same source |
| Total/ALU/INT32/INT16/FP32/Branch Instructions | 6–14 | **static** `pipelinePerformanceStatistics`, replicated onto the timeline |
| Allocated/Uniform Registers | 14 | **static** compile-time register counts |

Only the first two vary with time. The other eight are compile-time constants
per pipeline; they are not counter lanes in Xcode's sense and appear in no
Xcode lane.

Five further tracks are **declared in code but emit nothing**: Active Cores,
ALU Utilization, Bandwidth, Instruction Throughput, Shader Launch Limiter
(`cmd/gputrace/cmd/timeline.go:1387-1522`). Every encoder falls through
`if metrics == nil && encoderMetric == nil { continue }` at `timeline.go:1500`,
so all five end up empty and are dropped by the exporter. `[V]` — observed as
their absence from all four exported traces.

The remaining ~74 leaf series have no implementation at all (§11 counts the
tree: 27 collapsed rows, 84 leaves).

## 2. Why the obvious route is closed

The lanes are drawn from `Counters_f_*.raw`. Two prior findings constrain any
design and must not be re-litigated:

- `docs/research/COUNTER_FILE_MAPPING.md`: the "file N *is* counter column N-4"
  formula is **falsified**. `[V]` Each `Counters_f_N.raw` carries a full
  multi-counter capture pass — 137 counters, thousands of kicks — not one
  series. Files are separate capture windows, not table columns.
- `docs/research/COUNTER_NAME_MAPPING.md`: raw counter ids are 64-hex
  obfuscated hashes and **cannot be deobfuscated**. `[V]`
  `agxps_counter_deobfuscate_name` reads a table filled only by
  `agxps_load_counter_obfuscation_map`, which loads `RawCountersMapping.csv`
  from a bundle Apple does not ship to this machine. With both frameworks
  dlopened, deobfuscation is the identity function for 578/578 hashes. Xcode
  itself displays raw hashes for raw counters.

So there is no id→name dictionary, and no per-file shortcut. Any design that
assumes either is dead on arrival.

## 3. The fallback route: reconstruct Apple's arithmetic

> **Read §10 first.** It describes three routes that call Apple's own
> derived-counter computation instead of rebuilding it, and any of them makes
> most of this section and most of §4/L3–L4 unnecessary. This section is the
> fallback for when all three spikes fail. It is kept because it is the only
> route with no unknown floor — it is expensive, not blocked.
>
> An earlier revision presented this section as the primary route and claimed
> "every link is independently verified." That was wrong; the joint at step 2
> does not exist. The corrected version is below. The error is recorded rather
> than silently fixed because it is this project's characteristic failure mode
> — see `gputrace_silent_wrongness`.

The join runs the other way — from the lane name down to the hashes.

1. `[V]` `GPUCounterGraph.plist` (455 counters, shipped in Xcode) binds each UI
   lane name to a list of plaintext **vendor** counter names, plus its unit and
   visibility. Already parsed: `internal/parity.Catalog` /
   `CatalogEntry.VendorCounters`, located via `internal/xcodepath`.
2. **The vendorCounter → raw-hash joint does not exist as a lookup.** `[V]`
   `docs/research/COUNTER_NAME_MAPPING.md`: of the 533 distinct vendorCounter
   identifiers, 274 do not appear in the GTShaderProfiler arm64 slice at all,
   and "appearing as a string would not constitute a resolution to a raw hash in
   any case." That document names this exact route as the one that "looked
   viable for longer than it deserved," and gives the reason: *a count of how
   many Xcode column names appear in the plist is not a measure of what can be
   computed.*

   What **is** `[V]` is the *derived-counter-name* → raw-hash direction, read
   out of GTShaderProfiler's instruction stream at `0x54d9d0-0x54e0c0` and
   corroborated at `0x4f478c-0x4f4818`:

       Compute Simdgroups Inflight Per Shader Core <- 33634F0D, FD6F91B4, 50E7E1AA

   Derived-counter names and vendorCounter identifiers do coincide in the cases
   we can inspect — the line above is the vendorCounter for lane `Compute SIMD
   Groups Inflight per Core`. So the chain is not fictional, it is **unbuilt**:
   each binding must be *read out of the instruction stream*, per counter. Cost
   is a scripted disassembly walk over ~150 derived counters, which is what L3
   has always said. Anything that describes step 2 as a lookup is wrong.
3. `[V]` The APS parser reads the files: `agxps_aps_parser_parse` yields
   per-counter value arrays keyed by hash, plus `systemTimestamps`, kick counts,
   and chunk/parse-error stats. A working prober exists at
   `internal/agxps/counterprobe_manual_test.go` (`TestCounterFileParse`,
   `TestCounterFileFanout`, `TestCounterAggregate`), gated on
   `GPUTRACE_PROBE_COUNTERS`.

So the chain is **lane name → vendorCounter (lookup, `[V]`) → raw hashes
(*disassembly walk, ~150 counters, not a lookup*) → series in `Counters_f_*.raw`
→ timestamped samples**. Links 1 and 3 are verified and link 2 is a plan.

`[V]` Occupancy needs one extra step, already worked out in
`docs/research/OCCUPANCY_MECHANISM.md`:

    Kernel Occupancy (%) = Compute Simdgroups Inflight Per Shader Core / 96 * 100

## 4. Proposed architecture

Five layers. Each is independently testable and independently shippable; do not
collapse them.

### L0 — fix the five empty tracks (independent of everything else)

The encoder→shader-metrics match at `timeline.go:1470-1500` fails for every
encoder on these traces. This is a live correctness bug, not a missing feature,
and it is the cheapest visible win. Diagnose whether `ParsePerfCounters`
returns no `ShaderMetrics`, or returns them under names that never match
`encoder.Label`. Fix, and verify against the four staticmask traces: all five
tracks must appear with 46 samples each.

Do this first. It also proves out the sample-emission path that L4 reuses.

### L1 — lane catalog (`internal/counter/lanes`)

Turn `parity.Catalog` into the authority on what lanes exist. A `Lane` is:

```go
type Lane struct {
    Name           string   // "Kernel Occupancy"
    Unit           string   // "Percentage of Shader Core Resources"
    Group          string   // panel grouping, for Perfetto track groups
    VendorCounters []string // plaintext, from GPUCounterGraph.plist
    Normalize      func([]float64) float64 // e.g. /96*100 for Occupancy
}
```

Beware the documented trap in `catalog.go`: **unit is often not what the
rendered value suggests.** "Compute SIMD Groups Inflight per Core" has unit
"SIMD Groups" — a count — even though Xcode prints it with a percent sign. A
harness that infers percent from the glyph will report a unit disagreement as a
value disagreement.

This layer degrades to nil when Xcode is not installed. Follow `LoadCatalog`'s
existing precedent: no catalog is not an error, it is less enrichment.

### L2 — raw decode (`internal/counter/rawcounters`)

Promote the manual prober into a real package. Input: a `Counters_f_*.raw`
path. Output:

```go
type Series struct {
    Hash   string    // 64-hex, the only identity we have
    Values []float64
}
type Pass struct {
    File       string
    Series     []Series
    SystemTS   []uint64 // per-kick, raw ticks
    Kicks      int
    ChunksFailed, ParseErrs int // carry the parser's own health out
}
```

Open questions this layer must resolve before anything downstream is
trustworthy, each with a decisive check:

- **uarch and flags.** The prober sweeps `uarch` 0..7 × `flags` {1, 0x21}
  because the right combination was never established. Pin it: the correct
  combination is the one that yields `chunksFailed == 0` and `parseErrs == 0`
  across every file in a capture, not merely the one that returns non-null.
  If more than one qualifies, they must agree on values — check that, don't
  assume it.
- **Is the 137-counter set identical across files?** If yes, files are windows
  of one capture and concatenate. If no, they are different instrument
  configurations and must not be merged. Compare hash sets across all files in
  one capture; this is a cheap, decisive test and it gates L4's merge strategy.
- **Tick rate.** The prober converts with `ticks * 1000 / 24 / 1e6`, i.e. an
  assumed 24 MHz. `[?]` This is a guess. It must be validated against a known
  duration before any sample lands on a timeline — see §6.

Surface parser health in the returned struct. A pass with failed chunks that
silently returns short series is exactly the silent-wrongness failure mode this
project keeps hitting.

### L3 — hash binding (`internal/counter/lanes`, data-backed)

The derived→raw table currently exists only as disassembly notes in a research
doc. Lift it into a checked-in data file — `docs/research/derived-to-raw.yaml`,
following the precedent of `agxps-signatures.yaml` — with per-entry provenance:
the address it was read from, the Xcode build, and its confidence marker.

Two things this table is not:
- It is not complete. Only a handful of entries have been read out so far.
  Unbound lanes must be reported as **unavailable**, never as zero. A flat zero
  lane on a timeline reads as a measurement.
- It is not portable across Xcode versions without recheck. Record the build;
  refuse to apply the table to a mismatched one, or apply it and say so loudly.

Extraction of the remaining entries is a scriptable disassembly walk over the
same region, not a manual transcription. Budget it as its own task.

### L4 — aggregation and export

Per lane, per encoder: gather the raw series for its vendor counters, restrict
to the encoder's time window, combine per the lane's rule, normalize, emit a
`CounterTrack`.

Two decisions to make explicitly rather than by default:

- **Sampling density.** Existing tracks emit 2 samples per encoder (start,
  end). The raw files carry thousands of kicks. Emitting per-kick gives Xcode's
  actual resolution and is the point of the exercise; per-encoder averages throw
  away exactly what the lanes are for. Default to per-kick, with a flag to
  downsample.
- **Which timeline view.** Per-kick samples carry real timestamps, so they
  belong on the **wall** view. The busy view is a synthetic concatenation — its
  23 encoder slices are laid out exactly back-to-back with zero gaps `[V]` —
  and pinning real timestamps onto it produces a plausible, wrong picture. If
  counters are emitted onto the busy view at all, they must be resampled onto
  the synthetic axis, and the track name must say so.

Group tracks in Perfetto by the panel groups from L1, so the export reads like
the screenshot rather than as 27 loose tracks.

## 5. Clock alignment — the highest-risk item

Everything downstream is worthless if the counter clock is not tied to the
timeline clock. Known anchors:

- `command_buffer_wall_time_ns` = 2,007,611,416 and the wall view's CB slice
  span = 2,007,611,000. `[V]` These agree exactly, so the wall axis is sound.
- `command_buffer_active_time_ns` = 10,025,536 vs. summed CB durations
  10,018,000 — a 7.5 µs gap, exactly µs-truncation × 24 CBs. `[V]`
- `[?]` `docs/research/PERFCOUNTERS_REFERENCE.md` notes timestamps in
  `Counters_f_*.raw` and `Profiling_f_*.raw` sit around 5.3177e12, suggesting a
  shared clock domain.

The decisive test: parse a capture's counter files, take
`systemTimestamps[last] - systemTimestamps[0]`, convert under the candidate tick
rate, and compare to that capture's `command_buffer_wall_time_ns`. If 24 MHz is
right the span should land within the capture window. If it does not, the tick
rate is wrong and no lane may be exported until it is settled. Run this against
several captures in `~/tmp/gputrace-captures`, not one — a single agreement can
be coincidence at these magnitudes.

Do not ship a lane whose clock has not passed this test.

## 6. Verification

`~/tmp/gputrace-xcode-oracle-20260731` holds 12 Xcode counter-tab TSV exports,
23 encoders × 274 columns, deterministic. This is the oracle and the reason
this project can check itself at all.

Per lane, gate on:

1. **Presence** — the lane appears with a non-degenerate series.
2. **Value** — per-encoder aggregate matches the oracle column within a stated
   tolerance. `internal/parity` already does this comparison; extend it rather
   than writing a second harness.
3. **Variance** — the series actually varies where the oracle varies. There is
   precedent for this exact check: `parity_test.go:159` asserts ALU Utilization
   is a *varying* column, because a constant series that happens to sit near the
   right value passes a mean comparison while being entirely wrong.

A lane failing (2) or (3) ships as unavailable, not as a number.

## 7. Sequencing

| step | deliverable | blocked on |
|---|---|---|
| L0 | 5 existing lanes populate | nothing |
| L2a | tick rate settled (§5) | nothing |
| L2b | `rawcounters` package, uarch/flags pinned, cross-file hash sets compared | L2a |
| L1 | lane catalog with groups and units | nothing |
| L3 | `derived-to-raw.yaml` with provenance | L2b (need hashes present in files to confirm entries) |
| L4 | lanes on the wall timeline | L1, L2b, L3 |
| — | parity gate wired into CI | L4 |

L0, L1 and L2a are independent and can run in parallel. Nothing after L2a
should start until the clock question has an answer.

## 8. What this design does not deliver

- Lanes whose vendor counters are absent from the derived→raw table. These
  report unavailable until the table is extended.
- Any lane on the busy view at true resolution — see §4/L4.
- Fragment and vertex lanes. The screenshot's panel is filtered to compute;
  `GPUCounterGraph.plist` carries fragment/vertex variants of most of these
  (`Fragment Simdgroups Inflight Per Shader Core`, etc.). Same machinery, but
  the compute set is the deliverable here.
- Deobfuscated raw counter names. That is closed permanently (§2); the design
  routes around it rather than through it.

## 9. Unrelated defects found while measuring

Not part of this design, but observed in the same traces and worth separate
fixes:

- Counter track names are doubled in the export: `"GPU Cycles GPU Cycles"`,
  `"Execution Cost Execution Cost"` — all 10.
- One encoder is labelled with a GUID,
  `A69D645F-FEA0-31AD-BE67-DA0EA602A6C8` (546 µs), where the other 22 carry
  kernel names.
- `CB#22` has duration 0 while every other CB is 33 µs–1.61 ms.
- 117 dispatches / 1.77 ms (8% of dispatch time) are unattributed to any
  encoder in the nested view.
- `dispatch_span_ns` (21.9 ms) is 2.2× `command_buffer_active_time_ns`
  (10.03 ms). Both are correct measurements of different quantities, but the
  field name invites reading the busy extent as GPU time.

## 10. The primary route: let Apple compute the derived counters

**This section supersedes §3 as the plan of record.** It is numbered 10 only
because it was written later; read it before §3, which is now the fallback.

Added 2026-08-01 after an independent re-measurement. Two parts: a
corroboration of §1, and three routes §3 does not consider.

### 10.1 §1 reproduced independently

Exported `~/tmp/gputrace-captures/qwen25-05b-static_tokens_2_to_3-wperfdata.gputrace`
with `gputrace timeline --format chrome` and counted `ph:"C"` events. `[V]` Ten
series, same split as §1: GPU Cycles and Execution Cost measured, the other
eight static. None of the lanes. Different capture, different query path, same
answer — §1 is sound and can be built on.

`generateCounterTracks` is shared by the chrome and perfetto writers
(`timeline.go:1234`, formats branch at `:344`), so this is one gap, not two.

### 10.2 The premise §3 does not state

§2 establishes that raw hashes cannot be named. The sentence immediately after
that finding in `COUNTER_NAME_MAPPING.md` is the one that matters here:

> Xcode does not deobfuscate; it *computes*, via
> `agxps_counter_compute_derived_counters`.

`[V]` The lane arithmetic is compiled into the framework. `GPUCounterGraph.plist`
carries no formula, operand, or operator key — it is a display catalog. So there
are two ways to get a lane value: reconstruct Apple's arithmetic from
disassembly (§3, 150 derived counters to walk), or call the code that already
implements it. §3 chose the first without recording that the second exists.

Three entry points, each needing one throwaway spike before it is costed.
**Attempt them in the order A, C, B.** B is listed second below for continuity
with the argument, but it is ranked last: its blocker is a reversing project
with an unknown floor, not a spike (see the note at the end of B).

**A. `XRGPUAPSDataProcessor`.** `docs/research/GTShaderProfiler_BINDING_GAPS.md`
records that it "exposes APS/RDE raw and derived counter processing, including
shader loading, counter loading, timestamp conversion, and raw **or derived**
counter buffer accessors." A derived-counter buffer is the output §3 spends L1
through L4 reconstructing. Unknown: whether it constructs from an archived
capture or only from a live replay session. Spike: enumerate selectors with
`gputrace xcode-bindings --json`, then attempt construction against a
`.gpuprofiler_raw` directory and read one derived buffer.

**B. `GTMioCounterData`.** Exposes `name, index, sampleCount, sampleInterval,
scope, timestamps, values` — per-lane time series, named, already sampled. This
is the shape §4/L4 builds by hand, and the only route that supplies per-sample
timestamps without solving §5 first.

`[V]` Blocked upstream, and the blocker is documented:
`-[GTMioTraceData initWithStreamData:llvmHelperPath:options:]` returns a real
object for every option value 0–15 with every count zero and empty
`timelineCounters` (`GTMIO_CAPABILITY_MATRIX.md`). `fcc72a1` got further — the
GTAGX2 processors need an inflated `GTShaderProfilerStreamData` rather than the
bytes, which fixed the `unrecognized selector` crash and yielded device, plugin,
24 command buffers and 23 encoders — but the timeline payloads still come back
nil because the accessors past that point return C++ result structs with no
Objective-C selector to unpack them. Research, not a task with an estimate.

**Ranked last for that reason.** Unpacking C++ result structs with no selector
is open-ended reversing; A and C are bounded probes. Do not start B until both
have failed, and if it is started, timebox it explicitly.

**C. `agxps_counter_compute_derived_counters`.** The same computation as a C
entry point, reachable through `internal/agxps` if the signature can be
established. Spike: disassemble and add an entry to
`docs/research/agxps-signatures.yaml` with a `verified:` field. Do not infer the
signature from the name — that yaml's preamble exists because name-derived
guesses were wrong about half the time, and a wrong bulk-copy shape here writes
through a garbage pointer and reports success.

Name the specific hazard: a function called `compute_derived_counters` almost
certainly takes a caller-allocated **output buffer**. Guessing its shape wrong
is a memory-corruption bug that *returns success*, not a wrong number — the
failure is invisible at the call site and may not surface until unrelated code
crashes. Gate the first probe so it only reads: establish the signature and the
buffer size from disassembly before passing any pointer the callee writes to.

### 10.3 What this changes in the sequencing

Nothing before L2b. The raw-decode layer is the substrate every route consumes
and the only place to test a candidate derivation, so build it regardless.

What it changes is L3. Before committing to a scripted disassembly walk over 150
derived counters, run the three spikes. **Split the target by route** — the
right probe differs depending on whether the route needs a binding:

- **Routes A and C return a named lane**, so the check is an oracle number:
  use **ALU Utilization**, which the oracle pins at `1.59%` for encoder 0. Score
  on one question — does it return a plaintext lane name with a value, and does
  that value match.
- **Anything that needs a raw-hash binding cannot use ALU Utilization**, because
  ALU Utilization has no binding (§11.4). Use **`Fragment Shader Launch
  Utilization`**: bound with 6 hashes, and oracle column 36 reads 15 nonzero of
  23 encoders, max `0.79%`, mean `0.126%` `[V]`. It is a fragment counter
  firing incidentally on a compute workload, so the values are small — but they
  vary per encoder, and a binding spike needs variation, not magnitude.

If A or C answers yes, L3 and most of L4's combination logic are unnecessary; if
neither does, §3's route is the fallback and has lost only the spike time.

### 10.4 A correction to the verification target

§6 describes the oracle as "23 encoders × 274 columns." `[V]` The per-encoder
export at `~/tmp/gputrace-xcode-oracle-20260731/compute-kernel-encoders.txt` is
23 encoders × **39** columns; 274 is the count of vendorCounter identifiers that
do *not* resolve, from a different finding. The 39 are the target set, and they
are finer-grained than the screenshot's rail — Xcode's single "F32" lane is two
columns, `F32 Limiter` and `F32 Utilization`:

    Execution Cost, Occupancy Manager Target,
    Instruction Throughput {Limiter,Utilization}, ALU Utilization,
    F32 {Limiter,Utilization}, F16 {Limiter,Utilization},
    Integer and Complex {Limiter,Utilization},
    Integer and Conditional {Limiter,Utilization},
    Texture Read {Limiter,Utilization}, Texture Write {Limiter,Utilization},
    MMU {Limiter,Utilization}, Last Level Cache {Limiter,Utilization},
    Partial Render Count, Shaded Vertex Read Limiter, Cull Unit Limiter,
    Clip Unit Limiter, {Register,Other} L1 {Read,Write} Accesses,
    Control Flow {Limiter,Utilization},
    {Compute,Fragment,Vertex} Shader Launch {Limiter,Utilization}

Encoder 0 reads `Execution Cost 3.520%`, `Occupancy Manager Target 100.00%`,
`Instruction Throughput Limiter 12.77%`, `ALU Utilization 1.59%`.

Which of a `{Limiter, Utilization}` pair the rail draws under the bare name is
open. Resolve it by reading a value out of the UI, not by picking the plausible
one.

Note also that the sibling files — `xcode-memory.txt`,
`xcode-performance-limiters.txt`, `xcode-textures.txt` and the rest — are
separate panel exports, so the full oracle is wider than the encoder table
alone. Phase 1 should fixture all of them, not just the one.

### 10.5 Persistence

**Done for this file** — it now lives at `docs/research/COUNTER_LANES_DESIGN.md`
(moved 2026-08-01 from `/tmp/gputrace-counter-lanes-design.md`, which macOS
sweeps).

Still outstanding, and the more important half: the artifacts this design
depends on are all under `~/tmp` and none is checked in —

- `~/tmp/gputrace-xcode-oracle-20260731/` — the 12 panel exports, the entire
  verification story of §6 and §11.3
- `~/tmp/gputrace-derived-counter-raw-inputs.json` — the five derived→raw
  bindings, the *only* machine-readable binding that exists (§11.4)
- `~/tmp/gputrace-counter-hash-inventory.csv` — the 141-row unnamed inventory

The project has already lost one finding to a loose file under `~/tmp` written
by a session that is no longer running (`fcc72a1`). The second item above is a
1.9 KB file on which the entire delivery order now turns; it should be checked
in with provenance before anything is built on it.

## 11. The lane tree: what actually has to be produced

Transcribed from the Counters rail fully expanded (Xcode 26, compute capture,
2026-08-01). `[V]` as a record of what the UI draws; the exact plaintext of
truncated labels is `[?]` and marked inline.

This supersedes the "~27 lanes" framing used in the Scope line and in §4/L4.
**27 is the collapsed rail: 23 groups + 4 standalone lanes. Expanded, the tree
carries 84 leaf series.** Any estimate built on 27 is low by 3×.

### 11.1 The tree

Group header line is `Group — unit`; children are the leaves. `†` marks a group
whose header repeats one of its own children by name (see §11.2).

```
Active Cores † — % GPU Cores
├── Active Cores
└── RT Unit Active

Occupancy — % Shader Core Resources
├── VS Occupancy
├── FS Occupancy
├── Kernel Occupancy
└── Total Occupancy

Occupancy Manager — % Shader Core Resources
├── Occupancy Manager Target       (% Shader Core Resources)
└── L1 Eviction Rate               (Measure of L1 Cache Line Evictions)

Instruction Throughput — % Peak Instruction Throughput Performance [?] truncated
├── Instruction Throughput Limiter
└── Instruction Throughput Utilization

Shader Launch Limiter — % Peak Performance
├── Vertex Shader Launch Limiter
├── Fragment Shader Launch Limiter
└── Compute Shader Launch Limiter

Bandwidth — GiB/Second
├── GPU Bandwidth
├── GPU Read Bandwidth
└── GPU Write Bandwidth

ALU Utilization — % Peak ALU Performance                      [standalone leaf]

F32 — % Peak F32 Performance
├── F32 Limiter
└── F32 Utilization

F16 — % Peak F16 Performance
├── F16 Limiter
└── F16 Utilization

Integer and Conditional — % Peak Integer and Conditional Performance
├── Integer and Conditional Limiter
└── Integer and Conditional Utilization

Control Flow — % Peak Control Flow Performance
├── Control Flow Limiter
└── Control Flow Utilization

Integer and Complex — % Peak Integer and Complex Performance
├── Integer and Complex Limiter
└── Integer and Complex Utilization

Texture Read — % Peak Texture Read Performance
├── Texture Read Limiter
└── Texture Read Utilization

Texture Filtering Limiter — % Peak Texture Filtering Performance  [standalone]

Texture Read Cache Limiter † — % Peak Texture Read Cache Performance
├── Texture Read Cache Limiter
└── Texture Read Cache Utilization

Compression Ratio … Texture Memory Read [?] — Ratio of compressed to
                                              uncompressed memory [?]  [standalone]

Texture Write — % Peak Texture Write Performance
├── Texture Write Limiter
└── Texture Write Utilization

MMU — % Peak MMU Performance
├── MMU Limiter
└── MMU Utilization

Last Level Cache — % Peak Cache Performance
├── Last Level Cache Limiter
└── Last Level Cache Utilization

L1 Cache — % Peak L1 Performance
├── L1 Cache Limiter
└── L1 Cache Utilization

L1 Residency — % L1 Capacity
├── L1 Total Residency
├── L1 Buffer Residency
├── L1 Imageblock Residency
├── L1 RT Scratch Residency
├── L1 Register Residency
├── L1 Stack Residency
├── L1 Threadgroup Residency
└── L1 Other Residency

L1 Read Bandwidth † — GiB/s
├── L1 Read Bandwidth
├── Threadgroup Memory L1 Read Bandwidth [?] truncated
├── Imageblock L1 Read Bandwidth
├── Unclassified L1 Read Bandwidth
├── Register L1 Read Bandwidth
├── Stack L1 Read Bandwidth
├── Buffer L1 Read Bandwidth
└── RT Scratch L1 Read Bandwidth

L1 Write Bandwidth † — GiB/s
├── L1 Write Bandwidth
├── Threadgroup Memory L1 Write Bandwidth [?] truncated
├── Imageblock L1 Write Bandwidth
├── Unclassified L1 Write Bandwidth
├── Register L1 Write Bandwidth
├── Stack L1 Write Bandwidth
├── Buffer L1 Write Bandwidth
└── RT Scratch L1 Write Bandwidth

L1 Cache Reads — % Total L1 Read Accesses
├── Buffer L1 Read Accesses
├── ThreadGroup L1 Read Accesses
├── ImageBlock L1 Read Accesses
├── Stack L1 Read Accesses
├── Register L1 Read Accesses
├── RT Scratch L1 Read Accesses
└── Other L1 Read Accesses

L1 Cache Writes — % Total L1 Write Accesses
├── Buffer L1 Write Accesses
├── ThreadGroup L1 Write Accesses
├── ImageBlock L1 Write Accesses
├── Stack L1 Write Accesses
├── RT Scratch L1 Write Accesses
├── Register L1 Write Accesses
└── Other L1 Write Accesses

Last Level Cache Bandwidth — GB/s                             [standalone leaf]

SIMD Groups Inflight per Core — SIMD Groups
├── Total SIMD Groups Inflight
├── Vertex SIMD Groups Inflight
├── Fragment SIMD Groups Inflight
└── Compute SIMD Groups Inflight
```

Tally: 23 groups + 4 standalone = **27 collapsed rows**; 80 children + 4
standalone = **84 leaf series**.

Transcribed twice from the same screenshots by two sessions independently, with
identical structure and tally. That is agreement between two readings of one
source, not corroboration from a second source — it rules out transcription slips,
not a misread UI.

**Several names have a second, non-screenshot source.** The CSV column list in
`docs/research/COUNTER_FILE_MAPPING.md` came from an Xcode *export*, so where it
agrees the plaintext is settled rather than `[?]`:

| Label | Status |
|---|---|
| Compression Ratio of Texture Memory Read | `[V]` exact, incl. the sibling `…Written` |
| Buffer L1 {Read,Write} Bandwidth, Buffer L1 {Read,Write} Accesses | `[V]` |
| Control Flow {Limiter,Utilization} | `[V]` |
| Compute Shader Launch {Limiter,Utilization} | `[V]` |
| Clip Unit Limiter, Cull Unit Limiter | `[V]` — present in the oracle, absent from the rail |
| Buffer L1 Miss Rate | `[V]` in the CSV, in no rail group |

This retires the `Compression Ratio …` entry from §11.2's "worst case" list: the
name is `Compression Ratio of Texture Memory Read`. Its *unit* is still elided,
and since it is a ratio that remains the more dangerous half. The remaining
truncations — the two `Threadgroup Memory L1 * Bandwidth` and the group unit
strings — have no second source and stay `[?]` until read from the plist.

### 11.2 What the tree tells us that the collapsed rail did not

**The group header is itself a drawn lane, and its relationship to its children
is not uniform.** Four groups (`†`) repeat a child's name exactly — the "Active
Cores" group contains a child "Active Cores". Others do not: the "Occupancy"
group has no child called "Occupancy", and "L1 Cache Reads" has no child of that
name. So the header is sometimes a self-named member and sometimes an aggregate
or a picker. This generalizes §10.4's open question about which of a
`{Limiter, Utilization}` pair the bare name draws — the answer differs per group
and must be read out of the UI per group, not inferred. Until it is, emit the
leaves and omit the header rather than guessing an aggregation.

**Casing is inconsistent and must be preserved verbatim.** `L1 Threadgroup
Residency` vs `ThreadGroup L1 Read Accesses`; `Imageblock L1 Read Bandwidth` vs
`ImageBlock L1 Read Accesses`. These are Apple's strings. A lookup that
normalizes case will silently miss, and a lookup that silently misses reports a
lane as unavailable when it is merely misspelled. Key on the plist's exact
bytes.

**One unit is genuinely different, not a typo.** Every bandwidth lane is `GiB/s`
except `Last Level Cache Bandwidth`, which is `GB/s`. Binary vs decimal — a 7.4%
difference. Carry the unit per lane from the catalog; do not infer it from the
group or normalize the family.

**The unit is not inheritable from the group either**, which is a stronger claim
than the one above and independently checkable in the tree:

- `F32 Limiter` is `% Peak Performance` while its parent and its sibling
  `F32 Utilization` are `% Peak F32 Performance`. Same for F16, and for
  Instruction Throughput.
- `L1 Eviction Rate` has unit "Measure of L1 Cache Line Evictions" — i.e. none —
  under a group whose unit is `% Shader Core Resources`.

So the `Lane` struct in §4/L1 must carry `Unit` per leaf and treat the group's
unit as a display string only. A schema that hangs one unit off the group is
wrong for at least seven leaves.

**The groups come in three shapes, and only one of them needs an oracle.**

1. *Eponymous parent* (4): Active Cores, Texture Read Cache Limiter, L1 Read
   Bandwidth, L1 Write Bandwidth — the `†` set above. The collapsed rail is
   showing that child; nothing to derive.
2. *Limiter/Utilization pair* (11): Instruction Throughput, F32, F16, Integer and
   Conditional, Integer and Complex, Control Flow, Texture Read, Texture Write,
   MMU, Last Level Cache, L1 Cache. Neither child bears the bare group name, so
   the header renders something absent from the tree.
3. *Decomposition* (8): by shader stage (Occupancy, SIMD Groups Inflight) or by
   resource class (Bandwidth, L1 Residency, L1 Read Bandwidth, L1 Write
   Bandwidth, L1 Cache Reads, L1 Cache Writes).

   Shader Launch Limiter looks like this shape and is **not** — its three
   children partition nothing. There is no total, no 100% identity, and nothing
   to check them against. Shape 3 requires that the children partition a whole
   *and the whole is present or implied*.

Shape 3 supplies checks the tree can run on itself with no oracle involved:
`L1 Total Residency` against the other seven; `Total SIMD Groups Inflight`
against Vertex + Fragment + Compute; `GPU Bandwidth` against Read + Write;
`L1 Read Bandwidth` against its seven category children; `L1 Cache Reads`
children summing to 100%.

That covers 45 of the 55 leaves §11.3 previously called unverifiable — but
**these checks are necessary, not sufficient, and two of them are worthless.**
§11.3 now splits the tree three ways on exactly this distinction; read the "why
tier 2 cannot promote a leaf to verified" note there before relying on any of
them.

Shape 2 offers a weaker check still — Limiter and Utilization should be ordered
consistently — worth asserting once the direction is read out of one known pair
rather than assumed.

**Four labels are truncated in the UI** and are marked `[?]` above. Resolve them
from `GPUCounterGraph.plist`, never from the screenshot. The `Compression
Ratio…` lane is the worst case: both its name and its unit are elided, and it is
a *ratio*, so a wrong unit assumption changes the value's meaning rather than
its scale.

**Most of the tree is not compute.** VS/FS Occupancy, Vertex/Fragment Shader
Launch Limiter, Vertex/Fragment SIMD Groups Inflight, RT Unit Active, RT Scratch
(×4), Imageblock (×4), Texture Filtering/Read Cache/Write, Compression Ratio —
these read legitimately **zero** on a compute-only capture. This collides
head-on with §6's variance gate: a constant-zero series is correct here and
must not be failed as degenerate. The gate needs a per-lane expectation
(compute-relevant vs not), or it will reject ~30 correct lanes. Zero from "this
workload does not use that unit" and zero from "we failed to bind the counter"
must be distinguishable in the output — the first is a measurement, the second
is `unavailable`.

### 11.3 Oracle coverage is thinner than the tree

Against §10.4's 39-column per-encoder export, the tree divides three ways. The
middle tier is the one to read carefully: it is **necessary, not sufficient**.

**Tier 1 — oracle-verified (29 leaves).** A ground-truth number per encoder.

| | leaves |
|---|---|
| Instruction Throughput, F32, F16, Int&Cond, Int&Complex, Control Flow, Texture Read, Texture Write, MMU, Last Level Cache — `{Limiter, Utilization}` both | 20 |
| ALU Utilization, Occupancy Manager Target | 2 |
| Shader Launch Limiter (oracle also carries the Utilization halves the rail omits) | 3 |
| Register/Other L1 {Read,Write} Accesses — 4 of the 14 L1-access leaves | 4 |

**Tier 2 — arithmetic-consistent only (45 leaves).** Shape-3 groups whose
children partition a present or implied whole, per §11.2.

| group | leaves in this tier | identity | strength |
|---|---|---|---|
| SIMD Groups Inflight | 4 | Total = V+F+C, **and** §2's `Kernel Occupancy = Compute Inflight / 96 * 100` | **strong** — the only *cross-group* tie |
| Occupancy | 4 | Total vs VS+FS+Kernel `[?]` — not established that Total is the sum | moderate |
| Bandwidth | 3 | GPU = Read + Write | moderate |
| L1 Residency | 8 | Total vs other 7 | weak |
| L1 Read Bandwidth | 8 | header child vs 7 categories | weak |
| L1 Write Bandwidth | 8 | header child vs 7 categories | weak |
| L1 Cache Reads | 5 | children sum to 100% | **zero information** |
| L1 Cache Writes | 5 | children sum to 100% | **zero information** |

**Tier 3 — neither (~10 leaves).** Active Cores ×2, L1 Cache
{Limiter,Utilization}, Texture Filtering Limiter, Texture Read Cache ×2,
Compression Ratio, LLC Bandwidth, L1 Eviction Rate. No oracle column and no
sibling identity. These need a binding *and* an independent check invented for
them.

#### Why tier 2 cannot promote a leaf to verified

A partition identity checks **arithmetic, not attribution.** If all eight
`L1 * Residency` leaves are bound to the wrong raw hashes but bound *permutably*
— Buffer's hash on Stack, Stack's on Register, and so on — the sum still equals
`L1 Total Residency` and the check passes on a completely wrong lane set. Seven
anonymous sibling hashes with no names and no distinguishing units is precisely
the situation where a permuted binding is the *likely* error, not an exotic one.

Some of these identities may be unfalsifiable by construction. If Xcode defines
`L1 Total Residency` as the sum of its categories, then any binding we produce
sums to it *by definition* and the check cannot fail for any input.
`COUNTER_NAME_MAPPING.md` has the sentence for this: **"A check that cannot fail
is not a check."** The falsified `Counters_f_N → column N-4` mapping survived
months on exactly this defect — a 36-entry table lined up with 36 columns, which
any 36-entry ordering satisfies.

The two marked *zero information* are the clearest case: their unit is "% Total
L1 Read/Write Accesses," so summing to 100% is guaranteed by normalization
regardless of which hash went where.

So tier 2 catches dropped, duplicated and mis-scaled series — real value, and
available before any oracle work lands. It cannot establish that a lane is the
lane it claims to be. **A leaf shipping on a tier-2 check alone must say so in
the export**, and tier 2 is not a substitute for extending the oracle.

Two further consequences:

- The set relationship runs both ways — the oracle also carries `Partial Render
  Count`, `Shaded Vertex Read Limiter`, `Cull Unit Limiter`, `Clip Unit Limiter`,
  which appear in *no* rail group (the panel is filtered to compute). Neither set
  contains the other; do not treat the oracle as the tree's definition.
- §10.4's closing note is now load-bearing rather than a nicety: the sibling
  panel exports (`xcode-memory.txt`, `xcode-performance-limiters.txt`,
  `xcode-textures.txt`, …) are where tiers 2 and 3 ground truth lives — the
  only way to promote those 55 leaves out of "arithmetic-consistent" and into
  "verified." Fixture all of them in phase 1, and build the lane→oracle-column
  index as a checked-in artifact. Otherwise two thirds of the work ships on
  checks that cannot fail, which for this project is the same as shipping it
  wrong.

### 11.4 Delivery order over the tree

#### First, the finding that constrains the order

`[V]` `~/tmp/gputrace-derived-counter-raw-inputs.json` — the artifact
`COUNTER_NAME_MAPPING.md` leaves behind, and the only machine-readable
derived→raw binding that exists — contains **five** entries:

    Vertex Shader Launch Utilization              (6 hashes)
    Fragment Shader Launch Utilization            (6 hashes)
    Instruction Issue Utilization                 (7 hashes)
    Vertex Simdgroups Inflight Per Shader Core    (2 hashes)
    Fragment Simdgroups Inflight Per Shader Core  (3 hashes)

`Compute Simdgroups Inflight Per Shader Core` **is not among them.** What exists
for it is an 8-hex *prefix* triple in the prose of `COUNTER_NAME_MAPPING.md`
(`33634F0D, FD6F91B4, 50E7E1AA`) — not full 64-hex ids, and not in the
artifact. An earlier revision of this section claimed `[V]` that those hashes
were "already read out, making this the only group whose binding exists today."
That was overstated and is withdrawn.

Cross the three sets — full hashes known, oracle column exists, non-degenerate
on a compute capture. `[V]` measured against
`~/tmp/gputrace-xcode-oracle-20260731/compute-kernel-encoders.txt`:

- `Fragment Shader Launch Utilization` is bound (6 hashes) **and** is oracle
  column 36, which is *not* dead on this capture: 23 rows, 15 nonzero, max
  `0.79%`, mean `0.126%`. Sub-1% and it is a fragment counter firing
  incidentally on a compute workload — but it **varies across encoders**, which
  is all a binding test requires.
- `Instruction Issue Utilization` is bound and compute-relevant, but appears in
  **no** oracle column and maps to no lane in the §11.1 tree. It can prove the
  machinery runs; it cannot prove a number.
- `Vertex Shader Launch Utilization` is bound and is oracle column 39.
- The two `Simdgroups Inflight` bindings are Vertex and Fragment.

Two different claims follow, and they must not be conflated:

1. **Exactly one bound counter has a live, varying oracle value today** —
   `Fragment Shader Launch Utilization`. That is enough to test a binding
   end-to-end against ground truth, and it is therefore the binding-route spike
   target.
2. **Zero lanes *in the §11.1 tree* are both bound and oracle-verifiable.** The
   rail's Shader Launch group carries only the three `… Limiter` children;
   `Fragment Shader Launch Utilization` is an oracle column, not a rail lane. So
   nothing deliverable can be produced and checked end-to-end today.

The first says the pipeline can be proved. The second says no lane can be
shipped. Both are true, and an earlier revision of this section collapsed them
into a flat "zero lanes," which understated what is testable.

#### The artifact's gap is systematically compute-shaped

`[V]` Three times now the *compute* sibling is the missing one:

| counter | vertex | fragment | compute |
|---|---|---|---|
| Simdgroups Inflight Per Shader Core | in json | in json | **absent** (prose only) |
| Shader Launch Utilization | in json | in json | **absent** |

and `Compute Shader Launch Utilization` is oracle column 34 with *better* data
than its fragment sibling — 21 of 23 nonzero, max `0.74%`, mean `0.204%`.

Two vertex/fragment pairs whose compute member is absent is not coincidence.
The extraction run that produced this artifact was almost certainly partial or
stage-filtered, which is what makes step 0 cheap: the compute entries are not
undiscovered, they are unextracted.

#### The order

Do not attempt 84 lanes at once.

0. **Extend the derived→raw extraction to the compute side** — at minimum
   `Compute Simdgroups Inflight Per Shader Core` at full 64-hex width. It is the
   one counter whose lane has a *cross-group* check (§2's `/96*100` tie to
   Kernel Occupancy), which §11.3 rates the only strong tier-2 identity.

   **This is extraction, not discovery, and the distinction is the whole cost.**
   `[V]` `COUNTER_NAME_MAPPING.md` already carries the binding —
   `33634F0D, FD6F91B4, 50E7E1AA` at raw ordinals 483–485, read out of the
   instruction stream at `0x54d9d0-0x54e0c0` and corroborated at
   `0x4f478c-0x4f4818`. What is missing is only the full 64-hex width and a
   machine-readable home for it; the prose carries 8-hex prefixes. Combined with
   the stage-shaped gap above, step 0 is a re-run of an extraction that already
   worked, not a fresh reversing task. Budget it accordingly.

   Prove the pipeline in parallel on **`Fragment Shader Launch Utilization`** —
   bound, and oracle column 36 with a varying sub-1% value. It is the only
   binding that can be checked against ground truth today. (An earlier revision
   named `Instruction Issue Utilization` here; that was wrong — it has no oracle
   column, so it tests only that the machinery runs.)

   **Acceptance test — state it before running, so it can fail.** The
   stage-filter theory makes a falsifiable prediction. If it holds, re-running
   the extraction with the filter removed yields:

   | counter | predicted hashes | basis |
   |---|---|---|
   | `Compute Shader Launch Utilization` | 6 | matches both sibling widths |
   | `Compute Simdgroups Inflight Per Shader Core` | 3 | matches Fragment's 3; Vertex has 2 |

   and the second must come back as `33634F0D…`, `FD6F91B4…`, `50E7E1AA…` at
   full 64-hex width — because `COUNTER_NAME_MAPPING.md` already read those out
   of the instruction stream at ordinals 483–485.

   **If the re-run returns three hashes for `Compute Simdgroups Inflight` that
   are not those three, the extraction is wrong somewhere else and nothing
   downstream may trust it until that is explained.** This is the point of
   writing the prediction down: the alternative is reading whatever comes back
   as confirmation, which is how the falsified `Counters_f_N → column N-4`
   mapping survived (§2). A disagreement here is a finding, not a nuisance.

1. **The 29 tier-1 leaves** with `{Limiter, Utilization}` structure. One
   arithmetic shape repeated, and every one has a number to check against. This
   is the coverage win.
2. **SIMD Groups Inflight (4) + Occupancy (4)** — unblocked by step 0, and the
   strongest tier-2 identity once it is.
3. **The L1 family (37 leaves)** — Residency 8, Read Bw 8, Write Bw 8, Reads 7,
   Writes 7 minus overlap. Largest block, most uniform shape, and the category
   axis (Buffer/Threadgroup/Imageblock/Stack/Register/RT Scratch/Other) almost
   certainly maps to one raw counter per category, so binding one category
   likely yields all of them. Note the permutation hazard in §11.3 — this is the
   block where a self-consistent wrong binding is most likely.
4. **Tier 3** — Active Cores, texture lanes, Compression Ratio, LLC Bandwidth,
   L1 Eviction Rate. Heterogeneous, individually bound, no check yet invented.
   Lowest ratio of value to effort.

If §10's route A or C lands, steps 0 and 3's binding work mostly evaporate —
which is the strongest argument for spiking those before committing here.
