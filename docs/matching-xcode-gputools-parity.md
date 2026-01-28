# Matching Xcode GPU Tools Parity

**Date:** 2026-01-09
**Status:** Near complete

This document tracks gputrace's progress toward feature parity with Xcode Instruments' GPU profiling tools.

## Overview

Xcode Instruments provides comprehensive GPU profiling through several interconnected views. Our goal is to extract equivalent data programmatically from `.gputrace` bundles without requiring Xcode.

## Feature Comparison Matrix

| Feature | Xcode | gputrace | Status |
|---------|-------|----------|--------|
| **Trace Capture** |
| Capture GPU trace | Yes | Yes (`gputrace capture`) | Done |
| Capture with profiling | Yes | Yes (`--profile`) | Done |
| Capture from Xcode project | Yes | Yes (`gputrace xcui`) | Done |
| **Timing Analysis** |
| Encoder timing | Yes | Yes | Done |
| Dispatch timing | Yes | Yes | Done |
| Kernel Duration | Yes | Yes | Done |
| Execution Cost % | Yes | Yes | Done (from Profiling_f_*.raw) |
| **Pipeline Analysis** |
| Pipeline state list | Yes | Yes | Done |
| Function name resolution | Yes | Yes | Done |
| Instruction counts | Yes | Yes | Done |
| Register allocation | Yes | Yes | Done |
| Compilation time | Yes | Yes | Done |
| **Counter Data** |
| GPU counter samples | Yes | Partial | In progress |
| CSV export | Yes | Yes | Done |
| Per-encoder aggregation | Yes | Yes | Done |
| **Export Formats** |
| pprof output | No | Yes | Done |
| JSON output | No | Yes | Done |
| Flamegraph | No | Yes | Done |
| **Visualization** |
| Timeline view | Yes | Yes | Done (APSTimelineData from plist) |
| Dependency graph | Yes | Partial | Basic support |

## Command Reference

### Core Commands

| Command | Description | Data Source |
|---------|-------------|-------------|
| `gputrace profiler <trace>` | Full profiler data (timing, pipelines, execution cost) | streamData plist |
| `gputrace timing <trace>` | Kernel timing breakdown | streamData or synthetic |
| `gputrace shaders <trace>` | Shader metrics with register info | streamData plist |
| `gputrace kernels <trace>` | Kernel function list with dispatch counts | Encoder labels + pipeline map |
| `gputrace encoders <trace>` | List compute encoders | CS records |
| `gputrace pprof <trace>` | Export to pprof format | Timing + pipeline stats |

### Profiler-Only Traces

Commands now work with profiler-only traces (`.gpuprofiler_raw` without `unsorted-capture`):

```bash
# These work with profiler-only traces:
gputrace profiler /path/to/trace-perfdata.gputrace
gputrace timing /path/to/trace-perfdata.gputrace
gputrace shaders /path/to/trace-perfdata.gputrace
```

## Timing Metrics Deep Dive

### What Xcode Shows

Xcode displays three distinct timing metrics:

#### 1. Dispatch Duration (Per-Command)

- **Location:** GPU Profiler → Dispatches list
- **Source:** `gpuCommandInfoData` in streamData
- **Granularity:** Individual dispatch calls
- **gputrace:** `gputrace profiler --json` → `dispatches[].duration_us`

#### 2. Kernel Duration (Per-Pipeline)

- **Location:** GPU Profiler → Summary view, Pipeline Statistics
- **Source:** Aggregated from gpuCommandInfoData
- **Calculation:** Sum of all dispatch durations for each pipeline
- **gputrace:** `gputrace profiler` → "Aggregated by Function" section

Example Xcode display:
```
ComputePipelineState 0x8c7464f00
  gemv_t_float16_bm1_bn4
  Kernel Duration: 164.49 µs (34.2%)
```

#### 3. Execution Cost (Statistical Profiling)

- **Location:** GPU Profiler → Encoders list, "Execution Cost" column
- **Source:** `Profiling_f_*.raw` statistical samples
- **Method:** GPU sampling during execution
- **gputrace:** `gputrace profiler` → "Statistical Execution Cost" section

Xcode tooltip explains:
> "Shader execution cost percentage calculated using statistical profiling of shader programs executing on the GPU"

### Implementation Status

```
┌─────────────────────────────────────────────────────────────┐
│                     Timing Metrics                          │
├─────────────────────────────────────────────────────────────┤
│  Dispatch Duration    [████████████████████] 100% Done      │
│  Kernel Duration      [████████████████████] 100% Done      │
│  Execution Cost       [████████████████████] 100% Done      │
└─────────────────────────────────────────────────────────────┘
```

## Pipeline Statistics Parity

### What Xcode Shows

Pipeline Statistics view displays per-shader compilation metrics:

```
Pipeline State: 0x8c7464f00
Function: gemv_t_float16_bm1_bn4

Compilation Statistics:
  Instruction Count: 847
  ALU Instructions: 612
  FP16 Instructions: 445
  Branch Instructions: 23

Resource Usage:
  Temporary Registers: 32
  Uniform Registers: 8
  Threadgroup Memory: 4096 bytes
  Spilled Bytes: 0
```

### gputrace Equivalent

```bash
gputrace profiler /path/to/trace.gputrace

# Output:
Pipelines (3):
  [0] ID=27 gemv_t_float16_bm1_bn4
      Instructions: 847 (ALU=612, FP32=0, FP16=445, INT=167, Branch=23)
      Registers: temp=32 uniform=8 spilled=0 bytes
```

**Status:** Full parity achieved for compilation statistics.

## Shader Metrics

The `shaders` command now shows real register data from streamData:

```bash
gputrace shaders /path/to/trace.gputrace

# Output:
Cost    Name                                    Type     Pipeline State           # Allocated Registers  Spilled Bytes
4.00%   v_copyfloat32float16                   Compute  Compute Pipeline 0x...   9                      0 bytes
61.23%  gemv_t_float16_bm1_bn16_...            Compute  Compute Pipeline 0x...   2                      0 bytes
```

## Timeline Data

Timeline data is extracted from APSTimelineData in the streamData plist:

```bash
gputrace profiler /path/to/trace.gputrace --json | jq '.timeline'

# Output:
{
  "command_buffer_timestamps": [
    {"index": 0, "start_ticks": 123456, "end_ticks": 134993},
    ...
  ],
  "timebase_numer": 125,
  "timebase_denom": 3,
  "absolute_time": 1234567890
}
```

Duration calculation: `(end_ticks - start_ticks) * timebase_numer / timebase_denom` = nanoseconds

## Known Gaps

### 1. Full Counter Parity

**Gap:** Not all 241 Xcode CSV columns are validated.

**Status:** Core counters working (ALU utilization, kernel invocations, occupancy).

### 2. Memory Dependency Graph

**Gap:** Limited dependency visualization.

**Xcode shows:** Buffer read/write dependencies between dispatches.

**gputrace:** Basic hazard detection (RAW, WAW, WAR) implemented, no visualization.

### 3. Live Capture

**Gap:** Cannot capture from running app without Xcode.

**Why:** Requires Metal debugging entitlements and process attachment.

**Workaround:** Use `gputrace xcui` with Xcode UI automation.

## Validation Checklist

When testing parity with a new trace:

- [x] Run `gputrace profiler <trace>` and compare function list with Xcode
- [x] Verify Kernel Duration percentages match (within 1%)
- [x] Check instruction counts match Pipeline Statistics
- [x] Export counters CSV and diff against Xcode export
- [x] Verify encoder timing totals match
- [x] Verify execution cost percentages match

### Sample Validation Script

```bash
#!/bin/bash
TRACE="$1"

echo "=== Function List ==="
gputrace profiler "$TRACE" | grep -A 100 "Aggregated by Function"

echo ""
echo "=== Pipeline Stats ==="
gputrace profiler "$TRACE" --json | jq '.pipelines[] | {name: .function_name, instr: .instruction_count}'

echo ""
echo "=== Total Time ==="
gputrace profiler "$TRACE" --json | jq '.total_time_us'

echo ""
echo "=== Execution Cost ==="
gputrace profiler "$TRACE" | grep -A 20 "Statistical Execution Cost"
```

## Roadmap

### Phase 1: Timing Parity ✅ Complete
- [x] Dispatch duration extraction
- [x] Kernel duration aggregation
- [x] Per-encoder timing
- [x] Execution Cost from Profiling_f_*.raw

### Phase 2: Counter Parity (In Progress)
- [x] Basic counter extraction
- [x] CSV export format
- [ ] Full 241-column validation
- [ ] Architecture-specific counter mapping

### Phase 3: Visualization
- [x] Timeline data extraction (APSTimelineData)
- [ ] Web-based timeline viewer
- [ ] Dependency graph export

### Phase 4: Advanced Features
- [x] Shader source correlation
- [ ] Performance recommendations
- [ ] Regression detection

## References

- [STREAMDATA_FORMAT.md](STREAMDATA_FORMAT.md) - streamData binary format
- [BINARY_FORMAT_REFERENCE.md](BINARY_FORMAT_REFERENCE.md) - Counter file format
- [trace-format.md](trace-format.md) - Overall trace structure
- Apple Developer Documentation: Metal Performance Shaders
- WWDC Sessions: GPU Profiling with Metal

---

**Last Updated:** 2026-01-09
