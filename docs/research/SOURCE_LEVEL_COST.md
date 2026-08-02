# Source-level cost and the Heat Map

Two questions:

1. Can gputrace attribute cost to a line inside a Metal kernel, the way Xcode's
   shader source viewer does? **No** — nothing in the archive supports it.
2. What backs Xcode's Heat Map tab, and can gputrace reproduce it? A heat map
   **does** exist for compute dispatches, showing per-thread-position Shader
   Execution Cost. But no part of it is in the archive, so gputrace cannot
   reproduce it from a bundle.

This document records the falsifiers that were run and what they returned, so
the conclusions can be rechecked rather than taken on faith.

> **Correction.** An earlier revision of this document claimed the Heat Map was
> a render-pipeline-only feature, "absent by construction" for compute work.
> That was wrong. It was inferred from a single oracle screenshot in which a
> *pipeline* happened to be selected, plus a grep for guessed key names that
> returned zero. Driving Xcode and selecting an actual *dispatch* renders a heat
> map immediately. The corrected finding is in "The Heat Map is real" below. The
> lesson is the one this project keeps relearning: a zero from a grep for names
> you invented is not evidence of absence.

## Method

Three archives were used. Two for the byte-level censuses:

- `qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2.gputrace`
- `qwen25-05b-python-producer-tokens1-3-perfdata.gputrace`

The `-perfdata2` and `-perfdata3` bundles are byte-identical (sha1 `295eecd5`)
and count as one observation, not two. Every count below was reproduced on both
archives independently; where the two disagree, both numbers are given.

and a third, `qwen25-05b-static_tokens_2_to_3-wperfdata.gputrace`, which was the
one open in Xcode and so the one the UI observations below were made against.

Xcode's own rendering is available as oracle captures in
`~/tmp/gputrace-xcode-oracle-20260731`: `ui-shaders.png`, `ui-costgraph.png`,
`ui-heatmap.png` (pipeline selected, no heat map) and `ui-heatmap-dispatch.png`
(dispatch selected, heat map rendered — captured while writing this document).

## What the archive does carry

Metal **source text** is archived. Each `.gputrace` bundle holds sidecar files
under hex names whose first bytes are `0a 2f 2f 20` (`"\n// "`), and they are
whole preprocessed Metal translation units:

```
// Auto generated source for mlx/backend/metal/kernels/utils.h
#line 1 "mlx/backend/metal/kernels/bf16.h"
```

[V] Verified: 6 such sidecars in the first archive, 3 in the second, all
readable as Metal. `shader.ShaderSourceMapper.IndexTraceBundleSources` already
finds them, and the `#line` directives even let a line be traced back to the
original header.

So gputrace can say *where a kernel is written*. That is the whole of it.

## Compiler source locations are present, but have no cost edge

The processed `GTMioShaderBinaryData` model carries a small compiler location
table even though the archived metallib has no `DEBI` or `LINE` section. Each
record has a file path, function name, line, column, and owning shader-binary
index. The mapping is decoded through the model's `debugStrings` table, not by
guessing the raw struct field order:

| Capture | Shader binaries | Source locations |
| --- | ---: | ---: |
| `static_tokens_2_to_3` | 801 | 59 |
| `staticmask-warm-tokens2-4-rep1` | 1,617 | 78 |

`[V]` In both captures the location records' first two fields are bounded by
their binary's string table. The table contents establish the measured order:
field 1 selects source paths, field 2 selects function names, and fields 3 and
4 are line and column. The named direct accessors agree with the table when
read as `NSString` objects.

This is source mapping, not source-level cost. The current model exposes no
validated instruction-to-location edge and no cost attributed to an instruction
or location. It therefore cannot change a duration or counter observation into
a line-level measurement.

## Falsifier 1: does the archived MTLLibrary carry debug info?

If per-line cost were reconstructible, the metallib would need a debug-info
section mapping instruction offsets to source lines.

Tag census over the `MTLB` blob of each archive (`AC3508247A62B629`, 157 MB;
`203EA04B905F7819`, 162 MB):

| Tag | Meaning | Archive 1 | Archive 2 |
|-----|---------|-----------|-----------|
| `NAME` | function-table entry | 15937 | 16447 |
| `DEBI` | debug info | **0** | **0** |
| `LINE` | line table | **0** | **0** |
| `SORC` / `SRCA` | source archive | **0** | **0** |
| `HSRD` | source hash/dir | **0** | **0** |

[V] Verified by byte scan of the whole blob. The libraries are stripped: ~16k
functions, not one debug-info section between them. There is no offset-to-line
mapping to join against, so even a perfect instruction-level profile could not
be projected onto source.

**This particular zero is a property of how the shaders were built, not of the
archive format, and it is reversible.** Both archives were produced by a stock
MLX build, and MLX gates Metal debug info behind an option that defaults off:

	CMakeLists.txt:39
	  option(MLX_METAL_DEBUG "Enhance metal debug workflow" OFF)

	mlx/backend/metal/kernels/CMakeLists.txt:22
	  set(METAL_FLAGS ${METAL_FLAGS} -gline-tables-only -frecord-sources)

	cmake/extension.cmake:30-32
	  if(MLX_METAL_DEBUG OR MTLLIB_DEBUG)  ... same two flags

[V] Read from the MLX checkout, not from documentation. `-gline-tables-only`
and `-frecord-sources` are exactly the pair that emits the `LINE` and `SORC`
sections counted as zero above, so the census result is the documented default
rather than evidence about what Metal can archive. A producer rebuilt with
`-DMLX_METAL_DEBUG=ON` should carry them, which would reopen falsifier 1.

Falsifier 2 below does not depend on the build and is unaffected: no rebuild
adds a program counter to `GRC_SOURCE_ID`. So a debug-info build would supply
the line table and still leave per-line cost without a cost source to join it
against.

## Falsifier 2: does any counter record carry a program counter or source line?

`GRC_SOURCE_ID` is one of the seven fixed GPRWCNTR columns and its name invites
the assumption that it locates a sample in the program. It does not.

Scanning every `GPRWCNTR` record magic in `streamData` and tabulating column 6:

| Archive | Records | Distinct `GRC_SOURCE_ID` | Values |
|---------|---------|--------------------------|--------|
| 1 | 3,707,451 | **5** | 0, 1, 2, 3, 4 |
| 2 | 4,356,460 | **5** | 0, 1, 2, 3, 4 |

[V] Verified. For comparison, `GRC_ENCODER_ID` in the same scan takes 57,806 and
87,461 distinct values. `GRC_SOURCE_ID` is a small dense enum — a sample-source
or hardware-unit tag — with a value domain three million records wide and five
values deep. It is not a program counter, not a source line, and not an index
into anything of source-like cardinality. The name was misleading, as
name-derived guesses about this API usually are on this project.

## Falsifier 3: are there source- or heat-map-shaped keys in streamData?

Key census over `streamData` (440 MB), both archives, all counts identical:

| Key | Count |
|-----|-------|
| `shaderProfilerData` | 1 |
| `sourceLine`, `lineNumber`, `SourceLine`, `sourceMap` | 0 |
| `heatMap`, `HeatMap`, `heatmap` | 0 |
| `perLine`, `SourceAttribution`, `debugSource` | 0 |
| `sourceArchive`, `MTLSourceArchive` | 0 |
| `threadgroupID`, `tileCost`, `perTile`, `quadCost` | 0 |
| `fragmentCost`, `pixelCost`, `attachmentCost`, `RenderTarget` | 0 |

[V] Verified as counts. Note carefully what this does and does not establish:
these are names that were *guessed*, so a zero means "not under this name", not
"not present". Reading the spatial rows as proof that no per-position data
exists anywhere is exactly the error corrected at the top of this document. The
rows are retained because they are true and worth not re-running, not because
they carry the weight originally put on them.

`shaderProfilerData` appears, but a sibling investigation
established that the blobs it names hold only machine-wide samples
(`GRC_ENCODER_ID` `0xFFFFFFFF`) and no source field. Every other key is absent.

## What Xcode actually shows

[V] `ui-shaders.png` shows the Shaders tab is per-**pipeline**, not per-line:
its columns are Cost, Name, Type, Pipeline State, # SIMD Groups, # Allocated
Registers. Xcode's own per-line source cost requires a shader-profiling capture
with debug info retained, which these archives do not contain — consistent with
falsifier 1.

[V] `ui-costgraph.png`, and the same view driven live, settle the Cost Graph
question. With a pipeline state selected the Cost Call Graph is a **single**
frame spanning the whole 0–100% axis, labelled with the pipeline:

```
gemv_bfloat16_bm8_bn1_sm1_sn32_tm4_tn4_nc0_axpby0 (Compute Pipeline 0xa432f8a80)
```

There are no child frames and no expansion into source. The Source Files panel
beside it lists `MTLLibra…9f5bfac0` with one child, `3BAA0D…160BC9` — which is
exactly the Metal source sidecar identified above, confirming that Xcode's own
notion of "the source" for a library is that same archived text. The source
viewer next to it renders empty (line numbers 1 and 2, no content).

So Xcode itself resolves cost no deeper than the pipeline for these traces, and
that is precisely what falsifier 1 predicts: with no `DEBI` section there is no
mapping to attribute a cost to a line, so the flame graph bottoms out at the
function and the source pane has nothing to shade.

## The Heat Map is real, and is not in the archive

[V] Selecting a compute **dispatch** — not an encoder, not a pipeline state —
renders a heat map. The tab's placeholder text tracks the selected object and
says what it wants:

| Selection | Message |
|-----------|---------|
| compute encoder | "Heat map unavailable for compute encoders" |
| compute pipeline state | "Heat map unavailable for compute pipeline states" |
| compute shader | "Heat map unavailable for compute shader" |
| **compute dispatch** | **renders** |

All four end with "Select a compute dispatch to view heat maps". The earlier
reading of this as a refusal was wrong; it is an instruction.

[V] What renders is titled **Shader Execution Cost**: a 2D grid over the
dispatch, shaded red by cost, with a zoom control, `X:` and `Y:` spinners that
fill in with the position under the cursor (33, 12 in the captured example), and
a detail pane reading "Select a pixel to view SIMD Group". So the underlying
datum is a per-thread-position execution cost, drillable to the SIMD group that
ran at that position. This is a genuine spatial cost map, and it is the answer
to "which part of my dispatch is slow".

[D] **It is not archived.** A key census over the same trace's `streamData`
returns zero for `ShaderExecutionCost`, `ExecutionCost`, `HeatMap`, `heatMap`,
`heatMapData`, `ShaderCost`, `SIMDGroup`, `simdGroup`, `PerPixel` and
`perPixel`, and falsifier 3 already found no per-position key under any other
name tried. Nothing of the size or shape of a per-thread-position cost grid is
present for any of the 488 dispatches.

Marked [D] and not [V] because the census can only show that the guessed names
are absent, which is the mistake this document already made once. The positive
claim that Xcode computes the heat map by **replaying the dispatch on the
device** with an instrumented shader is untested here. It is the natural
explanation — it needs the GPU, it is per-dispatch, and the project already has
an `internal/replay` package wrapping `GPUToolsReplay` — but it has not been
proven, and it should be proven before anyone builds on it.

Either way the consequence for gputrace is the same and is firm: **a heat map
cannot be produced from a `.gputrace` bundle alone.** Reproducing it means
replaying, not parsing. That is a much larger piece of work than reading a
record, and it should not be started on the assumption that the data is sitting
in the archive under a name nobody has grepped for yet.

## Consequence for `pprof --source-lines`

The flag is not wrong, but its granularity was undisclosed. It maps a kernel
name to the line where the kernel is *declared* and attributes the kernel's
entire duration there. A `go tool pprof -list` listing therefore shows 100% of a
kernel's cost on one line, which reads as a measurement of that line. It is not;
it is a kernel-level number placed at a locatable coordinate.

The command now prints, alongside the existing timing-source disclosure:

```
Granularity: per kernel, not per line. Each kernel's cost is reported at its
declaration line; the trace carries no cost breakdown within a kernel body.
```

and no longer advertises "per-line costs".

## Reproducing

```
# Falsifiers 1 and 3 (tag and key censuses)
LC_ALL=C grep -c -a -o DEBI <bundle>/<MTLB blob>
LC_ALL=C grep -c -a -o sourceLine <bundle>/*.gpuprofiler_raw/streamData

# Falsifier 2: scan every GPRWCNTR magic and tabulate column 6.
# GPRWCNTRStride/ParseGPRWCNTR in internal/counter/gprwcntr.go decode a blob
# once it has been lifted out of the plist; the census above scanned the raw
# file for the magic directly, which needs no plist parse.
```

## What would change the answer

For source-line cost: a capture taken with shader profiling enabled *and* the
metallib built with debug info retained would put a `DEBI` section in the
library. Whether Apple then archives an instruction-level sample stream
alongside it is untested — no such capture was available here. Until one is,
source-line cost should be reported as absent, not approximated.

For the heat map: confirming or refuting the replay hypothesis. Two cheap
checks, neither run here — whether the Heat Map tab still renders with the
capture device absent or the trace opened on a different machine, and whether
`GPUToolsReplay` is entered when a dispatch is selected. A negative on the first
would prove the data is archived after all, under a name not yet guessed, and
would reopen this entirely.
