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
- ✅ **Enhanced timing extraction** from multiple sources (kdebug, signposts, MTSP)
- ✅ **Accurate GPU timing** using kernel debug events
- ✅ **Shader-level profiling** with Metal AGX signposts
- ✅ **Multi-source correlation** with quality indicators

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

### gputrace2pprof

Converts `.gputrace` files to pprof format with shader-level timing breakdowns:

```bash
# Build the tool
go build -o /tmp/gputrace2pprof ./cmd/gputrace2pprof

# Convert single trace
/tmp/gputrace2pprof trace.gputrace -o output.pprof.gz

# Generate all formats (recommended)
/tmp/gputrace2pprof trace.gputrace -all -prefix analysis

# View with pprof
go tool pprof -top analysis.gpu.pprof.gz
go tool pprof -http=:8080 analysis.gpu.pprof.gz
```

**Features:**
- Extracts GPU kernel execution hierarchy
- Creates pprof profiles with shader timing breakdowns
- Multiple output formats: hierarchical, flat, combined, text
- Compatible with standard Go profiling tools
- Shows which shaders consume the most GPU time

See [SHADER_PPROF_GUIDE.md](./SHADER_PPROF_GUIDE.md) for complete usage guide.

### enhanced-timing

**NEW:** Extract accurate GPU timing using multiple data sources (kdebug, signposts, MTSP):

```bash
# Build the tool
go build -o /tmp/enhanced-timing ./cmd/enhanced-timing

# Analyze a trace with enhanced timing
/tmp/enhanced-timing trace.gputrace
```

**Features:**
- ✅ Accurate GPU timing from kernel debug events (most accurate)
- ✅ Queue latency measurement (submission to execution)
- ✅ Shader-level profiling from Metal AGX signposts
- ✅ Multi-source correlation with quality indicators
- ✅ Top N analysis by execution time

**Example output:**
```
Enhanced GPU Timing Report
==========================

Total Encoders: 15
Total Execution Time: 127.45 ms
Total Queue Latency: 3.21 ms
Average Execution: 8.50 ms
Average Queue: 0.21 ms

Timing Quality:
  combined: 12  (kdebug + signpost + MTSP)
  kdebug: 2     (kdebug only)
  mtsp: 1       (MTSP fallback)

Detailed Timing:
Encoder                  Kernel                     Exec (ms)  Queue (ms)   Util %    Quality
------------------------------------------------------------------------------------------------
MatMulEncoder           affine_qmm_float16...         45.23        0.15     35.5%   combined
SoftmaxEncoder          vv_Multiply_float16           32.10        0.28     25.2%   combined
```

See [ENHANCED_TIMING.md](./ENHANCED_TIMING.md) for complete documentation.

### MTSP Analysis Tools

A comprehensive suite of tools for analyzing Metal Trace Storage Protocol records:

```bash
# Parse all MTSP record types
go run ./cmd/analyze-record-sequence trace.gputrace

# Dump specific record types
go run ./cmd/dump-record-type Ct trace.gputrace

# Analyze command flags
go run ./cmd/analyze-command-flags trace.gputrace

# Count dispatches by flag type
go run ./cmd/count-actual-dispatches trace.gputrace

# Extract device resources (PSOs, functions, buffers)
go run ./cmd/test-device-resources trace.gputrace

# Performance counter analysis
go run ./cmd/analyze-counter-structure trace.gputrace/.gpuprofiler_raw/Counters_f_0.raw
```

See [QUICK_REFERENCE.md](./QUICK_REFERENCE.md) for complete tool reference.

### analyze

Basic trace analysis tool:

```bash
go run ./cmd/analyze/main.go trace.gputrace
```

**Features:**
- Lists all GPU kernel names
- Shows encoder labels and execution order
- Displays buffer bindings
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
- `CS` - Command submission with encoder labels and kernel names
- `Ct` - Compute command with pipeline state, thread groups, and buffer bindings
- `Ci` - Indirect command buffer execution (ICB dispatch)
- `Culul` - Indirect command buffer definition with array elements
- `Cul` - Resource binding with buffer addresses
- `Cuw` - Command update/write operations
- `CU` - Command UUID/identifier

**Ct Record Structure (Compute Command):**
```
Offset  Size  Description
0x00    4     Record size
0x04    4     Command flags (0xffffc01c=dispatch, 0xffffc02f=setup)
0x28    8     Pipeline state object address
0x30    8     Function address
0x38    4     Buffer binding count
0x3c    4     Stride (always 8)
0x40    N×8   Buffer binding addresses
```

**Ci Record Structure (Indirect Command Buffer):**
```
Offset  Size  Description
0x00    4     Record size (52 bytes)
0x04    4     Command flags (0xffffc00d)
0x28    8     ICB address
0x30    4     Dispatch count (expands to multiple GPU dispatches)
```

See [mtsp_records.go](mtsp_records.go) for complete parsing implementation.

### Performance Counter Files

When traces are captured with performance counters enabled, a `.gpuprofiler_raw` directory is created containing:

**Directory Structure:**
```
trace.gputrace.gpuprofiler_raw/
└── Counters_f_0.raw through Counters_f_119.raw (120 files, ~6GB total)
```

**Counter File Format:**
- Record marker: `0x4E 0x00 0x00 0x00`
- ~20,000+ records per file
- Variable record sizes: 69 bytes to 40KB
- Contains GPU execution statistics:
  - Aggregate dispatch counts (1043 total, 308 direct, 422 commands)
  - Per-shader performance metrics
  - Instruction counts per dispatch
  - GPU execution timing

**Key Finding:** Xcode's dispatch count (1043) comes from performance counter files, not MTSP records. MTSP tracks command submission (422 Ct records), while performance counters track GPU execution events (1043 actual dispatches after ICB expansion).

See `/tmp/FINAL-DISPATCH-COUNT-ANALYSIS.md` for complete reverse engineering details.

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

## Documentation

### Core Documentation (docs/)
- [docs/QUICK_REFERENCE.md](./docs/QUICK_REFERENCE.md) - Quick reference for all tools
- [docs/SHADER_PPROF_GUIDE.md](./docs/SHADER_PPROF_GUIDE.md) - Complete pprof conversion guide
- [docs/SHADER_SOURCE_MAPPING.md](./docs/SHADER_SOURCE_MAPPING.md) - Link GPU kernels to Metal source files

Documentation is also tracked in beads for better version control:
- `bd show bd-89` - Quick reference guide
- `bd show bd-90` - Shader pprof guide
- `bd show bd-91` - Shader source mapping guide

### Enhanced Timing System (Beads)

The enhanced timing system provides:
- Accurate GPU timing from kernel debug events
- Queue latency measurement
- Shader-level profiling from Metal AGX signposts
- Multi-source correlation with quality indicators

Documentation in beads:
- `bd show bd-84` - Enhanced timing system (multi-source correlation)
- `bd show bd-83` - kdebug code reference (15+ trace codes)
- `bd show bd-85` - Instruments infrastructure analysis
- `bd show bd-86` - Future enhancements roadmap
- `bd show bd-87` - Exploration summary
- `bd show bd-88` - Cleanup summary

## References

- [Metal Performance Shaders Documentation](https://developer.apple.com/documentation/metalperformanceshaders)
- [Metal Capture Manager](https://developer.apple.com/documentation/metal/mtlcapturemanager)
- [pprof Documentation](https://github.com/google/pprof)
- [Go pprof Package](https://pkg.go.dev/github.com/google/pprof/profile)
- [kdebug man page](https://www.manpagez.com/man/1/kdebug/) - Kernel debug tracing
- [xctrace man page](https://keith.github.io/xcode-man-pages/xctrace.1.html) - Xcode tracing tool

## License

Part of the mlx-go project. See main repository for license information.

## Acknowledgments

- Apple Metal team for the GPU tracing infrastructure
- Google pprof team for the profiling format
- MLX team for the machine learning framework
