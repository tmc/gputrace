# Source-level cost and the Heat Map

Two questions, both answered negatively:

1. Can gputrace attribute cost to a line inside a Metal kernel, the way Xcode's
   shader source viewer does?
2. What backs Xcode's Heat Map tab, and can gputrace reproduce it?

The answer to both is no, and the reason is the same in each case: the data is
not archived. This document records the falsifiers that were run and what they
returned, so the negatives can be rechecked rather than taken on faith.

## Method

Two genuinely independent archives were used:

- `qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2.gputrace`
- `qwen25-05b-python-producer-tokens1-3-perfdata.gputrace`

The `-perfdata2` and `-perfdata3` bundles are byte-identical (sha1 `295eecd5`)
and count as one observation, not two. Every count below was reproduced on both
archives independently; where the two disagree, both numbers are given.

Xcode's own rendering of the same workload is available as the oracle capture in
`~/tmp/gputrace-xcode-oracle-20260731` (`ui-heatmap.png`, `ui-shaders.png`).

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

Falsifiers 2 and 3 below do not depend on the build and are unaffected: no
rebuild adds a program counter to `GRC_SOURCE_ID`, and none gives a compute
pipeline the render target the Heat Map shades.

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

[V] Verified. `shaderProfilerData` appears, but a sibling investigation
established that the blobs it names hold only machine-wide samples
(`GRC_ENCODER_ID` `0xFFFFFFFF`) and no source field. Every other key is absent.

## What Xcode actually shows

[V] The oracle screenshot `ui-heatmap.png` shows the Heat Map tab selected with
a compute pipeline selected in the encoder list. Xcode renders:

> Heat map unavailable for compute pipeline states
> Select a compute dispatch to view heat maps

That is Xcode declining to draw a heat map for this workload. The Heat Map is a
**render**-pipeline feature: it shades a render target by per-pixel or per-tile
cost, which is why the spatial keys in falsifier 3 are the ones to look for and
why they are all zero in a compute-only trace. A pure-compute Metal workload has
no render target to shade.

[V] `ui-shaders.png` shows the Shaders tab is per-**pipeline**, not per-line:
its columns are Cost, Name, Type, Pipeline State, # SIMD Groups, # Allocated
Registers. Xcode's own per-line source cost requires a shader-profiling capture
with debug info retained, which these archives do not contain — consistent with
falsifier 1.

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

A capture taken with shader profiling enabled *and* the metallib built with
debug info retained would put a `DEBI` section in the library. Whether Apple
then archives an instruction-level sample stream alongside it is untested — no
such capture was available here. Until one is, source-line cost should be
reported as absent, not approximated.
