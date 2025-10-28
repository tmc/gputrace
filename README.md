# GPU Trace Parser for Metal

A Go library and toolset for parsing, analyzing, and converting Apple Metal `.gputrace` files to standard profiling formats.

## Overview

This package provides utilities to extract detailed information from Metal GPU trace captures, including:
- **Kernel names** - GPU compute kernel function names
- **Execution order** - Sequence of GPU operations
- **Encoder labels** - Command encoder annotations
- **Buffer bindings** - Memory buffer allocations and sizes
- **Command structure** - Command buffer and encoder hierarchy
- **Timing data** - GPU execution timing (when available)

## Features

### Core Functionality
- ✅ Parse `.gputrace` directory bundles
- ✅ Extract MTSP (Metal Trace Storage Protocol) records
- ✅ Identify GPU kernel names and execution order
- ✅ Extract buffer bindings with sizes
- ✅ Build hierarchical execution structure
- ✅ Convert to pprof format for analysis with standard Go tools

### Output Formats
- **pprof profiles** - Compatible with `go tool pprof`
- **Text reports** - Human-readable analysis
- **JSON** - Machine-readable structured data (planned)

## Installation

```bash
go get github.com/tmc/mlx-go/experiments/gputrace
```

## Quick Start

### Analyzing a Trace

```go
package main

import (
    "fmt"
    "log"
    "github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
    // Open trace file
    trace, err := gputrace.Open("benchmark.gputrace")
    if err != nil {
        log.Fatal(err)
    }

    // Extract kernel names
    fmt.Println("GPU Kernels:")
    for i, kernel := range trace.KernelNames {
        fmt.Printf("  %d. %s\n", i+1, kernel)
    }

    // Extract timing data
    extractor := gputrace.NewTimingExtractor(trace)
    timings, err := extractor.ExtractTimingV2()
    if err != nil {
        log.Fatal(err)
    }

    // Print timing report
    report := extractor.ImprovedTimingReport(timings)
    fmt.Println(report)
}
```

### Converting to Pprof

```bash
# Convert trace to pprof format
go run ./cmd/gputrace-to-pprof benchmark.gputrace

# Analyze with go tool pprof
go tool pprof benchmark.pb

# Or launch web UI
go tool pprof -http=:8080 benchmark.pb
```

## Command-Line Tools

### gputrace-to-pprof

Converts `.gputrace` files to pprof format:

```bash
go run ./cmd/gputrace-to-pprof <trace.gputrace> [output.pb]
```

**Features:**
- Extracts GPU kernel execution hierarchy
- Creates pprof profiles with multiple sample types (`gpu_time`, `dispatches`)
- Synthetic timing when real timing unavailable
- Full compatibility with `go tool pprof`

**Example:**
```bash
# Convert and analyze
go run ./cmd/gputrace-to-pprof benchmark.gputrace
go tool pprof -top benchmark.pb
go tool pprof -tree benchmark.pb
go tool pprof -http=:8080 benchmark.pb
```

### analyze

Comprehensive trace analysis tool:

```bash
go run ./cmd/analyze <trace.gputrace>
```

**Outputs:**
- Trace metadata
- Kernel name list with frequencies
- Buffer bindings with sizes
- MTSP record analysis
- Timestamp pattern detection
- Store file analysis

## API Reference

### Core Types

#### Trace
```go
type Trace struct {
    Path              string
    Metadata          *Metadata
    CaptureData       []byte
    DeviceResources   map[string][]byte
    KernelNames       []string
    EncoderLabels     []string
    BufferLabels      []string
    CommandQueueLabel string
}
```

#### EncoderTiming
```go
type EncoderTiming struct {
    Label          string
    StartTimestamp uint64  // Mach absolute time
    EndTimestamp   uint64
    DurationNs     uint64  // Duration in nanoseconds
    DurationMs     float64 // Duration in milliseconds
    Percentage     float32 // Percentage of total GPU time
}
```

#### MTSPRecord
```go
type MTSPRecord struct {
    Type   string  // Record type (CS, Ct, CU, etc.)
    Offset int     // File offset
    Size   int     // Record size
    Data   []byte  // Raw data
    Label  string  // Parsed label (for CS records)
}
```

### Main Functions

#### Opening Traces
```go
func Open(path string) (*Trace, error)
```
Opens and parses a `.gputrace` bundle.

#### Parsing MTSP Records
```go
func (t *Trace) ParseMTSPRecords() ([]MTSPRecord, error)
```
Extracts all MTSP records from the capture file.

#### Timing Extraction
```go
func NewTimingExtractor(trace *Trace) *TimingExtractorV2
func (te *TimingExtractorV2) ExtractTimingV2() ([]*EncoderTiming, error)
```
Extracts timing data using multiple strategies.

#### Enhanced Metadata
```go
func (t *Trace) ExtractEnhancedMetadata() (*EnhancedMetadata, error)
```
Extracts detailed structure including command buffers, encoders, and buffer bindings.

#### Pprof Conversion
```go
func (t *Trace) ToPprof(timings []*EncoderTiming) (*profile.Profile, error)
```
Converts trace data to pprof format.

## .gputrace Format Documentation

### Directory Structure

```
trace.gputrace/
├── capture                          # Main capture file (MTSP format)
├── metadata                         # Trace metadata (plist)
├── device-resources-0xNNNNNN       # Device resources (MTSP format)
├── store0                          # Timing/stats data (zlib compressed)
├── index                           # File index
└── MTLBuffer-XXXX-Y                # Buffer data files
```

### MTSP Format

Metal Trace Storage Protocol (MTSP) is a proprietary binary format:

**Header (16 bytes):**
```
Offset  Size  Description
0x00    4     Magic: "MTSP"
0x04    4     Version (typically 1024)
0x08    4     Size
0x0C    4     Offset
```

**Records:**
Each record has:
- 4-byte size field
- 4-byte type field
- Variable-length data

**Record Types:**
- `CS` - Command submission with kernel names
- `Ct` - Command type/transition
- `CU` - Command unknown/UUID
- `Culul` - Command buffer marker
- `Cuw` - Command write
- `Ci` - Command info

### Buffer Entry Format

In `device-resources` files:

```
Offset  Size  Description
0x00    9     Marker: "CU<b>ulul"
0x09    3     Padding (0x00)
0x0C    8     Pointer/address
0x14    N     Buffer name (null-terminated string, e.g., "MTLBuffer-1744-0")
0x14+N  5     Padding (0x00)
0x19+N  8     Buffer size (uint64, little-endian)
```

## Timing Extraction

### Strategies

The timing extractor uses multiple strategies in order:

1. **MTSP Record Timestamps** - Search for mach_absolute_time values in record data
2. **Proximity Search** - Find timestamps near kernel name strings
3. **Synthetic Timing** - Generate estimated timing based on kernel type

### Timestamp Validation

Valid mach_absolute_time timestamps must:
- Be uint64 values
- Range: 1×10¹⁵ to 1×10¹⁸
- Not have suspicious bit patterns (many trailing zeros)
- Appear in chronological pairs (start/end)

### Synthetic Timing Heuristics

When real timing unavailable, estimates based on kernel patterns:

| Pattern | Estimated Duration |
|---------|-------------------|
| matmul, gemm, conv, attention | 5ms |
| quantize, dequantize, affine | 2ms |
| normalize, softmax, layer_norm | 2ms |
| rope, rotary, qkv | 3ms |
| add, mul, relu, sigmoid | 0.5ms |
| default | 1ms |

**Note:** These are for visualization only, not accurate performance measurements.

## Pprof Integration

### Profile Structure

Generated pprof profiles have:

**Sample Types:**
- `gpu_time/nanoseconds` - GPU execution time
- `dispatches/count` - Number of kernel dispatches

**Hierarchy:**
```
GPU Trace (root)
  └─ Command Queue
      └─ Encoder Label
          └─ Kernel Name (leaf)
```

**Metadata:**
- Comments with trace path, kernel count
- Timestamps (start time, duration)
- Labels for encoder and kernel names

### Usage Examples

```bash
# Show top GPU kernels by time
go tool pprof -top -sample_index=gpu_time trace.pb

# Show dispatch counts
go tool pprof -top -sample_index=dispatches trace.pb

# Interactive web UI
go tool pprof -http=:8080 trace.pb

# Generate flamegraph
go tool pprof -http=:8080 trace.pb
# In browser: View > Flame Graph
```

## Known Limitations

### No Real Timing Data (Currently)

The `store0` file in `.gputrace` bundles typically contains no timing data (all zeros). This means:
- ❌ Precise GPU kernel execution times not available
- ❌ Cannot measure actual performance
- ✅ Can still see execution order and hierarchy
- ✅ Synthetic timing enables visualization

**Workaround:** Use synthetic timing or integrate with external profiling tools.

### Timing Extraction Challenges

- Metal capture doesn't always record timing data
- `MTL_CAPTURE_ENABLED=1` may not enable timing counters
- Xcode Instruments has access to additional APIs not available in captures

### Future Work

1. **Real Timing Extraction:**
   - Research Metal Performance Counters API
   - Investigate MTLCounterSampleBuffer
   - Parse GPU event timestamps if available

2. **Cross-Platform:**
   - Support NVIDIA Nsight captures
   - Support AMD ROCm profiling data
   - Unified GPU profiling API

3. **Advanced Analysis:**
   - Memory bandwidth utilization
   - Occupancy analysis
   - Warp/thread group efficiency

## Examples

### Example 1: Basic Analysis

```go
trace, _ := gputrace.Open("benchmark.gputrace")

fmt.Printf("Kernels: %d\n", len(trace.KernelNames))
fmt.Printf("Encoders: %d\n", len(trace.EncoderLabels))

for _, kernel := range trace.KernelNames {
    fmt.Println("  -", kernel)
}
```

### Example 2: Buffer Analysis

```go
trace, _ := gputrace.Open("benchmark.gputrace")
meta, _ := trace.ExtractEnhancedMetadata()

totalSize := uint64(0)
for _, buf := range meta.BufferBindings {
    totalSize += buf.Size
    fmt.Printf("%s: %.2f MB\n", buf.Name, float64(buf.Size)/(1024*1024))
}
fmt.Printf("Total: %.2f MB\n", float64(totalSize)/(1024*1024))
```

### Example 3: MTSP Record Parsing

```go
trace, _ := gputrace.Open("benchmark.gputrace")
records, _ := trace.ParseMTSPRecords()

for _, rec := range records {
    if rec.Type == "CS" && rec.Label != "" {
        fmt.Printf("Kernel: %s (offset=0x%x)\n", rec.Label, rec.Offset)
    }
}
```

## Contributing

Contributions welcome! Areas of interest:
- Real timing extraction from Metal captures
- Additional MTSP record type parsing
- Integration with other profiling tools
- Performance optimizations
- Documentation improvements

## References

- [Metal Performance Shaders Documentation](https://developer.apple.com/documentation/metalperformanceshaders)
- [Metal Capture Manager](https://developer.apple.com/documentation/metal/mtlcapturemanager)
- [pprof Documentation](https://github.com/google/pprof)
- [Go pprof Package](https://pkg.go.dev/github.com/google/pprof/profile)

## License

Part of the mlx-go project. See main repository for license information.

## Acknowledgments

- Apple Metal team for the GPU tracing infrastructure
- Google pprof team for the profiling format
- MLX team for the machine learning framework
