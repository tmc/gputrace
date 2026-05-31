# Performance Counter Parsing Status

**Date:** 2026-05-31
**Status:** Core Field Extraction Active, Exact Raw Offsets Still Limited

## Overview

The performance counter parsing framework is no longer only scaffolding. Current
`internal/counter` code parses `.gpuprofiler_raw` counter records, extracts the
validated `Kernel Invocations` field at offset `0x0064`, applies file-mapped
counter extraction for selected metrics, optionally imports Xcode CSV data as
ground truth, and enriches shader metrics with compilation statistics from
`streamData`.

Important boundary: register allocation and spill counts are currently sourced
from `streamData` `pipelinePerformanceStatistics`, not from direct
`Counters_f_*.raw` field offsets. `HighRegister` remains a real gap; the current
binding-gap note records that the likely `GTMioShaderBinaryData` path needs a
safe adapter before it can be used in export paths.

## Current Implementation Snapshot

| Metric or field | Current source | Evidence in repo | Remaining gap |
|-----------------|----------------|------------------|---------------|
| Kernel Invocations | `Counters_f_*.raw` sample record offset `0x0064`, scaled by `27.75` | `parseCounterRecord` and `aggregateEncoderMetrics` in `internal/counter/counter.go` | Scaling is validated for existing analysis traces, but still needs broader GPU-family validation. |
| ALU Utilization | Xcode CSV when present; otherwise deterministic `Counters_f_12.raw` extraction and legacy float-range fallback | `ImportCountersCSV`, `counterConfigs`, `extractDeterministicMetrics` | Exact raw float offset is still not known. |
| Kernel Occupancy | `Profiling_f_*.raw` in encoder metric conversion, with counter-file fallback | `ParseProfilingFiles` and `PopulateEncoderMetricsFromPerfCounterStats` | Profiling extraction is heuristic and needs more fixtures. |
| Allocated registers | `streamData` `Temporary register count` | `PipelineStats.TemporaryRegisterCount`, `enhanceFromStreamData`, `applyPipelineStats` | Not a raw counter-file field offset. |
| Spilled bytes | `streamData` `Spilled bytes` | `PipelineStats.SpilledBytes`, `enhanceFromStreamData`, `applyPipelineStats` | Not a raw counter-file field offset. |
| High register | Not currently extracted safely | `docs/research/GTShaderProfiler_BINDING_GAPS.md` | Needs a safe `GTMioShaderBinaryData` or offline shader-binary adapter. |

## What's Complete ✅

### 1. Core Data Structures (perfcounters.go)

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
- Automatic fallback to estimates with "(est)" marker
- `formatSpilledBytes()` helper for human-readable output

**CLI Command:**
```bash
gputrace perfcounters trace.gputrace
```

### 5. Documentation

**Binary Format Documentation (perfcounters.go lines 172-191):**
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

### 3. Architecture-Specific Handling

Counter file format may vary by GPU:
- M1/M2 (AGX G13)
- M3 (AGX G15)
- M4 (AGX G16)

May need GPU detection:
```go
func parseCounterRecord(data []byte, offset int64, gpuFamily string) *CounterRecord {
    switch gpuFamily {
    case "AGX G13": // M1, M2
        return parseCounterRecordG13(data, offset)
    case "AGX G15": // M3
        return parseCounterRecordG15(data, offset)
    case "AGX G16": // M4
        return parseCounterRecordG16(data, offset)
    }
}
```

## Implementation Readiness

### Production Ready ✅

**These components can be used now:**
- `HasPerfCounters()` - Detection works
- `ParsePerfCounters()` - Framework complete
- `GetRegisterDataForShader()` - API ready (returns false until fields extracted)
- `correlateShaderNames()` - Correlation works
- Shader metrics integration - Falls back gracefully to estimates

### Requires More Validated Fixtures

**These require additional `.gpuprofiler_raw` analysis or a safe Xcode adapter:**
- Exact raw offsets for ALU utilization, occupancy, memory bandwidth, SIMD
  groups, and cycle counts
- High-register extraction from `GTMioShaderBinaryData` or an offline shader
  binary decoder
- GPU-family validation for M3/M4 and later

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
gputrace perfcounters test.gputrace > output.txt
# Compare with Instruments export
diff output.txt expected_instruments_output.txt
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

**Documentation:**
- [GPU_PROFILING_APIS_DISCOVERED.md](./GPU_PROFILING_APIS_DISCOVERED.md) - APS/AGXGPURawCounter reverse engineering
- [TRACE_FORMAT.md](./TRACE_FORMAT.md) - .gputrace file format documentation

**Code:**
- `internal/counter/counter.go` - Main implementation
- `internal/shader/metrics.go` - Integration with shader analysis
- `cmd/gputrace/cmd/perfcounters_validate.go` - CLI validation command

**Apple Frameworks:**
- `/System/Library/Extensions/AGXMetalA*.bundle/` - GPU counter implementation
- `/System/Library/PrivateFrameworks/GPUToolsReplay.framework/` - Replay infrastructure
- `IOKit.framework` - IOReport public API

## Summary

**The infrastructure is complete and production-ready.** The framework correctly:
- Detects performance counter files
- Parses record boundaries
- Extracts pipeline state addresses
- Correlates with shader names
- Provides clean API

**Additional field extraction requires evidence.** The repo already contains
kernel invocation extraction and streamData-backed register/spill extraction,
but exact offsets beyond `0x0064` should only be promoted after fixture-backed
CSV validation.

**Zero breaking changes.** All code gracefully handles missing counter data, falling back to estimates with clear "(est)" markers.

**Ready for immediate use with known limits.** Detection, correlation, CSV
enhancement, deterministic counter-file mapping, and streamData enrichment work
now. Missing high-register and exact raw-offset values should remain visible as
gaps rather than being silently filled from unsupported guesses.
