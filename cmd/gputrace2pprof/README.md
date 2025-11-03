# gputrace2pprof

A production-ready command-line tool for converting Apple Metal `.gputrace` files to pprof format with shader-level timing breakdowns.

## Overview

`gputrace2pprof` bridges the gap between Apple's Metal GPU profiling tools and the Go ecosystem's powerful pprof analysis tools. It extracts detailed GPU execution data from `.gputrace` bundles and converts them into standard pprof profiles that can be analyzed using `go tool pprof`.

**Key Features:**
- 🎯 Converts Metal GPU traces to standard pprof format
- 📊 Extracts shader-level timing breakdowns
- 🔍 Multiple output formats: hierarchical, flat, combined, and text
- ⚡ Synthetic timing when real timing data unavailable
- 📈 Compatible with standard Go profiling tools
- 🧪 Well-tested with comprehensive test coverage

## Installation

```bash
# Install from source
cd mlx-go/experiments/gputrace/cmd/gputrace2pprof
go install

# Or build locally
go build -o /usr/local/bin/gputrace2pprof
```

## Quick Start

### Basic Usage

```bash
# Convert a trace to pprof format
gputrace2pprof trace.gputrace

# Specify output file
gputrace2pprof trace.gputrace -o output.pprof

# Generate all output formats
gputrace2pprof trace.gputrace -all -prefix analysis

# View with pprof
go tool pprof -top output.pprof
go tool pprof -http=:8080 output.pprof
```

### Complete Workflow Example

```bash
# 1. Capture GPU trace from benchmark
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=1x

# 2. Convert to pprof (generates multiple formats)
gputrace2pprof /tmp/forward_pass_*.gputrace -all -prefix gpu_analysis

# 3. Analyze with pprof
go tool pprof -top gpu_analysis.gpu.pprof
go tool pprof -http=:8080 gpu_analysis.gpu.pprof

# 4. Or view the text report
cat gpu_analysis.txt
```

## Command-Line Options

### Basic Options

| Flag | Description | Example |
|------|-------------|---------|
| `-o <path>` | Output pprof file path | `-o output.pprof` |
| `-prefix <name>` | Output prefix for `-all` mode | `-prefix results` |
| `-v` | Verbose output | `-v` |

### Output Mode Options

| Flag | Description | Output Files |
|------|-------------|--------------|
| (none) | Single GPU pprof file | `trace.pprof` |
| `-all` | All profile formats | `.gpu.pprof`, `.gpu-flat.pprof`, `.combined.pprof`, `.txt` |
| `-text` | Text report only | `.txt` |
| `-stats` | Statistics only (no files) | stdout only |

## Output Formats

When using `-all`, the tool generates multiple complementary views of your GPU trace:

### 1. Hierarchical GPU Profile (`.gpu.pprof`)

Shows GPU time organized hierarchically:
```
GPU Trace
  └─ CommandQueue
      └─ Encoder
          └─ Kernel (shader)
```

**Best for:** Understanding the overall structure and finding hot spots.

```bash
go tool pprof -top gpu_analysis.gpu.pprof
go tool pprof -tree gpu_analysis.gpu.pprof
```

### 2. Flat GPU Profile (`.gpu-flat.pprof`)

Each encoder is a top-level sample with simpler structure.

**Best for:** Quick overview of encoder timings.

```bash
go tool pprof -top gpu_analysis.gpu-flat.pprof
```

### 3. Combined Profile (`.combined.pprof`)

Multi-view profile with multiple sample types:
- `gpu_time/nanoseconds` - GPU execution time
- `gpu_utilization/percentage` - GPU utilization percentage

**Best for:** Advanced analysis with multiple metrics.

```bash
go tool pprof -sample_index=gpu_time gpu_analysis.combined.pprof
go tool pprof -sample_index=gpu_utilization gpu_analysis.combined.pprof
```

### 4. Text Report (`.txt`)

Human-readable report with:
- Trace metadata
- Total GPU time
- Encoder breakdown with timings and percentages
- Kernel name list

**Best for:** Quick review without specialized tools.

```bash
cat gpu_analysis.txt
```

## Usage Examples

### Example 1: Basic Conversion

```bash
gputrace2pprof benchmark.gputrace
# Creates: benchmark.pprof

go tool pprof -top benchmark.pprof
```

### Example 2: Comprehensive Analysis

```bash
gputrace2pprof trace.gputrace -all -prefix analysis
# Creates:
#   analysis.gpu.pprof       - Hierarchical view
#   analysis.gpu-flat.pprof  - Flat view
#   analysis.combined.pprof  - Multi-metric view
#   analysis.txt             - Text report

# Analyze hierarchical profile
go tool pprof -http=:8080 analysis.gpu.pprof

# Quick text review
cat analysis.txt
```

### Example 3: Statistics Only

```bash
gputrace2pprof trace.gputrace -stats
# Prints statistics to stdout, no files created
```

### Example 4: Verbose Mode

```bash
gputrace2pprof trace.gputrace -v -all -prefix debug
# Shows detailed loading information and statistics
```

### Example 5: Custom Output Path

```bash
gputrace2pprof trace.gputrace -o /tmp/gpu_profile.pprof
go tool pprof -top /tmp/gpu_profile.pprof
```

## Analyzing Pprof Output

### Using go tool pprof

```bash
# Top functions by GPU time
go tool pprof -top analysis.gpu.pprof

# Call tree
go tool pprof -tree analysis.gpu.pprof

# Interactive web UI (recommended)
go tool pprof -http=:8080 analysis.gpu.pprof
```

### Web UI Features

When using `-http=:8080`, you get access to:
- **Flame Graph** - Visual representation of GPU time
- **Top** - Sorted list of GPU kernels by time
- **Graph** - Call graph with timing information
- **Source** - Source code view (if available)
- **Peek** - Interactive exploration

### Sample Output

```
$ go tool pprof -top analysis.gpu.pprof
File: analysis.gpu.pprof
Type: gpu_time
Time: Jan 2, 2025 at 3:04pm (UTC)
Duration: 127.45ms
Showing nodes accounting for 127.45ms, 100% of 127.45ms total
      flat  flat%   sum%        cum   cum%
   45.23ms 35.48% 35.48%    45.23ms 35.48%  matmul_kernel
   32.10ms 25.19% 60.67%    32.10ms 25.19%  softmax_kernel
   28.67ms 22.49% 83.16%    28.67ms 22.49%  layer_norm_kernel
   21.45ms 16.84% 100.00%    21.45ms 16.84%  attention_kernel
```

## Understanding the Output

### Profile Structure

The generated pprof profiles show GPU time organized hierarchically:

1. **GPU Trace** (root) - Total GPU time
2. **Command Queue** - GPU command queue
3. **Encoder** - Command encoder with label
4. **Kernel** (leaf) - Individual GPU shader/kernel

### Sample Types

Different profiles include different sample types:

- **gpu_time/nanoseconds** - GPU execution time in nanoseconds
- **dispatches/count** - Number of kernel dispatches
- **gpu_utilization/percentage** - GPU utilization as percentage

### Timing Data

The tool uses multiple strategies to extract timing:

1. **Real Timing** - Actual GPU timestamps when available
2. **Store0 Timing** - Performance counter data
3. **Synthetic Timing** - Estimated timing based on kernel patterns

When synthetic timing is used, the tool logs a warning. Synthetic timing provides qualitative analysis (relative ordering and structure) but not accurate performance measurements.

## Troubleshooting

### Common Issues

#### "Trace file not found"
```
Error: Trace file not found: trace.gputrace
```
**Solution:** Ensure the `.gputrace` path exists and is correct. The path should point to a directory bundle, not a file.

#### "Failed to load trace"
```
Error: Failed to load trace: invalid metadata
```
**Solution:** The trace file may be corrupted. Recapture the GPU trace with `MTL_CAPTURE_ENABLED=1`.

#### "Trace path must be a .gputrace directory bundle"
```
Error: Trace path must be a .gputrace directory bundle, got file: trace.txt
```
**Solution:** `.gputrace` files are actually directory bundles, not single files. Point to the `.gputrace` directory.

#### Empty or Zero Timing Data
```
Warning: Using synthetic timing (no real timing data available)
```
**Explanation:** The trace doesn't contain real timing data. The tool will generate synthetic timing for visualization purposes. The structure and kernel names are accurate, but the timing values are estimates.

**Solution:** To get real timing data, capture traces with performance counters enabled (requires Xcode or Instruments).

### Debug Mode

Use `-v` flag for verbose output:

```bash
gputrace2pprof -v trace.gputrace -all -prefix debug
```

This will show:
- Trace loading details
- Timing extraction strategy used
- Statistics about the trace
- File creation confirmations

### Verifying Output

Verify the generated pprof is valid:

```bash
go tool pprof -top output.pprof
```

If this command succeeds, the pprof file is valid and can be analyzed.

## Integration with Development Workflows

### Continuous Profiling

```bash
#!/bin/bash
# profile-test.sh - Profile a test and generate reports

TEST_NAME="$1"
OUTPUT_DIR="profiles/$(date +%Y%m%d_%H%M%S)"
mkdir -p "$OUTPUT_DIR"

# Run test with GPU capture
MTL_CAPTURE_ENABLED=1 go test -bench="$TEST_NAME" -benchtime=1x

# Find the latest trace
TRACE=$(ls -t /tmp/*.gputrace | head -1)

# Convert to pprof
gputrace2pprof "$TRACE" -all -prefix "$OUTPUT_DIR/profile"

# Open in browser
go tool pprof -http=:8080 "$OUTPUT_DIR/profile.gpu.pprof"
```

### CI/CD Integration

```yaml
# .github/workflows/gpu-profiling.yml
name: GPU Profiling

on: [push]

jobs:
  profile:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v2

      - name: Setup Go
        uses: actions/setup-go@v2

      - name: Run benchmarks with GPU capture
        run: |
          MTL_CAPTURE_ENABLED=1 go test -bench=. -benchtime=1x

      - name: Convert traces to pprof
        run: |
          go install ./experiments/gputrace/cmd/gputrace2pprof
          for trace in /tmp/*.gputrace; do
            gputrace2pprof "$trace" -all -prefix "artifacts/$(basename $trace .gputrace)"
          done

      - name: Upload profiles
        uses: actions/upload-artifact@v2
        with:
          name: gpu-profiles
          path: artifacts/
```

## Technical Details

### Input Format

`.gputrace` files are directory bundles containing:
- `capture` - Main MTSP (Metal Trace Storage Protocol) data
- `metadata` - Trace metadata (plist format)
- `device-resources-*` - Device resources
- `store0` - Timing/statistics data (zlib compressed)
- `MTLBuffer-*` - Buffer data files

### Processing Pipeline

1. **Parse** - Extract MTSP records from capture file
2. **Extract** - Extract kernel names, encoder labels, and timing data
3. **Build** - Construct hierarchical profile structure
4. **Convert** - Generate pprof protobuf format
5. **Write** - Output to file(s)

### Dependencies

- `github.com/tmc/mlx-go/experiments/gputrace` - GPU trace parsing
- `github.com/tmc/mlx-go/experiments/mlxprof` - Profile generation
- `github.com/google/pprof/profile` - pprof format

## Testing

The tool includes comprehensive tests:

```bash
# Run all tests
go test -v

# Run with coverage
go test -v -cover

# Run integration tests (requires test traces)
go test -v -run TestIntegration

# Benchmark conversion performance
go test -bench=. -benchmem
```

### Test Coverage

- ✅ Flag parsing and validation
- ✅ Input validation (file existence, format)
- ✅ Output path generation
- ✅ Error message quality
- ✅ Integration with real traces
- ✅ Pprof output validity
- ✅ All output modes (-all, -text, -stats)
- ✅ Performance benchmarks

## Performance

Typical performance on modern hardware:

| Trace Size | Conversion Time | Memory Usage |
|------------|----------------|--------------|
| Small (<10MB) | <100ms | ~20MB |
| Medium (10-100MB) | 100-500ms | ~50MB |
| Large (>100MB) | 500ms-2s | ~100MB |

## Limitations

1. **Real Timing Data** - Not all `.gputrace` files contain real timing data. The tool will use synthetic timing when real data is unavailable.

2. **Platform** - Only works with Apple Metal traces (macOS/iOS).

3. **Trace Format** - Assumes `.gputrace` directory bundle format (standard Metal capture format).

## Contributing

Contributions welcome! Areas of interest:

- Additional output formats
- Improved timing extraction algorithms
- Enhanced kernel name detection
- Performance optimizations
- Documentation improvements

## See Also

- [gputrace package documentation](../../README.md)
- [mlxprof package documentation](../../../mlxprof/README.md)
- [Metal GPU Capture Guide](https://developer.apple.com/documentation/metal/debugging_tools/capturing_gpu_command_data)
- [pprof Documentation](https://github.com/google/pprof)

## License

Part of the mlx-go project. See main repository for license information.

## Support

For issues, questions, or feature requests:
- Open an issue on GitHub
- See the [main gputrace README](../../README.md) for more details
- Check the [troubleshooting section](#troubleshooting) above
