# Xcode Counters-tab oracle

Ground-truth per-encoder counter values exported by Xcode itself, used by
`internal/parity` to measure how much of Xcode's counter surface gputrace
reproduces. Nothing here is decoded by us; every number came out of Xcode.

## Capture

    trace     qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace
              (profiler-only: .gpuprofiler_raw, no unsorted-capture)
    workload  MLX Qwen2.5-0.5B, static mask, warm, tokens 2-4, repetition 1
    host      Apple M4 Max, macOS 26.6
    exporter  Xcode 26.3 (17C529), GPU trace -> Counters tab
    exported  2026-07-31

`ALU Utilization` for the first and tenth encoders (1.59%, 2.70%) was checked
against Xcode's live inspector UI and agrees with the export, so the exports
render what the UI shows.

The `.gputrace` bundle itself is ~17 GB and is not in the repository.

## Ground truth reported by Xcode for this capture

    23 encoders, 958 dispatches, 24 command buffers, 18 pipelines,
    9.16 ms effective GPU time, "Sampled Cores 21/40" (warning icon)

## Files

Two independent exports of the same Counters tab. Neither is a superset of the
other, so `internal/parity` merges them: the union is 234 distinct columns, 91
of them populated.

### xcode-counters-export.csv

Xcode's whole-tab CSV export, already joined: 247 columns x 23 encoder rows.
Exported as `qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2 Counters.csv`
(the original name contains a space); renamed here. Columns 1-5 are `Index`,
`Encoder FunctionIndex`, `CommandBuffer Label`, `Encoder Label`, and a blank,
leaving 242 metric columns under 226 distinct names, 86 of them populated.

This is the perfdata2 bundle rather than perfdata3. The two captures' inputs --
`streamData`, `Profiling_f_*`, `Counters_f_*` -- are md5-identical, and both
exports agree on all 23 encoder keys, so they describe one capture.

Twenty-nine columns here appear in no sub-tab export, all of them fragment
shader counters (`FS *`, `Samples Shaded Per Tile`, ...), all zero for this
compute workload.

**Sixteen column names appear twice** in the header: Depth Load Utilization,
Depth Test Utilization, Depth Texture Device Memory Bytes Read/Written, L1 RT
Scratch Residency, Occupancy Manager Target, Other L1 Read/Write Accesses,
Register L1 Read/Write Accesses, Texture Cache Miss Rate, Texture Device Memory
Bytes Read/Written, Texture L1 Bytes Read, ThreadGroup L1 Write Accesses,
Threadgroup Memory L1 Write Bandwidth. Every pair is byte-identical in all 23
rows, so it is an export quirk and not two counters sharing a name. A reader
keying on the header name silently keeps the last occurrence; `LoadCountersCSV`
checks the pairs and fails rather than choosing one, and `TestRepeatedHeaders\
AreIdentical` pins the count at 16.

### xcode-*.txt, compute-kernel*.txt

Tab-separated, one file per Counters sub-tab. Row 1 is the header; rows 2..24
are the 23 compute encoders in execution order. Column 1 is `Thumbnails`
(empty or `-`), column 2 is `Name`.

Eight columns appear only here, and one of them matters: **`Execution Cost`**,
the leading column of every sub-tab, is absent from the CSV export. The other
seven are `Primitives Culled` and six `* Bandwidth` columns.

    xcode-memory.txt                 79 metric columns
    xcode-textures.txt               47
    xcode-performance-limiters.txt   39
    compute-kernel-encoders.txt      39   re-export of performance-limiters
    xcode-vertex-shaders.txt         24
    xcode-pre-fragment-stage.txt     23
    xcode-primitives.txt             22
    xcode-post-fragment-stage.txt    16
    xcode-compute-kernels.txt        12
    compute-kernel.txt               11   re-export of compute-kernels
    xcode-ray-tracing.txt             6
    xcode-vertices.txt                6

All tabs carry the same `Name` column, so they join into one table of 23 rows
keyed by encoder name.

## Determinism

`compute-kernel-encoders.txt` and `xcode-performance-limiters.txt` are two
separate exports of the same sub-tab of the same capture. They agree on every
metric cell; they differ only in the `Thumbnails` column (`-` vs empty) and in
the trailing tab. `compute-kernel.txt` / `xcode-compute-kernels.txt` are the
same pair for the Compute Kernel sub-tab. Xcode's export is deterministic, so a
mismatch against these files is a real difference and not export noise.

## The oracle is not gospel per cell

Verified holes in Xcode's own output — treat a disagreement in these columns as
uninformative rather than as a gputrace bug. `internal/parity` reports them as
`ORACLE SUSPECT`:

  - `Kernel Invocations` is 0 for the encoders named `6329 …` and `10974 …`
    while those same rows report non-zero `Execution Cost` (4.533%, 9.740%),
    non-zero `ALU Utilization` (1.39%, 2.70%), and have real dispatches. Both
    exports reproduce the hole identically, so it is Xcode's, not the export's.
  - `Kernel Texture Cache Miss Rate` is 0.00% in all 23 rows: no information.
  - `Kernel ALU Performance` is byte-identical to `Kernel ALU Instructions` in
    all 23 rows — a raw instruction count printed under a performance label.

Also note: `Kernel Invocations` is not the dispatch count. It ranges 32..44,951
per encoder against 958 dispatches total for the whole capture, so it counts
threads or threadgroups, not `dispatchThreadgroups` calls.

## Join key

The encoder `Name` in the sub-tab exports is `<n> Compute Encoder <i> 0x<addr>`,
e.g. `546 Compute Encoder 0 0x79f00c8c0`. The leading `<n>` is the join key.

Confirmed from three directions:

  - it equals `end_offset_micros` from `encoderInfoData` in `streamData` for all
    23 encoders, in order;
  - Xcode names it outright in the CSV export, as `Encoder FunctionIndex`, and
    the CSV's `Encoder Label` is exactly the remainder of the sub-tab `Name`;
  - both exports list the same 23 keys in the same order.

`0x<addr>` is not unique on its own (`0x7a0834280` appears twice), so it cannot
be the key.

## Precision

The CSV export rounds every value to two decimals; the sub-tab exports carry up
to four, and render byte counters with a unit (`2.21 MiB` where the CSV says
`2312832.00`). Eleven shared columns therefore differ in rendering -- all of
them bandwidths -- while holding the same numbers. `Merge` keeps the finer
rendering and reports anything that differs by more than the coarser export's
last place. No column is all-zero in one export and populated in the other.

## The exports are not everything Xcode measures

`GPUCounterGraph.plist` defines 455 counters; these exports carry 234. Xcode's
Timeline shows `SIMD Groups Inflight per Core` under its Occupancy filter, and
no export column carries it -- grepping the merged header for `SIMD`,
`Inflight` or `Active Core` returns nothing, which `TestNoSIMDInflightColumn`
pins. Read a NOT PRODUCED count as measured against what Xcode *exports*, not
against what it can measure.

## Graphics tabs

This is a pure compute workload. `xcode-vertices.txt`, `xcode-primitives.txt`,
`xcode-vertex-shaders.txt`, `xcode-pre-fragment-stage.txt`,
`xcode-post-fragment-stage.txt` and `xcode-ray-tracing.txt` are all-zero or
empty. That is the absence of graphics work in the capture, not a gap in Xcode
or in gputrace; `internal/parity` reports those columns as `NO SIGNAL`.
