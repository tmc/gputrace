# streamData Format Reference

**Date:** 2025-01-09
**Status:** Documented based on reverse engineering of .gpuprofiler_raw/streamData

## Overview

The `streamData` file within `.gpuprofiler_raw/` directories contains profiler metadata in NSKeyedArchiver plist format. It provides per-dispatch timing, pipeline compilation statistics, and encoder information.

### File Location

```text
trace.gputrace/
└── *.gpuprofiler_raw/
    ├── streamData          ← NSKeyedArchiver plist (this document)
    ├── Counters_f_*.raw    ← GPU counter samples (see research/BINARY_FORMAT_REFERENCE.md)
    ├── Profiling_f_*.raw   ← Statistical profiling samples
    └── Timeline_f_*.raw    ← Timeline visualization data
```

## Shader Table Metrics

Xcode Instruments displays several shader-table metrics. Understanding their differences is critical for accurate profiling:

| Metric | Source | What It Measures | Use Case |
|--------|--------|------------------|----------|
| **Dispatch Duration** | gpuCommandInfoData | StreamData dispatch duration or cumulative offset delta | Per-dispatch granularity |
| **Kernel Duration** | gpuCommandInfoData aggregated | Sum of dispatch durations per pipeline | Function-level aggregation |
| **Execution Cost** | Profiling_f_*.raw | Statistical GPU sampling percentage | Relative cost comparison |

### Dispatch Duration vs Kernel Duration

- **Dispatch Duration**: Time for a single `dispatchThreads` or `dispatchThreadgroups` call
- **Kernel Duration**: Aggregated time for all dispatches using the same pipeline state

Example: If `gemv_t_float16` is called 10 times with 16.4 µs average, Kernel Duration = 164.0 µs

### Execution Cost (Statistical Profiling)

The "Execution Cost" percentage shown in Xcode uses statistical GPU sampling from `Profiling_f_*.raw` files. This is **not** the same as dispatch timing. See `internal/counter/execution_cost.go` for the implementation.

## Timeline and Summary Timing

Measured replay timing comes from `.gpuprofiler_raw/streamData` when APSTimelineData is present:

- `ReplayerGPUTime`: Xcode Effective GPU Time.
- Command Buffer Timestamps: command-buffer active and wall-clock spans.
- `encoderInfoData` and `gpuCommandInfoData`: encoder and dispatch cumulative offsets.

GPRWCNTR encoder profile blobs annotate timeline/profile samples. They do not replace measured wall-clock timing.

## NSKeyedArchiver Structure

The plist uses Apple's NSKeyedArchiver format with a `$objects` array containing referenced objects.

### Top-Level Keys (in $objects[1])

| Key | Type | Description |
|-----|------|-------------|
| `strings` | UID | Array of function name strings |
| `pipelineStateInfoData` | UID | Binary data with pipeline metadata |
| `pipelineStateInfoSize` | uint64 | Record size (typically 40 bytes) |
| `pipelinePerformanceStatistics` | UID | Dictionary of compilation stats |
| `gpuCommandInfoData` | UID | Binary data with per-dispatch timing |
| `gpuCommandInfoSize` | uint64 | Record size (typically 32 bytes) |
| `functionInfoData` | UID | Binary data with function metadata |
| `functionInfoSize` | uint64 | Record size (typically 48 bytes) |
| `encoderInfoData` | UID | Binary data with encoder timing |
| `encoderInfoSize` | uint64 | Record size (typically 40 bytes) |
| `APSData` | UID | Array of archived APS payload blobs |
| `APSTimelineData` | UID | Nested timeline data with ReplayerGPUTime, command-buffer timestamps, and GPRWCNTR encoder profile blobs |
| `APSCounterData` | UID | Array of archived counter payload blobs |
| `shaderProfilerData` | UID | Array of shader-profiler payload blobs |
| `gpuTimelineData` | UID | Array of GPU-timeline payload blobs |
| `batchIdFilteredCountersData` | UID | Array of batch-filtered counter payload blobs |

The Perfetto evidence manifest reports two counts for each array: its exact
top-level entry count and the number of entries decoded as `NSData` blobs. The
difference is reported as non-blob entries. These counts describe archive
structure only; one blob may contain many records or samples. Missing or
malformed arrays remain unavailable instead of appearing as empty arrays.

For `APSCounterData`, the manifest additionally reports decoded GPRWCNTR
records split into capture-attributed, machine-wide, and remaining
unattributed populations. It also reports the decoder's blob, encoder-ID,
per-group aggregate, pass-column, trace-ID, and stride-mismatch counts. These
are coverage and integrity evidence, not a counter time series: the archive
does not establish a mapping from counter timestamps to the exported busy or
wall clocks.

The manifest inventories the decoded `APSData` dictionary shapes separately.
It counts dictionaries containing `Counter Info`, `ShaderProfilerData`, `Post
Processing Frame Marker`, `APSTraceDataFile`, and `TraceId to BatchId`, plus
malformed blobs. The counts are independent key-presence observations rather
than exclusive record kinds. `APSTraceDataFile` contents remain private opaque
payloads until their schema and units are established.

## Binary Data Structures

Every field below carries a confidence marker, because these layouts are read
out of an undocumented format and not all of them are known equally well:

- **[V]** Verified against the Objective-C type encodings on the
  `GTShaderProfilerStreamData` struct accessors. These are compiled into the
  framework and state the layout outright.
- **[D]** Derived from the data by a check a wrong answer would have failed.
- **[?]** Inferred from value patterns in a single archive. May be coincidence.

Last checked against one archive: `version=5`, M4 Max / AGXMetalG16X. Record
sizes and offsets come from the framework and should hold generally; the **[?]**
*meanings* do not have that backing. Treat an unmarked claim as [?].

### pipelineStateInfoData (40 bytes/record)

Maps pipeline states to function names and addresses.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    4     uint32  Pipeline ID           [V] Internal ID (27, 28, 29...)
0x04    4     -       Reserved
0x08    8     uint64  Pipeline Address      [V] Metal PSO pointer (0x8c7464f00)
0x10    8     uint64  Object/Serial ID      [?] NOT the function info index
0x18    4     uint32  Pipeline Ordinal      [?] 0..n-1, matches array position
0x1C    4     uint32  Dispatch Count        [D] Times this pipeline was dispatched
0x20    4     uint32  Function Info Index   [?] Unconfirmed; see below
0x24    4     -       Reserved
```

**Critical Finding:** The function string index is NOT at offset 0x18 of pipelineStateInfoData (that field often points to empty strings). Instead, use `functionInfoData[i]` at offset 28-32 (bytes `[28:32]`) as the string index into the `strings` array for correct function name resolution.

**Dispatch Count (0x1C):** reads `98,144,96,96,48,96,96,98,48,2,2,2,1,37` for the
fourteen pipelines in the test archive, matching a per-pipeline tally of
`gpuCommandInfoData` exactly. Previously documented as reserved.

**Function Info Index (0x20):** the code pairs functionInfo to pipelineState *by
array position*. `0x20` is the likelier real link, but in this archive it and the
ordinal at `0x18` are both `0..13`, so the data cannot distinguish them. Do not
rely on either until an archive is found where they diverge.

**Naming pipelines whose string index is empty:** prefer
`pipelinePerformanceStatistics[<id>]["Compile Performance"]["Function Name"]`,
keyed by the uint64 at offset 0x00. It names all fourteen pipelines in the test
archive, including the two that resolve to an empty string via the normal path.

### functionInfoData (48 bytes/record)

Maps function info indices to function name strings.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    28    -       Various metadata
0x1C    4     uint32  Name String Index     [V] Index into strings array ← KEY FIELD
0x20    4     uint32  Source File Index     [?] Index into strings array
0x24    12    -       Reserved
```

**Note:** The correct pipeline-to-function-name mapping uses `functionInfoData[i][28:32]` as the string index, where `i` is the Function Info Index from `pipelineStateInfoData`.

### gpuCommandInfoData (32 bytes/record)

Per-dispatch timing information.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    4     uint32  Command Index         [V] Dispatch sequence (0, 1, 2...)
0x04    4     uint32  Encoder Index         [D] Owning encoder ← see below
0x08    8     uint64  Pipeline Info         [V] Upper 32 bits = pipeline index
0x10    8     uint64  Cumulative Time (µs)  [V] Running total, subtract previous
0x18    4     uint32  Constant Tag          [D] Always 2 — NOT the encoder index
0x1C    4     int32   Constant             [?] Always -1
```

**Encoder Index (0x04):** this was previously documented at 0x18, which holds the
constant 2 in every record — so every dispatch was attributed to encoder 2, and
the timeline export stacked all of them onto one track with start times derived
from the wrong encoder's base. Fixed in `internal/counter/streamdata.go`.

The correct offset was established using `encoderInfoData`'s first-command index
and command count, which tile the command stream exactly with no gaps or
overlaps. Taking that as ground truth for which encoder owns each command, over
864 records: `0x04` agrees for all 864, `0x08` for 465, `0x18` for none.

**Duration Calculation:**
```go
duration := record[i].CumulativeTime - record[i-1].CumulativeTime
// First record's duration equals its cumulative time
```

### encoderInfoData (40 bytes/record)

Per-encoder timing for command encoders.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    8     uint64  Sequence ID           [V] Encoder sequence identifier
0x08    8     uint64  Start Timestamp       [V] Raw timestamp value
0x10    8     uint64  Cumulative Offset(µs) [V] End time, cumulative
0x18    4     uint32  Encoder Index         [?] 0..n-1, matches array position
0x1C    4     uint32  First Command Index   [D] Into gpuCommandInfoData
0x20    4     uint32  Command Buffer Index  [?] Non-contiguous across encoders
0x24    4     uint32  Command Count         [D] Commands owned by this encoder
```

**First Command Index / Command Count (0x1C, 0x24):** these two define the half
open command range `[first, first+count)` each encoder owns. Across the test
archive the twenty-one ranges tile all 864 commands with no gap and no overlap,
which is what makes them usable as ground truth for validating other fields.

### pipelinePerformanceStatistics

NSDictionary mapping pipeline IDs to compilation metrics:

| Key | Type | Description |
|-----|------|-------------|
| `Instruction count` | int | Total shader instructions |
| `ALU instruction count` | int | ALU operations |
| `FP32 instruction count` | int | 32-bit float operations |
| `FP16 instruction count` | int | 16-bit float operations |
| `INT32 instruction count` | int | 32-bit integer operations |
| `INT16 instruction count` | int | 16-bit integer operations |
| `Branch instruction count` | int | Branch/jump instructions |
| `Temporary register count` | int | Temp registers allocated |
| `Uniform register count` | int | Uniform registers |
| `Spilled bytes` | int | Register spill to memory |
| `Threadgroup memory` | int | Shared memory usage |
| `Device load instruction count` | int | Device memory loads |
| `Device store instruction count` | int | Device memory stores |
| `Device atomic instruction count` | int | Device memory atomics |
| `Threadgroup load instruction count` | int | Threadgroup memory loads |
| `Threadgroup store instruction count` | int | Threadgroup memory stores |
| `Threadgroup atomic instruction count` | int | Threadgroup memory atomics |
| `Texture reads instruction count` | int | Texture reads |
| `Texture writes instruction count` | int | Texture writes |
| `Wait instruction count` | int | Wait instructions |
| `Thread invariant spilled bytes` | int | Thread-invariant spill |
| `Constant calculation temporary register count` | int | Temp registers in the constant phase |
| `Constant calculation phase present` | bool | Constant phase was emitted |
| `Compilation time in milliseconds` | float | Shader compile time |
| `Compile Performance` | dict | Compiler timings and `Function Name` |
| `Remarks` | string | YAML optimization remarks from the compiler |
| `Telemetry Statistics` | dict | Empty in every archive seen so far |
| `ComputeBufferPrefetch` | array | Per-buffer prefetch flags |

[V] The full key set above was enumerated from every pipeline of
`qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2.gputrace` (18 pipelines,
all 28 keys present on each).

#### No instruction-type cost breakdown

Xcode's shader inspector shows an "Instruction Type Cost" split (Math,
Comparison, Permute, Data Movement, Load, Predication, Control Flow, Select).
That table is **not** in the trace:

- `pipelinePerformanceStatistics` has no such key; the 28 keys above are all of
  them. It carries counts by *operand type* (FP32/FP16/INT32/…), not cost by
  *instruction category*.
- The top-level `shaderProfilerData` array is empty (0 entries) in the
  profiler-only captures we have.
- `grep` for `Permute`, `Predication`, `Data Movement`, and `Instruction Type`
  over the whole 15 GB `.gputrace` bundle returns no match, in any file.

[D] Xcode appears to derive the split by classifying GPRWCNTR PC samples
against an AGX disassembly it obtains from the compiler service, neither of
which the trace stores. Nothing in gputrace should print those percentages.

## Implementation

### Parsing Example

```go
// Parse NSKeyedArchiver plist
var archive map[string]interface{}
plist.Unmarshal(data, &archive)

objects := archive["$objects"].([]interface{})
obj1 := objects[1].(map[string]any)

// Get function names
stringsUID := obj1["strings"].(plist.UID)
stringsObj := objects[int(stringsUID)].(map[string]any)
nsObjects := stringsObj["NS.objects"].([]any)
// ... resolve UIDs to strings

// Get pipeline info
pipeInfoUID := obj1["pipelineStateInfoData"].(plist.UID)
pipeInfoObj := objects[int(pipeInfoUID)].(map[string]any)
nsData := pipeInfoObj["NS.data"].([]byte)

// Parse 40-byte pipeline records + 48-byte function info records
for i := 0; i < len(nsData)/40; i++ {
    rec := nsData[i*40 : (i+1)*40]
    pipelineAddr := binary.LittleEndian.Uint64(rec[8:16])

    // Use functionInfoData[i][28:32] for string index (correct mapping)
    fiRec := funcInfoData[i*48 : (i+1)*48]
    funcStrIdx := binary.LittleEndian.Uint32(fiRec[28:32])
    funcName := funcNames[funcStrIdx]
}
```

### Aggregating Kernel Duration

```go
// Group dispatches by pipeline
funcTotals := make(map[string]int)
for _, dispatch := range dispatches {
    funcTotals[dispatch.FunctionName] += dispatch.DurationUs
}

// Calculate percentages
totalTime := 0
for _, t := range funcTotals {
    totalTime += t
}
for name, t := range funcTotals {
    pct := float64(t) / float64(totalTime) * 100
    fmt.Printf("%s: %.1f%%\n", name, pct)
}
```

## Validation

Compare output against Xcode Instruments' GPU Profiler view:

1. **Kernel Duration**: Should match Xcode's per-function timing breakdown
2. **Dispatch Count**: Total dispatches should match Xcode's dispatch list
3. **Instruction Counts**: Should match Xcode's Pipeline Statistics view

## Related Files

- `internal/counter/streamdata.go` - Go implementation
- `cmd/gputrace/cmd/profiler.go` - CLI command
- [research/BINARY_FORMAT_REFERENCE.md](./research/BINARY_FORMAT_REFERENCE.md) - Counter file formats

## Future Work

1. **Architecture Testing**: Validate on M1/M2/M3/M4 variants

---

**Last Updated:** 2026-03-17
