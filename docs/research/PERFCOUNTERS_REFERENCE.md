# Performance Counter Reference

Binary layout, field offsets, metric catalog, and parsing status for the
`.gpuprofiler_raw` counter files. This file consolidates the former
`PERFCOUNTERS_STATUS.md` and `PERFCOUNTER_FIELD_OFFSET_MAP.md`, which
maintained overlapping copies of the record layout and offset tables.

Implementation lives in `internal/counter`.

## Overview

The performance counter parsing framework is no longer only scaffolding. Current
`internal/counter` code parses `.gpuprofiler_raw` counter records, applies
file-mapped counter extraction for selected metrics, optionally imports Xcode CSV data as
ground truth, and enriches shader metrics with compilation statistics from
`streamData`.

Important boundary: register allocation and spill counts are currently sourced
from `streamData` `pipelinePerformanceStatistics`, not from direct
`Counters_f_*.raw` field offsets. `HighRegister` remains a real gap; the current
binding-gap note records that the likely `GTMioShaderBinaryData` path needs a
safe adapter before it can be used in export paths.

## Retraction: the ÷27.75 Kernel Invocations scale

Everything below that describes a 464-byte "sample record", an unsigned 32-bit
Kernel Invocations field at offset `0x0064`, or a `÷ 27.75` scale on it, is
WRONG and has been removed from the code. It is kept here as a record of a
false lead, not as a description of the format.

[V] The divisor was never measured. It was back-fitted from exactly one pair:
raw `28,416` against `1,024` in one Xcode CSV export, and `28,416/1,024 = 27.75`
exactly. No hardware quantity produces `111/4`, and no second observation of the
pair was ever recorded, so the "VALIDATED" marks below were unearned.

[V] The record size that gated it does not occur. Over the first five
`Counters_f_*.raw` of
`/tmp/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace` (~30,000
records) not one record is 464 bytes; the common sizes are 1742, 612, 671 and
8192. The branch therefore produced no metrics on real archives, and since
emission was gated on a non-zero invocation count, nothing downstream of it was
ever displayed. Removing it changed no user-visible value.

Real Kernel Invocations ground truth does exist — the Xcode counter-tab exports
carry per-encoder values such as 8,058 and 11,297 — but no offset in
`Counters_f_*.raw` has been shown to yield them.

## Current Implementation Snapshot

| Metric or field | Current source | Evidence in repo | Remaining gap |
|-----------------|----------------|------------------|---------------|
| Kernel Invocations | RETRACTED — not extracted from `Counters_f_*.raw` at all | see "Retraction: the ÷27.75 Kernel Invocations scale" below | No sourced route from a counter file to this metric. |
| ALU Utilization | Xcode CSV when present; otherwise deterministic `Counters_f_12.raw` extraction and legacy float-range fallback | `ImportCountersCSV`, `counterConfigs`, `extractDeterministicMetrics` | Exact raw float offset is still not known. |
| Kernel Occupancy | `Profiling_f_*.raw` in encoder metric conversion, with counter-file fallback | `ParseProfilingFiles` and `PopulateEncoderMetricsFromPerfCounterStats` | Profiling extraction is heuristic and needs more fixtures. |
| Allocated registers | `streamData` `Temporary register count` | `PipelineStats.TemporaryRegisterCount`, `enhanceFromStreamData`, `applyPipelineStats` | Not a raw counter-file field offset. |
| Spilled bytes | `streamData` `Spilled bytes` | `PipelineStats.SpilledBytes`, `enhanceFromStreamData`, `applyPipelineStats` | Not a raw counter-file field offset. |
| High register | Not currently extracted safely | `docs/research/GTShaderProfiler_BINDING_GAPS.md` | Needs a safe `GTMioShaderBinaryData` or offline shader-binary adapter. |

## Record Structure Analysis

### Record Type Distribution (from 262 total records)

| Size (bytes) | Count | Percentage | Type |
|-------------|-------|------------|------|
| 464 | 87 | 33.2% | **Sample records** (performance metrics) |
| 523 | 27 | 10.3% | Metadata variant |
| 987 | 22 | 8.4% | Metadata variant |
| 516 | 6 | 2.3% | Metadata variant |
| 491 | 3 | 1.1% | Metadata variant |
| 2,300-2,900 | ~20 | ~7.6% | **Metadata records** (encoder identification) |
| Other | ~97 | ~37.0% | Various metadata sizes |

### Sample Record Layout (464 bytes)

```text
Offset    Size  Type     Field Name                 Notes
------    ----  ----     ----------                 -----
0x0000    4     uint32   Record marker              Always 0x4E000000
0x0004    4     uint32   Record type                Varies
0x0008    8     uint64   Pipeline state addr (?)    Hypothesis
...
0x0064    4     uint32   Kernel Invocations         RETRACTED: rawValue / 27.75 (see retraction)
0x0068    ?     ?        Unknown
...
various   4     float32  ALU Utilization            Range: 0.0 - 5.0%
various   4     float32  Kernel Occupancy           Range: 0.0 - 2.0%
various   4     float32  Limiters                   Multiple limiter fields
various   4     float32  Utilizations               Multiple utilization fields
various   4     float32  Cache metrics              Buffer L1 metrics
various   8     uint64   Memory bandwidth           Bytes read/written
```

### Metadata Record Layout (2,300-2,900 bytes)

```text
Offset    Size  Type     Field Name                 Notes
------    ----  ----     ----------                 -----
0x0000    4     uint32   Record marker              Always 0x4E000000
0x01b4    8     uint64   Encoder ID                 Hypothesis (needs validation)
...       ...   ...      Additional encoder metadata
```

## Known Field Offsets

### Confirmed Offsets

| Offset | Size | Type | Field Name | Scaling | Status |
|--------|------|------|------------|---------|--------|
| 0x0064 | 4 | uint32 | Kernel Invocations | ÷ 27.75 | ❌ RETRACTED, removed from code |

### Heuristic Extraction (Float32 Range Search)

These metrics are extracted by scanning the 464-byte record for float32 values in specific ranges:

| Metric | Range | Priority | Uniqueness Strategy |
|--------|-------|----------|---------------------|
| ALU Utilization | 0.0 - 5.0 | High | First match, exclude if > 5.0 |
| Kernel Occupancy | 0.0 - 2.0 | High | First match ≠ ALU Util |
| Buffer L1 Miss Rate | 10.0 - 100.0 | Medium | Higher values preferred |
| Buffer L1 Read Accesses | 10.0 - 100.0 | Medium | After miss rate |
| Buffer L1 Write Accesses | 5.0 - 100.0 | Medium | After read accesses |
| Buffer L1 Read Bandwidth | 0.1 - 15.0 | Low | Smaller values |
| Buffer L1 Write Bandwidth | 0.1 - 10.0 | Low | Smaller values |
| Limiters (various) | 0.001 - 5.0 | Medium | Pattern-based assignment |
| Utilizations (various) | 0.01 - 100.0 | Medium | Exclude other metrics |

## Data Type Reference

| Type | Size | Endianness | Notes |
|------|------|------------|-------|
| uint32 | 4 bytes | Little | Standard integer fields |
| uint64 | 8 bytes | Little | Memory bandwidth, addresses |
| float32 | 4 bytes | Little | Percentages, utilization, limiters |
| Record Marker | 4 bytes | - | Always `0x4E 0x00 0x00 0x00` |

## What's Still Pending

### 1. Exact Raw Binary Field Offsets

**Current State:**
- Record boundaries are identified by the `0x4E 0x00 0x00 0x00` marker.
- Sample records are classified by length (`464` bytes); metadata records are
  classified by length (`2300`-`2900` bytes).
- Encoder groups are sequence-based because the previously suspected metadata
  ID field at `0x01b4` was not unique enough for grouping.
- `Kernel Invocations` is extracted from sample offset `0x0064`.
- Several float and byte metrics are extracted with file-mapped or range-scan
  heuristics when Xcode CSV data is unavailable.

**Needs Implementation:**
The exact raw byte offsets for these fields remain to be determined:
- **HighRegister** - high register index field location or safe binary adapter
  unknown
- **SIMDGroups** - SIMD group count field location unknown in raw counters
- **ALUUtilization** - exact float field offset unknown; current extraction is
  CSV-first, file-mapped, or range-based
- **KernelOccupancy** - exact field offset unknown; current extraction uses
  `Profiling_f_*.raw` and counter fallback heuristics
- **MemoryBandwidth** - exact byte counter offsets unknown for most columns
- **TotalCycles** - cycle count field location unknown

**Implemented direct-offset pattern:**
```go
// In parseCounterRecord():
if len(data) == 464 {
    metrics := &ShaderHardwareMetrics{}

    // Kernel Invocations - offset 0x0064
    if len(data) >= 0x0068 {
        rawValue := binary.LittleEndian.Uint32(data[0x0064:0x0068])
        metrics.ExecutionCount = int(float64(rawValue) / 27.75)
    }

    record.ShaderMetric = metrics
}
```

### 2. Field Offset Discovery Process

**Required Steps:**

1. **Obtain Profiled Trace:**
   ```bash
   # Capture trace with Xcode Instruments Shader Profiler enabled
   # This generates .gputrace + .gpuprofiler_raw directory
   open /Applications/Xcode.app/Contents/Developer/usr/bin/instruments
   ```

2. **Analyze Counter Files:**
   ```bash
   # Examine raw counter data
   hexdump -C trace.gputrace.gpuprofiler_raw/Counters_f_0.raw | less

   # Compare with Instruments output
   gputrace shaders trace.gputrace > our_output.txt
   # Open same trace in Instruments, export GPU data
   diff our_output.txt instruments_output.txt
   ```

3. **Identify Field Patterns:**
   - Look for integer values matching known invocation or SIMD-group counts
   - Look for large values matching SIMD group counts (100s-100000s)
   - Look for percentage values (0.0-100.0 for utilization metrics)
   - Correlate file offsets with known shader configurations

4. **Validate Offsets:**
   ```go
   // Add test cases with known values
   func TestCounterFieldExtraction(t *testing.T) {
       // Use reference trace with known Instruments output
       trace := openTestTrace("reference_profiled.gputrace")
       stats := trace.ParsePerfCounters()

       // Validate against known Instruments values
       assert.Equal(t, 1024, stats.ShaderMetrics[0].ExecutionCount)
       assert.InDelta(t, 3.25, stats.ShaderMetrics[0].ALUUtilization, 0.01)
   }
   ```

## CSV Metric Categories

### 1. Core Execution Metrics

| Column | Metric Name | Data Type | Extraction Method | Notes |
|--------|-------------|-----------|-------------------|-------|
| 70 | Kernel Invocations | uint32 | offset `0x0064` ÷ 27.75 | Scaled value, SIMD-related |
| 71 | Kernel Occupancy | float32 | Range search 0.0-2.0 | Percentage format |
| 13 | ALU Utilization | float32 | Range search 0.0-5.0 | Percentage format |

**Implementation:** See `counter.go:374-396`

```go
// Kernel Invocations - offset 0x0064
rawValue := binary.LittleEndian.Uint32(data[0x0064:0x0068])
metrics.ExecutionCount = int(float64(rawValue) / 27.75)

// ALU Utilization - float32 scan
metrics.ALUUtilization = findFloatInRange(data, 0.0, 5.0)

// Kernel Occupancy - float32 scan
metrics.KernelOccupancy = findFloatInRange(data, 0.0, 2.0)
```

### 2. Memory Bandwidth Metrics

| Column | Metric Name | Data Type | Extraction Method | Notes |
|--------|-------------|-----------|-------------------|-------|
| 24 | Buffer Read Bytes | uint64 | Range search | 1KB - 100KB per sample |
| 25 | Buffer Write Bytes | uint64 | Range search | 1KB - 100KB per sample |
| 26 | Bytes Read From Device Memory | uint64 | Aggregated from samples | Sum across encoder group |
| 27 | Bytes Written To Device Memory | uint64 | Aggregated from samples | Sum across encoder group |

**Implementation:** See `counter.go:400-415`

```go
// Search for byte values in reasonable range
for i := 0; i < len(data)-8; i += 4 {
    val := binary.LittleEndian.Uint64(data[i : i+8])
    if val >= 1000 && val <= 100000 {
        if metrics.BytesReadFromDeviceMemory == 0 {
            metrics.BytesReadFromDeviceMemory = val
        } else if metrics.BytesWrittenToDeviceMemory == 0 {
            metrics.BytesWrittenToDeviceMemory = val
        }
    }
}
```

### 3. Buffer L1 Cache Metrics (gputrace-66)

| Column | Metric Name | Data Type | Value Range | Extraction Method |
|--------|-------------|-----------|-------------|-------------------|
| 23 | Buffer L1 Miss Rate | float32 | 10.0-100.0% | Float search |
| 21 | Buffer L1 Read Accesses | float32 | 10.0-100.0 | Float search |
| 22 | Buffer L1 Read Bandwidth | float32 | 0.1-15.0 GB/s | Float search |
| 24 | Buffer L1 Write Accesses | float32 | 5.0-100.0 | Float search |
| 25 | Buffer L1 Write Bandwidth | float32 | 0.1-10.0 GB/s | Float search |

**Implementation:** See `counter.go:467-492`

```go
// Search for float32 values in cache metric ranges
l1CacheValues := findAllFloatsInRange(data, 0.0, 100.0, 30)

for _, val := range l1CacheValues {
    switch {
    case val >= 10.0 && val <= 100.0 && metrics.BufferL1MissRate == 0:
        metrics.BufferL1MissRate = val
    case val >= 10.0 && val <= 100.0 && metrics.BufferL1ReadAccesses == 0:
        metrics.BufferL1ReadAccesses = val
    // ... additional cases for bandwidth metrics
    }
}
```

### 4. Shader Launch Limiters

| Column | Metric Name | Data Type | Value Range | Typical Values |
|--------|-------------|-----------|-------------|----------------|
| 29 | Compute Shader Launch Limiter | float32 | 0.03-0.10% | 0.03-0.08 |
| 39 | Fragment Shader Launch Limiter | float32 | 0.03-0.10% | Similar |
| 106 | Vertex Shader Launch Limiter | float32 | 0.03-0.10% | Similar |

**Implementation:** See `counter.go:420-465`

```go
// Find limiter candidates in range
limiters := findAllFloatsInRange(data, 0.001, 5.0, 20)

for i, val := range limiters {
    switch {
    case i == 0 && val >= 0.03 && val <= 0.1:
        if metrics.ComputeShaderLaunchLimiter == 0 {
            metrics.ComputeShaderLaunchLimiter = val
        }
    // ... additional limiter assignments
    }
}
```

### 5. Pipeline Limiters

| Column | Metric Name | Data Type | Value Range | Notes |
|--------|-------------|-----------|-------------|-------|
| 31 | Control Flow Limiter | float32 | 0.01-2.0% | |
| 51 | Instruction Throughput Limiter | float32 | 0.05-0.1% | |
| 52 | Integer and Complex Limiter | float32 | 1.0-2.0% | |
| 53 | Integer and Conditional Limiter | float32 | 1.0-2.0% | |
| 54 | F16 Limiter | float32 | 0.01-5.0% | |
| 55 | F32 Limiter | float32 | 2.0-4.0% | Up to 3.74 for complex shaders |

### 6. Memory System Limiters

| Column | Metric Name | Data Type | Value Range |
|--------|-------------|-----------|-------------|
| 56 | L1 Cache Limiter | float32 | 0.01-0.15% |
| 57 | Last Level Cache Limiter | float32 | 0.01-0.15% |
| 58 | MMU Limiter | float32 | 0.02-0.04% |

### 7. Texture Limiters

| Column | Metric Name | Data Type | Value Range |
|--------|-------------|-----------|-------------|
| 92 | Texture Filtering Limiter | float32 | 0.01-0.04% |
| 93 | Texture Write Limiter | float32 | 0.01-0.04% |
| 94 | Texture Read Limiter | float32 | 0.01-0.04% |

### 8. Shader Utilization Metrics (gputrace-67)

| Column | Metric Name | Data Type | Value Range | Complementary To |
|--------|-------------|-----------|-------------|------------------|
| 30 | Compute Shader Utilization | float32 | 0.01-5.0% | Compute Limiter |
| 40 | Fragment Shader Utilization | float32 | 0.01-5.0% | Fragment Limiter |
| 107 | Vertex Shader Utilization | float32 | 0.01-5.0% | Vertex Limiter |
| 32 | Control Flow Utilization | float32 | 0.01-2.0% | Control Flow Limiter |
| 59 | Instruction Throughput Util | float32 | 0.01-5.0% | Instruction Limiter |
| 60 | Integer and Complex Util | float32 | 0.01-5.0% | Integer Complex Limiter |
| 61 | Integer and Conditional Util | float32 | 0.01-5.0% | Integer Conditional Limiter |
| 62 | F16 Utilization | float32 | 0.01-5.0% | F16 Limiter |
| 63 | F32 Utilization | float32 | 0.01-5.0% | F32 Limiter |

**Implementation:** See `counter.go:494-538`

```go
// Utilization metrics complement limiters
utilizationValues := findAllFloatsInRange(data, 0.0, 100.0, 30)

for _, val := range utilizationValues {
    // Skip values already assigned to other metrics
    if val == metrics.ALUUtilization || val == metrics.KernelOccupancy { continue }

    switch {
    case val >= 0.01 && val <= 5.0 && metrics.ComputeShaderUtilization == 0:
        metrics.ComputeShaderUtilization = val
    // ... additional utilization assignments
    }
}
```

### 9. Last Level Cache Metrics

| Column | Metric Name | Data Type | Extraction Method |
|--------|-------------|-----------|-------------------|
| 73 | Last Level Cache Bytes Read | uint64 | Aggregated from samples |
| 74 | Last Level Cache Bytes Written | uint64 | Aggregated from samples |
| 75 | Last Level Cache Bandwidth | float64 | Calculated: (Read + Write) |
| 76 | Last Level Cache Miss Rate | float32 | Float search 0.0-100.0 |

## Complete Non-Zero Metric List (58 metrics)

Based on analysis of `testdata/traces/06-six-encoders/06-six-encoders-run1 Counters.csv`:

### Execution & Performance
1. ALU Utilization (col 9)
2. Kernel Invocations (col 103)
3. Kernel Occupancy (col 104)
4. Kernel ALU Float Instructions (col 97)
5. Kernel ALU Instructions (col 99)
6. Kernel ALU Integer and Complex Instructions (col 100)
7. Kernel ALU Integer and Conditional Instructions (col 101)
8. Kernel ALU Performance (col 102)

### Memory & Bandwidth
9. Buffer Device Memory Bytes Read (col 18)
10. Buffer Device Memory Bytes Written (col 19)
11. Bytes Read From Device Memory (col 25)
12. Bytes Written To Device Memory (col 26)
13. Device Memory Bandwidth (col 50)
14. GPU Read Bandwidth (col 85)
15. GPU Write Bandwidth (col 86)

### Buffer L1 Cache
16. Buffer L1 Miss Rate (col 20)
17. Buffer L1 Read Accesses (col 21)
18. Buffer L1 Read Bandwidth (col 22)
19. Buffer L1 Write Accesses (col 23)
20. Buffer L1 Write Bandwidth (col 24)

### Last Level Cache
21. Last Level Cache Bandwidth (col 120)
22. Last Level Cache Bytes Read (col 121)
23. Last Level Cache Bytes Written (col 122)
24. Last Level Cache Limiter (col 123)
25. Last Level Cache Miss Rate (col 124)
26. Last Level Cache Utilization (col 125)

### L1 Cache
27. L1 Buffer Residency (col 106)
28. L1 Cache Limiter (col 107)
29. L1 Cache Utilization (col 108)
30. L1 Other Residency (col 111)
31. L1 Read Bandwidth (col 114)
32. L1 Total Residency (col 118)
33. L1 Write Bandwidth (col 119)

### Shader Launch Limiters
34. Compute Shader Launch Limiter (col 30)
35. Compute Shader Launch Utilization (col 31)

### Pipeline Limiters & Utilization
36. Control Flow Limiter (col 32)
37. Control Flow Utilization (col 33)
38. Instruction Throughput Limiter (col 91)
39. Instruction Throughput Utilization (col 92)
40. Integer and Complex Limiter (col 93)
41. Integer and Complex Utilization (col 94)
42. Integer and Conditional Limiter (col 95)
43. Integer and Conditional Utilization (col 96)
44. F32 Limiter (col 53)
45. F32 Utilization (col 54)

### MMU & Memory Management
46. MMU Limiter (col 132)
47. MMU TLB Miss Rate (col 133)
48. MMU Utilization (col 134)

### Occupancy Manager
49. Occupancy Manager Target (col 139)
50. Occupancy Manager Target (col 140) - duplicate

### Other L1 Metrics
51. Other L1 Read Accesses (col 141)
52. Other L1 Read Accesses (col 142) - duplicate
53. Other L1 Write Accesses (col 143)
54. Other L1 Write Accesses (col 144) - duplicate

### Texture Operations
55. Predicated Texture Thread Reads (col 153)
56. Predicated Texture Thread Writes (col 154)
57. Texture Write Limiter (col 205)
58. Texture Write Utilization (col 206)

## Aggregation Strategy

Performance counter data requires aggregation across multiple sample records within an encoder group:

### 1. Encoder Grouping

```go
// Records are grouped by encoder context
// 1. Metadata record (2.3-2.9 KB) identifies encoder
// 2. Following sample records (464 bytes) belong to that encoder
// 3. New metadata record starts new encoder group

type EncoderGroup struct {
    EncoderID      uint64
    MetadataRecord *CounterRecord
    SampleRecords  []*CounterRecord
}
```

### 2. Aggregation Rules

| Metric Type | Aggregation | Example |
|------------|-------------|---------|
| Kernel Invocations | **FIRST** | Deterministic per encoder; use first non-zero sample |
| ALU Utilization | **AVERAGE** | Mean of non-zero samples |
| Kernel Occupancy | **AVERAGE** | Mean of non-zero samples |
| Memory Bandwidth | **SUM** | Total bytes read + written |
| Limiters | **FIRST** or **MAX** | Typically same across samples |
| Utilizations | **FIRST** or **AVERAGE** | Typically same across samples |

**Implementation:** See `counter.go:629-696`

```go
func aggregateEncoderMetrics(group *EncoderGroup) *ShaderHardwareMetrics {
    var firstInvocations int
    var invocationsSet bool
    var totalALUUtil float64
    var aluSamples int

    for _, record := range group.SampleRecords {
        metrics := record.ShaderMetric

        // First: Kernel Invocations are deterministic within an encoder
        if !invocationsSet && metrics.ExecutionCount > 0 {
            firstInvocations = metrics.ExecutionCount
            invocationsSet = true
        }

        // Average: ALU Utilization
        if metrics.ALUUtilization > 0 {
            totalALUUtil += metrics.ALUUtilization
            aluSamples++
        }
    }

    aggregated.ExecutionCount = firstInvocations
    if aluSamples > 0 {
        aggregated.ALUUtilization = totalALUUtil / float64(aluSamples)
    }

    return aggregated
}
```

## What's Complete ✅

### 1. Core Data Structures (`internal/counter`)

```go
// Comprehensive metrics container
type ShaderHardwareMetrics struct {
    ShaderName       string  // Shader/kernel function name
    PipelineState    uint64  // Pipeline state object address
    SIMDGroups       int     // Number of SIMD groups executed
    AllocatedRegs    int     // Number of allocated registers
    HighRegister     int     // Highest register used
    SpilledBytes     int     // Bytes spilled to memory
    ALUUtilization   float64 // ALU utilization percentage (0-100)
    KernelOccupancy  float64 // Kernel occupancy percentage (0-100)
    MemoryBandwidth  uint64  // Memory bandwidth used (bytes)
    ExecutionCount   int     // Number of times this shader executed
    TotalCycles      uint64  // Total GPU cycles spent
}

// Overall statistics container
type PerfCounterStats struct {
    DispatchCount    int
    TotalRecords     int
    FilesProcessed   int
    ConfidenceLevel  float64
    ShaderMetrics    []ShaderHardwareMetrics
}

// Individual record representation
type CounterRecord struct {
    Offset       int64
    RecordType   uint32
    RecordSize   uint32
    Data         []byte
    ShaderMetric *ShaderHardwareMetrics
}
```

### 2. Parsing Infrastructure

**File Discovery and Processing:**
- `ParsePerfCounters()` - Main entry point for parsing `.gpuprofiler_raw` directory
- `parseCounterFileWithMetrics()` - Parse individual Counters_f_*.raw files
- `findRecordBoundaries()` - Locate all 0x4E markers delimiting records
- `parseCounterRecord()` - Extract data from individual records

**Metrics Management:**
- Aggregates metrics across multiple counter files
- Groups metrics by pipeline state address
- Handles metric merging for same shader across files
- Tracks execution counts and accumulates spill bytes

**Shader Correlation:**
- `correlateShaderNames()` - Match pipeline state addresses to shader names
- Uses command buffer analysis to extract encoder labels
- Automatic fallback to pipeline state address when name unavailable

### 3. Public API

**Query Functions:**
```go
// Check if trace has performance counter data
func (t *Trace) HasPerfCounters() bool

// Get all hardware metrics
func (t *Trace) ParsePerfCounters() (*PerfCounterStats, error)

// Get register data by pipeline state
func (t *Trace) GetRegisterDataForShader(pipelineStateAddr uint64) (allocatedRegs, highRegister, spilledBytes int, found bool)

// Get register data by shader name
func (t *Trace) GetRegisterDataByName(shaderName string) (allocatedRegs, highRegister, spilledBytes int, found bool)

// Get method description for counting
func (t *Trace) GetDispatchCountMethod() string
```

### 4. Integration

**Shader Metrics Integration:**
- `FormatShadersXcodeStyle()` uses real register data when available
- Missing hardware counter/register fields remain absent or source-labelled;
  heuristic/synthetic timing paths report `TimingSource` and `TimingApprox`
- `formatSpilledBytes()` helper for human-readable output

**CLI Command:**
```bash
gputrace shaders trace.gputrace
```

### 5. Documentation

**Binary Format Documentation (`internal/counter`):**
```go
// Try to extract shader metrics if this looks like a shader performance record
// Based on APS (Apple Performance Streaming) format discovered in GPUToolsReplayService
//
// The performance counter records contain hardware metrics collected by AGXGPURawCounter
// during shader execution. Key fields include:
// - SIMD group count (threadgroups executed)
// - Register allocation (number of registers allocated per thread)
// - High register (highest register index used)
// - Spilled bytes (register spills to memory)
// - ALU utilization, memory bandwidth, occupancy, etc.
//
// Format varies by record type and GPU architecture, but common patterns:
// - Record marker: 0x4E 0x00 0x00 0x00 at offset 0
// - Record type at offset 0x04 (varies by metric)
// - Pipeline state address typically in first 32 bytes
// - SIMD group counts often at fixed offsets for compute dispatch records
// - Register counts in shader-specific performance records
```

**Reference Documentation:**
- [GPU_PROFILING_APIS_DISCOVERED.md](./GPU_PROFILING_APIS_DISCOVERED.md) - Complete APS/AGXGPURawCounter reverse engineering
- Documents IOReport framework, Apple Performance Streaming architecture
- Details ring buffer implementation and data flow
- Provides workflow diagrams and time budgets

## Implementation Readiness

### Production Ready ✅

**These components can be used now:**
- `HasPerfCounters()` - Detection works
- `ParsePerfCounters()` - Framework complete and fail-closed for missing or
  invalid `.gpuprofiler_raw` counter records
- `GetRegisterDataForShader()` - API ready (returns false until fields extracted)
- `correlateShaderNames()` - Correlation works
- Shader metrics integration - Uses parsed counters and streamData where
  available; heuristic or synthetic timing is explicitly source-labelled

### Requires More Validated Fixtures

**These require additional `.gpuprofiler_raw` analysis or a safe Xcode adapter:**
- Exact raw offsets for ALU utilization, occupancy, memory bandwidth, SIMD
  groups, and cycle counts
- High-register extraction from `GTMioShaderBinaryData` or an offline shader
  binary decoder
- GPU-family validation for M3/M4 and later

## Validation Approach

### 1. Known Value Correlation

Compare extracted values with Xcode Instruments CSV:

```bash
# Generate our CSV
./gputrace export-counters trace.gputrace > our_output.csv

# Compare with Xcode export
diff our_output.csv xcode_instruments_export.csv
```

### 2. Field Offset Validation

For offset `0x0064` (Kernel Invocations):

```python
import struct

# Read counter file
with open('Counters_f_0.raw', 'rb') as f:
    data = f.read()

# Find record at offset
record = data[offset:offset+464]

# Extract value
raw_value = struct.unpack('<I', record[0x64:0x68])[0]
invocations = int(raw_value / 27.75)

print(f"Raw: {raw_value}, Scaled: {invocations}")
# Expected: Raw: 28416, Scaled: 1024 (matches CSV)
```

### 3. Aggregation Validation

Verify that aggregated values across multiple sample records match CSV:

```go
// Test case
func TestKernelInvocationsAggregation(t *testing.T) {
    trace := openTestTrace("06-six-encoders-run1-perf.gputrace")
    stats, _ := counter.ParsePerfCounters(trace)

    // Should match CSV row 1 value
    assert.Equal(t, 1024, stats.ShaderMetrics[0].ExecutionCount)
}
```

## Testing Strategy

### Unit Tests

```go
// TestPerfCounterParsing - Test basic parsing
func TestPerfCounterParsing(t *testing.T) {
    trace := openTestTrace("profiled.gputrace")
    assert.True(t, trace.HasPerfCounters())

    stats, err := trace.ParsePerfCounters()
    assert.NoError(t, err)
    assert.True(t, stats.FilesProcessed > 0)
}

// TestRegisterDataExtraction - Test field extraction
func TestRegisterDataExtraction(t *testing.T) {
    trace := openTestTrace("profiled.gputrace")
    alloc, high, spill, found := trace.GetRegisterDataByName("test_shader")
    assert.True(t, found)
    assert.InRange(t, alloc, 4, 256)
}

// TestShaderCorrelation - Test name matching
func TestShaderCorrelation(t *testing.T) {
    trace := openTestTrace("profiled.gputrace")
    stats, _ := trace.ParsePerfCounters()

    for _, metric := range stats.ShaderMetrics {
        assert.NotEmpty(t, metric.ShaderName)
    }
}
```

### Integration Tests

```bash
# Test with real Instruments profiled trace
gputrace export-counters test.gputrace > output.csv
# Compare with Instruments export
gputrace perfcounters-validate test.gputrace expected_instruments_counters.csv
```

## Architecture Considerations

### GPU Family Differences

Counter file format may vary by Apple Silicon generation:

| GPU Family | Chips | Notes |
|-----------|-------|-------|
| AGX G13 | M1, M2 | Original format |
| AGX G15 | M3 | May have format variations |
| AGX G16 | M4 | Newest generation |

**Current Status:** Implementation tested on M1/M2. M3/M4 validation pending.

**Future Work:** Add GPU family detection and variant parsers if needed.

```go
func parseCounterRecord(data []byte, gpuFamily string) *CounterRecord {
    switch gpuFamily {
    case "AGX G13": // M1, M2
        return parseCounterRecordG13(data)
    case "AGX G15": // M3
        return parseCounterRecordG15(data)
    case "AGX G16": // M4
        return parseCounterRecordG16(data)
    }
}
```

## Usage Examples

### Current Usage

```bash
$ gputrace shaders trace.gputrace
Cost    Name                      # Allocated Registers   Spilled Bytes
12.12%  block_softmax_float32     44                      0 bytes
```

When streamData is available, allocated registers and spilled bytes come from
`pipelinePerformanceStatistics`. The `High Register` column is still not backed
by a safe source-specific extraction path.

### Future Usage (With High Register Adapter)

```bash
$ gputrace shaders profiled_trace.gputrace
Cost    Name                      # Allocated Registers   High Register
12.12%  block_softmax_float32     162                     182
```

### Programmatic Access

```go
trace := gputrace.Open("profiled.gputrace")

// Check if counter data available
if trace.HasPerfCounters() {
    // Get full statistics
    stats, _ := trace.ParsePerfCounters()

    for _, metric := range stats.ShaderMetrics {
        fmt.Printf("%s: %d registers, %d spilled bytes\n",
            metric.ShaderName,
            metric.AllocatedRegs,
            metric.SpilledBytes)
    }

    // Query specific shader
    alloc, high, spill, found := trace.GetRegisterDataByName("my_shader")
    if found {
        fmt.Printf("Allocated: %d, High: %d, Spilled: %d bytes\n",
            alloc, high, spill)
    }
}
```

## Next Steps

### Immediate (P1)

1. **Add checked-in or fetchable profiler fixtures:**
   - Include Xcode CSV ground truth separately from generated raw traces
   - Record GPU model, Xcode version, and capture command
   - Keep raw trace dumps out of the repo unless intentionally added as fixtures

2. **Validate current extractors:**
   - Compare `Kernel Invocations` offset `0x0064` against CSV on each fixture
   - Validate `Profiling_f_*.raw` occupancy against CSV
   - Track whether file-mapped metrics remain stable across GPU families

3. **Implement only evidence-backed new offsets:**
   - Add offset constants after a CSV-backed fixture proves the location
   - Keep range-scan metrics labelled as heuristic
   - Do not report high-register values as source-backed until the adapter is safe

### Future (P2)

4. **Architecture Detection:**
   - Add GPU family detection
   - Implement variant parsers if needed
   - Test across M1/M2/M3/M4

5. **Comprehensive Metrics:**
   - Exact ALU utilization offset
   - Exact memory bandwidth offsets
   - Source-backed high-register extraction

6. **Performance Optimization:**
   - Memory-efficient parsing for large counter files
   - Incremental parsing for streaming analysis
   - Caching for repeated queries


## References

### Code

- `internal/counter/counter.go` - counter record parsing and field extraction
- `internal/counter/execution_cost.go` - `Profiling_f_*.raw` execution cost
- `internal/counter/timeline.go` - `Timeline_f_*.raw` header parsing
- `internal/shader/metrics.go` - shader metric assembly

### Documentation

- [BINARY_FORMAT_REFERENCE.md](./BINARY_FORMAT_REFERENCE.md) - counter binary format
- [FIELD_OFFSET_QUICK_REFERENCE.md](./FIELD_OFFSET_QUICK_REFERENCE.md) - field lookup shortcuts
- [COUNTER_FILE_MAPPING.md](./COUNTER_FILE_MAPPING.md) - counter file mapping
- [XDIC_INDEX_FORMAT.md](./XDIC_INDEX_FORMAT.md) - capture bundle and index format
- [../STREAMDATA_FORMAT.md](../STREAMDATA_FORMAT.md) - profiler `streamData` layouts
