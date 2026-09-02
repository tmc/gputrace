# Xcode Counters-tab oracle, second capture

Ground-truth per-encoder counter values exported by Xcode itself. Nothing here
is decoded by us; every number came out of Xcode.

This is the *second* oracle. The first, in `testdata/xcode-oracle/`, is a
23-encoder capture. Keeping both is the point: our Execution Cost method scores
0.911 pp worst-case against the first and 2.941 pp against this one, and only
having two captures made that visible.

## Capture

    trace     qwen25-05b-static_tokens_2_to_3-wperfdata.gputrace
              (profiler-only: .gpuprofiler_raw, no unsorted-capture)
    workload  MLX Qwen2.5-0.5B, static mask, tokens 2-3
    host      Apple M4 Max, macOS 26.6
    exporter  Xcode 26.3, GPU trace -> Counters tab
    exported  2026-08-01

## Ground truth reported by Xcode for this capture

    11 command buffers, 11 compute encoders, 466 dispatches, 16 pipelines,
    5.33 ms GPU time, "Num Override Cores: 0" (40 cores)

Xcode's Overview and Performance tabs were screenshotted at the time and agree
with these files.

## Files

Eleven tab-separated Counters-tab exports, one per counter group. Only the
compute and memory groups carry data: this is a compute-only workload, so
`vertex-shader.txt`, `fragment-shader.txt`, `primitives.txt`, `vertices.txt`,
and the two fragment-stage files are present for completeness and are empty of
compute rows.

Each populated file has a header row and 11 encoder rows, named
`<id> Compute Encoder 0 <pointer>`. The pointer column joins 1:1 to
`encoderInfoData` pointer IDs at runtime, in ordinal order.

## Known-bad columns in these exports

`Kernel Texture Cache Miss Rate` and `Kernel ALU Half Instructions` are 0.00%
on every row. That is consistent with a bfloat16 workload issuing no half-float
ALU and reading no textures, but it has not been independently confirmed, so do
not use either column as a parity target.
