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
| `APSTimelineData` | UID | Nested timeline data with ReplayerGPUTime, command-buffer timestamps, and GPRWCNTR encoder profile blobs |

## Binary Data Structures

### pipelineStateInfoData (40 bytes/record)

Maps pipeline states to function names and addresses.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    4     uint32  Pipeline ID               Internal ID (27, 28, 29...)
0x04    4     -       Reserved
0x08    8     uint64  Pipeline Address          Metal PSO pointer (0x8c7464f00)
0x10    4     uint32  Function Info Index        Index into functionInfoData
0x14    8     -       Reserved
0x1C    12    -       Reserved/Flags
```

**Critical Finding:** The function string index is NOT at offset 0x18 of pipelineStateInfoData (that field often points to empty strings). Instead, use `functionInfoData[i]` at offset 28-32 (bytes `[28:32]`) as the string index into the `strings` array for correct function name resolution.

### functionInfoData (48 bytes/record)

Maps function info indices to function name strings.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    28    -       Various metadata
0x1C    4     uint32  String Index              Index into strings array ← KEY FIELD
0x20    16    -       Reserved
```

**Note:** The correct pipeline-to-function-name mapping uses `functionInfoData[i][28:32]` as the string index, where `i` is the Function Info Index from `pipelineStateInfoData`.

### gpuCommandInfoData (32 bytes/record)

Per-dispatch timing information.

```text
Offset  Size  Type    Field                     Notes
------  ----  ------  ----------------------    -------------------------
0x00    4     uint32  Command Index             Dispatch sequence (0, 1, 2...)
0x04    4     -       Unknown
0x08    8     uint64  Pipeline Info             Upper 32 bits = pipeline index
0x10    8     uint64  Cumulative Time (µs)      Running total, subtract previous for duration
0x18    8     uint64  Encoder/Flags             Lower 32 bits = encoder index
```

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
0x00    8     uint64  Sequence ID               Encoder sequence identifier
0x08    8     uint64  Start Timestamp           Raw timestamp value
0x10    8     uint64  Cumulative Offset (µs)    End time, cumulative
0x18    8     -       Unknown                   Possibly dependency info
0x20    8     -       Unknown
```

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
| `Compilation time in milliseconds` | float | Shader compile time |

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
