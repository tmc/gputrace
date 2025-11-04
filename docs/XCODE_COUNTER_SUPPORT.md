# Xcode Performance Counter Support

**Date:** 2025-11-03
**Status:** Complete

## Overview

gputrace now supports importing and analyzing performance counter data from Xcode Instruments GPU captures. This provides access to 240+ hardware performance metrics without requiring direct Metal API integration.

## Background

When capturing GPU traces with Xcode Instruments with performance counters enabled, Xcode generates:

1. **.gputrace** directory (Xcode format with `index`, `metadata`, `store0` files)
2. **Counters.csv** file with aggregated performance metrics
3. **.gpuprofiler_raw** directory with raw counter samples

The Xcode format is different from the raw capture format, using:
- `index` - Binary index file ("xdic" magic)
- `metadata` - Apple binary property list (bplist00)
- `store0` - Zlib-compressed trace data (1.6GB decompressed)
- `.gpuprofiler_raw/Counters_f_*.raw` - Raw binary counter files (~40 files, 6.4MB each)

## Implementation

### CSV Import (csv_import.go)

Rather than parsing the complex binary counter files, we import the human-readable Counters.csv file that Xcode exports. This provides:

- **6 encoders** (one per command buffer in the test trace)
- **241 metrics** per encoder (240+ unique performance counters)
- **225 counters** with actual values per encoder

#### Key Functions

```go
// Parse Xcode Counters.csv file
func (t *Trace) ParseXcodeCountersCSV(csvPath string) (*XcodeCounterData, error)

// Check if Counters.csv is available
func (t *Trace) HasXcodeCountersCSV() bool

// Get specific counter value for an encoder
func (xcd *XcodeCounterData) GetCounterValue(encoderIndex int, counterName string) (float64, bool)

// Get encoder by function index (for correlation with trace data)
func (xcd *XcodeCounterData) GetEncoderByFunctionIndex(functionIndex int) (*XcodeEncoderCounters, bool)
```

### Data Structures

```go
type XcodeCounterData struct {
    Encoders []XcodeEncoderCounters  // Per-encoder counter data
    Metrics  []string                // List of available metric names
}

type XcodeEncoderCounters struct {
    Index              int                // Row index from CSV
    FunctionIndex      int                // Encoder function index
    CommandBufferLabel string             // Command buffer label
    EncoderLabel       string             // Encoder label
    Counters           map[string]float64 // Map of counter name to value
}
```

### CLI Command (xcode-counters)

New command `gputrace xcode-counters` provides multiple output formats:

#### Summary Format (default)
```bash
./gputrace xcode-counters trace.gputrace
```

Shows key metrics in table format:
- ALU Utilization
- Kernel Occupancy
- Kernel Invocations
- GPU Read/Write Bandwidth
- Instruction Throughput Utilization

#### Detailed Format
```bash
./gputrace xcode-counters trace.gputrace --format detailed
```

Shows all 225 counter values for each encoder.

#### Metrics List
```bash
./gputrace xcode-counters trace.gputrace --format metrics
```

Lists all 241 available metric names.

#### Filtering
```bash
# Show top 3 encoders by ALU utilization
./gputrace xcode-counters trace.gputrace --metric "ALU Utilization" --top 3
```

## Available Metrics

The Counters.csv includes 240+ hardware metrics across categories:

### Compute Metrics
- ALU Utilization
- Kernel Occupancy
- Kernel Invocations
- Compute Shader Launch Limiter/Utilization
- Instruction Throughput Limiter/Utilization

### Memory Metrics
- Buffer Device Memory Bytes Read/Written
- Buffer L1 Miss Rate
- Buffer L1 Read/Write Bandwidth
- GPU Read/Write Bandwidth
- Device Memory Bandwidth
- Last Level Cache Bandwidth/Miss Rate

### ALU Metrics
- F16/F32 Limiter/Utilization
- Integer and Complex Limiter/Utilization
- Kernel ALU Float/Half/Integer Instructions
- Kernel ALU Performance

### Control Flow
- Control Flow Limiter/Utilization

### Cache Metrics
- L1 Cache Limiter/Utilization
- L1 Buffer/Register/Threadgroup Residency
- Texture Cache Miss Rate
- Texture L1 Bytes Read

### Occupancy Metrics
- Kernel Occupancy
- Occupancy Manager Target
- Ray Occupancy (for ray tracing)

## File Locations

The CSV parser automatically searches for Counters.csv in several locations:

1. `<trace-dir>/<basename> Counters.csv`
2. `<trace-dir>/<basename>-perf Counters.csv`
3. `<trace-dir>/<basename>.gputrace Counters.csv`
4. `<trace.gputrace>/Counters.csv` (inside bundle)

Example with trace `/tmp/llm-tool_1762220084.gputrace`:
- Looks for `/tmp/llm-tool_1762220084 Counters.csv` ✓
- Looks for `/tmp/llm-tool_1762220084-perf Counters.csv`
- Etc.

## Test Results

### Test Trace: llm-tool_1762220084.gputrace

```
=== Xcode Performance Counters ===

Total Encoders: 6
Total Metrics:  241

Encoder  Command Buffer                ALU Utilization  Kernel Occupancy  Kernel Invocations
-------  --------------                --------         --------          --------
1        Command Buffer 0              20.82%           23.39%            780800
2        Command Buffer 1              21.01%           20.73%            689920
3        Command Buffer 2              21.87%           23.22%            769536
4        Command Buffer 3              22.00%           23.03%            1029379
5        Command Buffer 4              30.67%           35.87%            1158144
6        Command Buffer 5              1.24%            3.55%             449921
```

### Performance
- CSV parsing: <100ms for 6 encoders
- Memory usage: ~2MB for parsed data
- All tests passing

## Usage Examples

### Basic Analysis
```bash
# View summary of all encoders
gputrace xcode-counters trace.gputrace

# Find bottleneck by ALU utilization
gputrace xcode-counters trace.gputrace --metric "ALU Utilization" --top 1

# Check memory bandwidth usage
gputrace xcode-counters trace.gputrace --metric "GPU Read Bandwidth" --top 5
```

### Detailed Investigation
```bash
# See all metrics for top encoder
gputrace xcode-counters trace.gputrace --format detailed --top 1

# List all available metrics
gputrace xcode-counters trace.gputrace --format metrics

# Export to JSON for analysis
gputrace xcode-counters trace.gputrace --format json > counters.json
```

## Integration Points

### Future Timeline Integration

The XcodeCounterData can be integrated into the timeline viewer to show:

1. **Counter overlays** - Display metrics like ALU utilization as graphs
2. **Encoder correlation** - Match counter data with timeline events by function index
3. **Interactive tooltips** - Show detailed metrics on hover
4. **Performance bottleneck highlighting** - Visual indicators for high utilization

### Example Integration
```go
// In timeline generation:
csvData, err := trace.ParseXcodeCountersCSV("")
if err == nil {
    // Add counter tracks to timeline
    for _, encoder := range csvData.Encoders {
        if aluUtil, ok := encoder.Counters["ALU Utilization"]; ok {
            timeline.AddCounterTrack("ALU Utilization", encoder.Index, aluUtil)
        }
    }
}
```

## Comparison: CSV vs Raw Binary Parsing

| Approach | Pros | Cons |
|----------|------|------|
| **CSV Import** (implemented) | ✓ Simple, reliable<br>✓ All metrics available<br>✓ Human-readable format<br>✓ No binary parsing needed | • Requires Xcode export<br>• Slightly larger file size |
| **Raw Binary Parsing** | ✓ Works without CSV export<br>✓ Can extract finer-grained samples | • Complex binary format<br>• Requires reverse engineering<br>• Format may change<br>• 40 files × 6MB = 240MB to parse |

**Decision:** CSV import provides the best balance of simplicity, reliability, and completeness.

## Files Created

- `csv_import.go` (146 lines) - CSV parser implementation
- `csv_import_test.go` (84 lines) - Test coverage
- `cmd/gputrace/cmd/xcode_counters.go` (280 lines) - CLI command
- `docs/XCODE_COUNTER_SUPPORT.md` (this file)

## Limitations

1. **CSV Required**: Must have Counters.csv file exported from Xcode
2. **Aggregated Data**: CSV contains aggregated/averaged values, not raw samples
3. **No Timeline Correlation Yet**: Counter data not yet integrated into timeline viewer (planned)

## Next Steps

1. **Timeline Integration** - Add counter tracks to HTML timeline viewer
2. **Correlation Engine** - Match encoders with shader names using function indices
3. **Bottleneck Detection** - Automatic identification of performance bottlenecks
4. **Recommendations** - Suggest optimizations based on counter patterns

## References

- [Xcode Instruments Documentation](https://developer.apple.com/documentation/xcode/instruments)
- [Metal Performance Counters](https://developer.apple.com/documentation/metal/performance/counters)
- [GPU Profiling Best Practices](https://developer.apple.com/documentation/metal/performance/optimizing)
