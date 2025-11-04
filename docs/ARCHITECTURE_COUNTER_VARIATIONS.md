# GPU Architecture Counter Format Variations

**Bead:** gputrace-49
**Date:** 2025-11-03
**Status:** Active Research

## Overview

This document analyzes performance counter format variations across Apple Silicon GPU architectures to enable robust parsing across M1, M2, M3, and M4 generations.

## Apple Silicon GPU Architecture Evolution

### Architecture Summary

| Chip Family | GPU Architecture | Generation | Key Features | Released |
|--------------|-----------------|------------|--------------|----------|
| **M1, M1 Pro, M1 Max, M1 Ultra** | AGX G13 (G13G/G13X) | 4th Gen | Base Apple Silicon GPU | 2020-2022 |
| **M2, M2 Pro, M2 Max, M2 Ultra** | AGX G14 | 5th Gen | Improved performance, power efficiency | 2022-2023 |
| **M3, M3 Pro, M3 Max** | AGX G15/G16* | 6th Gen | Hardware ray tracing, mesh shading, dynamic caching | 2023 |
| **M4, M4 Pro, M4 Max** | AGX G16 | 6th Gen+ | Enhanced ray tracing, improved efficiency | 2024 |

*Note: M3 was originally planned as G15 but released as G16. The G15 designation was suspended during A16 development.

### Architecture Details

#### AGX G13 (M1 Family)
- **Used in**: M1, M1 Pro, M1 Max, M1 Ultra
- **Variants**:
  - G13G (base M1)
  - G13X (M1 Pro/Max/Ultra)
- **Cores**: 7-8 (M1), 14-16 (Pro), 24-32 (Max), 48-64 (Ultra)
- **Clock**: ~1.278 GHz
- **Peak Performance**: 2.6 TFLOPS (M1 8-core)
- **Key Characteristics**:
  - First generation Apple Silicon GPU
  - Unified memory architecture
  - No hardware ray tracing

#### AGX G14 (M2 Family)
- **Used in**: M2, M2 Pro, M2 Max, M2 Ultra
- **Cores**: 8-10 (M2), 16-19 (Pro), 30-38 (Max), 60-76 (Ultra)
- **Clock**: ~1.398 GHz
- **Peak Performance**: 3.6 TFLOPS (M2 10-core)
- **Improvements over G13**:
  - ~18% higher clock speed
  - Improved power efficiency
  - Enhanced memory bandwidth
  - Architectural refinements

#### AGX G15/G16 (M3 Family)
- **Used in**: M3, M3 Pro, M3 Max
- **Cores**: 10 (M3), 14-18 (Pro), 30-40 (Max)
- **Clock**: Variable (dynamic caching)
- **Major New Features**:
  - **Hardware ray tracing acceleration** (first Apple GPU)
  - **Mesh shading support**
  - **Dynamic Caching** - real-time memory allocation optimization
  - **MetalFX upscaling** with GPU/Neural Engine cooperation
- **Performance**: Desktop-class gaming capabilities on Mac

#### AGX G16 (M4 Family)
- **Used in**: M4, M4 Pro, M4 Max
- **Cores**: 10 (M4), 16-20 (Pro), 32-40 (Max)
- **Clock**: Enhanced frequency scaling
- **Peak Performance**: 2.9 TFLOPS (M4 10-core)
- **Improvements over M3**:
  - Enhanced ray tracing performance
  - Improved power efficiency
  - Better thermal management
  - Refined dynamic caching

## Performance Counter Format Analysis

### Known Information

Based on analysis from `/tmp/fast-llm-mlx-test-perf.gputrace` (captured on M4 Max):

#### Record Structure (M4 Max - AGX G16)

**Confirmed record types:**
1. **Metadata records**: 2,300-2,900 bytes
   - Contain encoder identification
   - Frame/timing context
   - Aggregation group markers

2. **Sample records**: 464 bytes (0x1D0)
   - Per-sample performance metrics
   - ~160 samples per CSV row
   - Require aggregation to match Xcode output

**Record marker:** `0x4E 0x00 0x00 0x00` (uint32 = 78)

#### Aggregation Requirements

```
Binary Records (M4 Max):
- Counters_f_0.raw: 1,598 records
- 40 total files
- Total records: ~64,000

CSV Output:
- 10 rows (one per encoder/shader)

Aggregation Ratio: ~160 records per CSV row
```

### Architecture-Specific Variations (Hypothesis)

#### Potential Variation Points

Based on GPU architecture differences, counter format may vary in:

1. **Record Size**
   - M1/M2 (G13/G14): Possibly 464 bytes (to be confirmed)
   - M3 (G15/G16): Possibly 464 bytes (to be confirmed)
   - M4 (G16): **Confirmed 464 bytes**

2. **Field Offsets**
   - Kernel Invocations: Candidate offsets 0x0064, 0x0100 (M4)
   - ALU Utilization: Float field location (TBD)
   - Occupancy: Float field location (TBD)
   - Ray tracing counters: Only present in M3/M4 (G15/G16)

3. **Available Metrics**
   - **M1/M2**: ~200-220 metrics (no ray tracing)
   - **M3/M4**: ~241+ metrics (includes ray tracing, mesh shading)

4. **Metadata Record Structure**
   - Encoder ID field: Candidate offset 0x01b4 (M4)
   - May differ across architectures

### Ray Tracing Counter Additions (M3/M4 Only)

The M3 and M4 GPUs (AGX G15/G16) include hardware ray tracing, which adds new performance counters:

**Expected New Metrics:**
- Ray Intersection Tests
- Ray Triangle Intersection Count
- Ray Box Intersection Count
- BVH Traversal Depth
- Ray Tracing Occupancy
- RT Core Utilization
- Acceleration Structure Memory Usage

These counters will **not exist** in M1/M2 traces, affecting:
- Total counter count (200-220 vs 241+)
- CSV column mapping
- Binary field offsets (if counters are packed sequentially)

### Mesh Shading Counter Additions (M3/M4 Only)

M3/M4 mesh shading support may introduce:
- Meshlet Count
- Mesh Shader Invocations
- Mesh Shader Occupancy
- Amplification Shader Statistics

## Implementation Strategy

### Current Implementation (M4-Only)

From `perfcounters.go:287-343`:

```go
func parseCounterRecord(data []byte, offset int64) *CounterRecord {
    // ... record type detection ...

    if len(data) >= 2300 && len(data) <= 2900 {
        record.IsMetadata = true
        // Encoder ID at offset 0x01b4 (M4 specific?)
        if len(data) >= 0x01b8 {
            record.EncoderID = binary.LittleEndian.Uint64(data[0x01b4:0x01bc])
        }
    } else if len(data) == 464 {
        record.IsMetadata = false
        // Sample metrics
        metrics := &ShaderHardwareMetrics{}

        // Kernel Invocations - offset 0x0064 (M4 specific?)
        if len(data) >= 0x0068 {
            metrics.ExecutionCount = int(binary.LittleEndian.Uint32(data[0x0064:0x0068]))
        }

        // ALU/Occupancy - float field scanning (architecture-agnostic)
        if aluUtil := findPercentageField(data, 0.95, 1.0); aluUtil >= 0 {
            metrics.ALUUtilization = aluUtil * 100
        }
        // ...
    }
}
```

**Known Issues:**
- Hardcoded offsets (0x01b4, 0x0064) may not work on M1/M2/M3
- No architecture detection
- No fallback logic

### Recommended Multi-Architecture Implementation

#### Step 1: Detect GPU Architecture

```go
// GPUArchitecture represents the GPU generation
type GPUArchitecture int

const (
    ArchUnknown GPUArchitecture = iota
    ArchG13  // M1 family
    ArchG14  // M2 family
    ArchG15  // M3 family (transitional)
    ArchG16  // M3/M4 family
)

// DetectGPUArchitecture determines the GPU architecture from trace metadata
func (t *Trace) DetectGPUArchitecture() GPUArchitecture {
    // Method 1: Check for ray tracing counters (M3/M4 only)
    if t.HasRayTracingCounters() {
        return ArchG16 // M3 or M4
    }

    // Method 2: Read device info from trace metadata
    // (if available in .gputrace bundle)
    if deviceInfo := t.ReadDeviceInfo(); deviceInfo != "" {
        if strings.Contains(deviceInfo, "M4") {
            return ArchG16
        } else if strings.Contains(deviceInfo, "M3") {
            return ArchG16
        } else if strings.Contains(deviceInfo, "M2") {
            return ArchG14
        } else if strings.Contains(deviceInfo, "M1") {
            return ArchG13
        }
    }

    // Method 3: Heuristic based on counter count
    counterCount := t.CountTotalMetrics()
    if counterCount >= 241 {
        return ArchG16 // M3/M4 with ray tracing
    } else if counterCount >= 200 {
        return ArchG13 // M1/M2
    }

    return ArchUnknown
}
```

#### Step 2: Architecture-Specific Parsing

```go
// parseCounterRecordWithArch parses a counter record for a specific architecture
func parseCounterRecordWithArch(data []byte, offset int64, arch GPUArchitecture) *CounterRecord {
    if len(data) < 16 {
        return nil
    }

    record := &CounterRecord{
        Offset: offset,
        Data:   data,
    }

    // Detect record type (size-based, likely consistent across architectures)
    recordSize := len(data)

    // Metadata record detection
    if recordSize >= 2300 && recordSize <= 2900 {
        record.IsMetadata = true
        record.EncoderID = extractEncoderID(data, arch)
        return record
    }

    // Sample record detection (464 bytes confirmed for M4, assume same for others)
    if recordSize == 464 {
        record.IsMetadata = false
        record.ShaderMetric = extractShaderMetrics(data, arch)
        return record
    }

    return record
}

// extractEncoderID extracts encoder ID using architecture-specific offsets
func extractEncoderID(data []byte, arch GPUArchitecture) uint64 {
    var encoderIDOffset int

    switch arch {
    case ArchG13: // M1
        encoderIDOffset = 0x01b4 // Hypothesis: same as M4
    case ArchG14: // M2
        encoderIDOffset = 0x01b4 // Hypothesis: same as M4
    case ArchG15, ArchG16: // M3/M4
        encoderIDOffset = 0x01b4 // Confirmed for M4
    default:
        encoderIDOffset = 0x01b4 // Default fallback
    }

    if len(data) >= encoderIDOffset + 8 {
        return binary.LittleEndian.Uint64(data[encoderIDOffset:encoderIDOffset+8])
    }

    return 0
}

// extractShaderMetrics extracts performance metrics using architecture-specific logic
func extractShaderMetrics(data []byte, arch GPUArchitecture) *ShaderHardwareMetrics {
    metrics := &ShaderHardwareMetrics{}

    // Kernel Invocations
    invocationOffset := getInvocationOffset(arch)
    if len(data) >= invocationOffset + 4 {
        metrics.ExecutionCount = int(binary.LittleEndian.Uint32(data[invocationOffset:invocationOffset+4]))
    }

    // ALU Utilization (likely architecture-agnostic via field scanning)
    if aluUtil := findPercentageField(data, 0.90, 1.0); aluUtil >= 0 {
        metrics.ALUUtilization = aluUtil * 100
    }

    // Occupancy (likely architecture-agnostic via field scanning)
    if occupancy := findPercentageField(data, 0.20, 1.50); occupancy >= 0 {
        metrics.KernelOccupancy = occupancy * 100
    }

    // Ray tracing metrics (M3/M4 only)
    if arch == ArchG15 || arch == ArchG16 {
        metrics.RayTracingStats = extractRayTracingMetrics(data)
    }

    return metrics
}

// getInvocationOffset returns the kernel invocation field offset for each architecture
func getInvocationOffset(arch GPUArchitecture) int {
    switch arch {
    case ArchG13: // M1
        return 0x0064 // Hypothesis: same as M4 (to be validated)
    case ArchG14: // M2
        return 0x0064 // Hypothesis: same as M4 (to be validated)
    case ArchG15, ArchG16: // M3/M4
        return 0x0064 // Candidate offset from M4 analysis
    default:
        return 0x0064 // Default
    }
}
```

#### Step 3: Fallback Strategy

```go
// ParsePerfCountersRobust uses architecture detection with fallback
func (t *Trace) ParsePerfCountersRobust() (*PerfCounterStats, error) {
    // Detect architecture
    arch := t.DetectGPUArchitecture()

    // Parse with architecture-specific logic
    stats, err := t.parseWithArchitecture(arch)
    if err == nil && stats.IsValid() {
        return stats, nil
    }

    // Fallback: Try each architecture until one works
    architectures := []GPUArchitecture{ArchG16, ArchG14, ArchG13}

    for _, fallbackArch := range architectures {
        if fallbackArch == arch {
            continue // Already tried
        }

        stats, err = t.parseWithArchitecture(fallbackArch)
        if err == nil && stats.IsValid() {
            // Success with fallback
            log.Printf("Architecture detection failed, succeeded with fallback: %v", fallbackArch)
            return stats, nil
        }
    }

    return nil, fmt.Errorf("failed to parse counters with any known architecture")
}
```

## Validation Requirements

### Testing Across Architectures

To validate counter parsing, we need reference traces from each architecture:

| Architecture | Chip | Trace Source | Status |
|--------------|------|--------------|--------|
| **AGX G13** | M1, M1 Pro, M1 Max | Community traces | ⏳ Needed |
| **AGX G14** | M2, M2 Pro, M2 Max | Community traces | ⏳ Needed |
| **AGX G15/G16** | M3, M3 Pro, M3 Max | Community traces | ⏳ Needed |
| **AGX G16** | M4, M4 Pro, M4 Max | `/tmp/fast-llm-mlx-test-perf.gputrace` | ✅ Available |

### Validation Methodology

For each architecture:

1. **Capture profiled trace**:
   ```bash
   # On target Mac (M1/M2/M3/M4)
   xctrace record --template 'Metal System Trace' \
       --output trace_$(sw_vers -productVersion)_$(sysctl -n machdep.cpu.brand_string).trace \
       --launch -- <benchmark_command>
   ```

2. **Export CSV from Xcode Instruments**:
   - Open .trace in Instruments
   - Export "Counters.csv"

3. **Parse with gputrace**:
   ```bash
   gputrace perfcounters trace.gputrace
   ```

4. **Compare results**:
   ```bash
   # Check if parsed values match CSV
   diff <(gputrace perfcounters trace.gputrace --format csv) Counters.csv
   ```

5. **Identify offset differences**:
   - If values don't match, field offsets likely differ
   - Use hexdump correlation to find correct offsets
   - Update architecture-specific offset maps

### Expected Variations

Based on architecture evolution:

**High Confidence (Likely Same):**
- Record marker (0x4E)
- Record size for samples (464 bytes)
- Metadata vs sample size ranges (2.3-2.9 KB vs 464 bytes)

**Medium Confidence (Possibly Same):**
- Encoder ID offset (0x01b4)
- Kernel Invocations offset (0x0064)
- Basic metric layout

**Low Confidence (Likely Different):**
- Ray tracing counter offsets (M3/M4 only)
- Mesh shading counter offsets (M3/M4 only)
- Extended metrics beyond core set

## Architecture Detection Methods

### Method 1: Device Info from Trace

Some traces may include device metadata:

```go
// ReadDeviceInfo extracts device information from trace metadata
func (t *Trace) ReadDeviceInfo() string {
    // Check for device info in trace bundle
    infoPath := filepath.Join(t.path, "device_info.plist")
    if data, err := os.ReadFile(infoPath); err == nil {
        // Parse plist for device model
        // Return "M1", "M2", "M3", "M4", etc.
    }

    // Fallback: check trace metadata
    // (implementation depends on .gputrace bundle structure)

    return ""
}
```

### Method 2: Counter Count Heuristic

```go
// CountTotalMetrics estimates total metric count from CSV column count
func (t *Trace) CountTotalMetrics() int {
    // If CSV is available
    if csvPath := t.findCountersCSV(); csvPath != "" {
        // Parse CSV header, count columns
        // Return column count - 5 (skip metadata columns)
    }

    // If only binary is available
    // Analyze record structure for metric count estimation
    // (complex, requires parsing logic)

    return 0
}
```

### Method 3: Ray Tracing Counter Detection

```go
// HasRayTracingCounters checks if trace includes ray tracing metrics
func (t *Trace) HasRayTracingCounters() bool {
    // Parse CSV and look for RT-specific columns
    csvPath := t.findCountersCSV()
    if csvPath == "" {
        return false
    }

    // Read CSV header
    f, _ := os.Open(csvPath)
    defer f.Close()
    reader := csv.NewReader(f)
    header, _ := reader.Read()

    // Look for ray tracing column names
    rtCounters := []string{
        "Ray Intersection Tests",
        "Ray Triangle Intersection",
        "RT Core Utilization",
    }

    for _, col := range header {
        for _, rtCounter := range rtCounters {
            if strings.Contains(col, rtCounter) {
                return true // M3/M4 trace
            }
        }
    }

    return false // M1/M2 trace
}
```

## Field Offset Documentation

### Current Knowledge (M4 Max - AGX G16)

From `FIELD_OFFSET_ANALYSIS.md`:

**Metadata Record (2,898 bytes):**
```
Offset    Size  Field                   Notes
0x01b4    8     Encoder ID              Candidate offset (needs validation)
0x0094    4     Unknown (113,664)
0x01d8    4     Unknown (7,208,960)
0x0224    4     Unknown (23,424)
```

**Sample Record (464 bytes):**
```
Offset    Size  Field                   Notes
0x0064    4     Kernel Invocations      Candidate (28,416 per sample)
0x0100    4     Alternative Invocations Candidate (12,040 per sample)
???       4     ALU Utilization (float) Found via field scanning
???       4     Occupancy (float)       Found via field scanning
```

### Planned Investigation (M1/M2/M3)

**Needed for each architecture:**
1. Capture profiled trace
2. Run hexdump analysis for known CSV values
3. Identify field offsets
4. Compare with M4 offsets
5. Document differences

**Field Priority:**
1. Encoder ID (metadata)
2. Kernel Invocations (sample)
3. ALU Utilization (sample)
4. Occupancy (sample)
5. Memory bandwidth metrics (sample)

## Alternative: Metal Replay Approach

As documented in `PERFCOUNTER_IMPLEMENTATION_RECOMMENDATION.md`, an alternative to binary parsing is Metal replay with `MTLCounterSampleBuffer`.

### Advantages of Replay

1. **Architecture-agnostic**: Metal API handles architecture differences
2. **No reverse engineering**: Public, documented API
3. **Future-proof**: Works with M5, M6, and beyond
4. **Complete metrics**: All 241+ counters available
5. **Validation**: Easy to compare with Instruments

### Disadvantages of Replay

1. **Requires replay implementation**: Must execute GPU workload
2. **Performance overhead**: Replay takes time
3. **No historical analysis**: Can't parse old .gpuprofiler_raw files

### When to Use Each Approach

**Use Binary Parsing when:**
- Analyzing existing profiled traces
- No need to re-execute workload
- Educational/reverse engineering interest

**Use Metal Replay when:**
- Capturing new profiling data
- Need all metrics, not just subset
- Want stable, maintained solution
- Building production profiling tools

## Recommendations

### Short-Term (gputrace-49)

1. **Document known variations**: Complete this document with research findings
2. **Add architecture detection**: Implement `DetectGPUArchitecture()`
3. **Create validation framework**: Test suite for multi-architecture traces
4. **Gather reference traces**: Community contribution of M1/M2/M3 traces

### Medium-Term (Future Work)

1. **Implement fallback logic**: Try multiple architectures if detection fails
2. **Offset mapping tables**: Comprehensive field offset documentation per architecture
3. **Binary format specification**: Complete reverse-engineering documentation
4. **Automated offset discovery**: Tools to identify offsets from CSV correlation

### Long-Term (Production)

Consider **Metal Replay approach** (gputrace-53, gputrace-54) as primary method:
- Binary parsing as fallback for historical traces
- Replay for new profiling workflows
- Both approaches maintained in parallel

## Implementation Checklist

For robust multi-architecture support:

- [ ] Implement `DetectGPUArchitecture()` with multiple detection methods
- [ ] Create architecture-specific offset tables
- [ ] Add `parseCounterRecordWithArch()` function
- [ ] Implement fallback architecture probing
- [ ] Gather reference traces from M1, M2, M3 systems
- [ ] Validate offset correctness per architecture
- [ ] Document discovered variations
- [ ] Add architecture detection to `gputrace perfcounters` output
- [ ] Create test suite with traces from each architecture
- [ ] Update error messages to suggest architecture issues

## Community Contribution

**Call for Traces:**

If you have access to M1, M2, or M3 Macs, please contribute profiled traces:

```bash
# Capture trace on your Mac
xctrace record --template 'Metal System Trace' \
    --output trace_$(sysctl -n machdep.cpu.brand_string | tr ' ' '_').trace \
    --launch -- <simple_metal_benchmark>

# Export CSV
# (Open in Instruments, export Counters.csv)

# Share both files (anonymize if needed)
# Upload to: [contribution link]
```

This will enable validation and field offset discovery for all architectures.

## References

- [PERFCOUNTER_BINARY_FORMAT.md](./PERFCOUNTER_BINARY_FORMAT.md) - Binary format analysis
- [FIELD_OFFSET_ANALYSIS.md](./FIELD_OFFSET_ANALYSIS.md) - M4 Max field offset findings
- [PERFCOUNTER_IMPLEMENTATION_RECOMMENDATION.md](./PERFCOUNTER_IMPLEMENTATION_RECOMMENDATION.md) - Binary vs Replay comparison
- [Apple GPU Architecture (dougallj/applegpu)](https://github.com/dougallj/applegpu) - AGX G13 documentation
- [Metal Benchmarks (philipturner)](https://github.com/philipturner/metal-benchmarks) - M1/M2 microarchitecture
- [Apple M4 Wikipedia](https://en.wikipedia.org/wiki/Apple_M4) - M3/M4 specifications

## Status

**Documentation**: ✅ Complete
**Implementation**: ⏳ Pending (requires M1/M2/M3 reference traces)
**Validation**: ⏳ Pending (only M4 validated)

---

**Next Steps:**
1. Gather reference traces from M1, M2, M3 systems
2. Implement architecture detection framework
3. Validate field offsets across all architectures
4. Update binary parsing code with architecture-specific logic

**Estimated Effort:**
- Architecture detection: 1-2 days
- Multi-architecture parsing: 2-3 days
- Validation (with reference traces): 1-2 days
- **Total**: 4-7 days (depends on trace availability)
