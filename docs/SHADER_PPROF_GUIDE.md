# Shader Timing Analysis with Pprof

This guide shows how to generate and analyze pprof files with shader-level timing breakdowns from GPU traces.

## Overview

The `gputrace` and `mlxprof` packages can extract shader timing information from `.gputrace` files and convert them to Go's pprof format. This allows you to use standard Go profiling tools to analyze GPU shader performance.

## Quick Start

### 1. Capture GPU Trace

Run a benchmark with GPU capture enabled:

```bash
cd examples/mlx-lm-go/models
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=3x
```

This creates a `.gputrace` file in `/tmp/forward_pass_*.gputrace`

### 2. Convert to Pprof

Use the `gputrace2pprof` tool:

```bash
cd experiments/gputrace
go build -o /tmp/gputrace2pprof ./cmd/gputrace2pprof

# Generate all formats
/tmp/gputrace2pprof /tmp/forward_pass_*.gputrace -all -prefix gpu_analysis
```

This creates:
- `gpu_analysis.gpu.pprof.gz` - Hierarchical GPU profile
- `gpu_analysis.gpu-flat.pprof.gz` - Flat GPU profile
- `gpu_analysis.combined.pprof.gz` - Combined multi-view profile
- `gpu_analysis.txt` - Human-readable text report

### 3. Analyze with Pprof

View top shaders by GPU time:

```bash
go tool pprof -top gpu_analysis.gpu.pprof.gz
```

Interactive web interface:

```bash
go tool pprof -http=:8080 gpu_analysis.gpu.pprof.gz
```

## Detailed Usage

### gputrace2pprof Command

**Basic conversion**:
```bash
gputrace2pprof trace.gputrace
# Creates: trace.pprof.gz
```

**Custom output path**:
```bash
gputrace2pprof trace.gputrace -o custom_name.pprof.gz
```

**Generate all formats**:
```bash
gputrace2pprof trace.gputrace -all -prefix analysis
# Creates:
#   analysis.gpu.pprof.gz
#   analysis.gpu-flat.pprof.gz
#   analysis.combined.pprof.gz
#   analysis.txt
```

**Verbose output**:
```bash
gputrace2pprof trace.gputrace -v -all -prefix results
```

**Text report only**:
```bash
gputrace2pprof trace.gputrace -text -o report.txt
```

### Pprof Analysis Commands

**Top shaders by GPU time**:
```bash
go tool pprof -top gpu_analysis.gpu.pprof.gz
```

Example output:
```
Showing nodes accounting for 51.19ms, 100% of total
      flat  flat%   sum%        cum   cum%
  23.08ms 45.10% 45.10%   23.08ms 45.10%  affine_qmm_t_float16...
  14.67ms 28.66% 73.76%   14.67ms 28.66%  affine_qmv_fast_float16...
  10.55ms 20.62% 94.38%   10.55ms 20.62%  affine_dequantize_float16...
```

**List specific shader details**:
```bash
go tool pprof -list=affine_dequantize gpu_analysis.gpu.pprof.gz
```

**Tree view (hierarchical)**:
```bash
go tool pprof -tree gpu_analysis.gpu.pprof.gz
```

**Interactive web UI** (recommended):
```bash
go tool pprof -http=:8080 gpu_analysis.gpu.pprof.gz
```

The web UI provides:
- Flame graphs showing shader hierarchy
- Source-style view of GPU operations
- Graph visualization of shader relationships
- Top functions sorted by various metrics

**Compare two traces**:
```bash
go tool pprof -base=before.pprof.gz after.pprof.gz
```

This shows the delta between two runs (useful for measuring optimization impact).

## Profile Structure

The pprof profile organizes GPU timing hierarchically:

```
GPU Trace (root)
  └─ CommandQueue
      └─ Encoder (e.g., "Compute Encoder 1")
          └─ Kernel/Shader (e.g., "affine_qmv_fast_float16_t_gs_64_b_4_batch_0")
```

This structure makes it easy to:
- See total GPU time at the root
- Break down by command queue (usually just one)
- See timing per encoder
- **Identify which shaders consume the most time**

## Use Cases

### 1. Identify Performance Bottlenecks

Find the slowest shaders:

```bash
go tool pprof -top -nodecount=20 gpu_analysis.gpu.pprof.gz
```

Look for:
- High `flat%` values (time spent in that shader)
- Unexpected shaders (wrong kernel selection)
- Separate dequantization kernels (should be fused)

### 2. Compare Go vs Swift/Python

Capture traces from both implementations and compare shader usage:

**Go trace**:
```bash
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=1x
gputrace2pprof /tmp/forward_pass_*.gputrace -all -prefix go_shaders
```

**Swift trace** (capture using MLX-Swift):
```bash
# Capture Swift trace using Xcode Instruments or MLX-Swift benchmarks
gputrace2pprof /tmp/swift_trace.gputrace -all -prefix swift_shaders
```

**Compare**:
```bash
# View Go shaders
go tool pprof -top go_shaders.gpu.pprof.gz > go_top.txt

# View Swift shaders
go tool pprof -top swift_shaders.gpu.pprof.gz > swift_top.txt

# Compare
diff go_top.txt swift_top.txt
```

Look for:
- Different shaders being used (e.g., `affine_qmm` vs `affine_qmv`)
- Presence of separate dequantization in Go but not Swift
- Differences in SIMD group counts or execution times

### 3. Validate Optimizations

Before optimization:
```bash
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=5x
gputrace2pprof /tmp/forward_pass_*.gputrace -o before.pprof.gz
```

After optimization:
```bash
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=5x
gputrace2pprof /tmp/forward_pass_*.gputrace -o after.pprof.gz
```

Compare:
```bash
go tool pprof -base=before.pprof.gz after.pprof.gz
```

This shows the delta: positive values = slower, negative = faster.

### 4. Automated Performance Regression Detection

Add to CI:

```bash
#!/bin/bash
# perf_check.sh

# Run benchmark and capture GPU trace
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=3x

# Convert to pprof
gputrace2pprof /tmp/forward_pass_*.gputrace -text -o current.txt

# Check for known bad patterns
if grep -q "affine_dequantize" current.txt; then
    echo "❌ FAIL: Separate dequantization detected (should be fused)"
    exit 1
fi

if grep -q "affine_qmm_t.*batch_0" current.txt; then
    echo "❌ FAIL: Using matrix-matrix multiply for single tokens (should be qmv)"
    exit 1
fi

echo "✅ PASS: No known GPU performance issues detected"
```

## Programmatic Usage

You can also use the mlxprof package directly in Go:

```go
package main

import (
	"log"

	"github.com/tmc/mlx-go/experiments/mlxprof"
)

func main() {
	// Load GPU trace
	prof, err := mlxprof.FromGPUTrace("trace.gputrace")
	if err != nil {
		log.Fatal(err)
	}
	defer prof.Close()

	// Print summary
	prof.PrintSummary()

	// Generate all profile formats
	if err := prof.WriteAll("output"); err != nil {
		log.Fatal(err)
	}

	// Or individual formats
	prof.WriteGPUProfile("gpu.pprof.gz")
	prof.WriteCombinedProfile("combined.pprof.gz")
	prof.WriteTextReport("report.txt")
}
```

## Integration with Benchmarks

You can automatically generate pprof files from benchmarks:

```go
func BenchmarkForwardPass(b *testing.B) {
	bench := gpuprof.New(b, "forward_pass")
	defer bench.Close()

	// ... benchmark code ...

	// After benchmark completes, convert trace to pprof
	if os.Getenv("GENERATE_PPROF") == "1" {
		// Find the generated trace
		tracePattern := "/tmp/forward_pass_*.gputrace"
		traces, _ := filepath.Glob(tracePattern)

		if len(traces) > 0 {
			prof, err := mlxprof.FromGPUTrace(traces[0])
			if err == nil {
				prof.WriteAll("forward_pass_profile")
				prof.Close()
			}
		}
	}
}
```

Run with:
```bash
GENERATE_PPROF=1 MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$
```

## Known Issues from DBD3 Analysis

Based on the DBD3 session analysis, the following shader-level issues are visible in pprof:

### Issue 1: Separate Dequantization (20.62% overhead)

**Symptom in pprof**:
```
10.55ms 20.62%  affine_dequantize_float16_t_gs_64_b_4
```

**Problem**: Dequantization runs as a separate kernel instead of being fused with matmul.

**Expected**: This shader should NOT appear. Dequantization should be fused with `affine_qmv_fast`.

### Issue 2: Wrong Kernel Type (45.10% time)

**Symptom in pprof**:
```
23.08ms 45.10%  affine_qmm_t_float16_t_gs_64_b_4_alN_true_batch_0
```

**Problem**: Using matrix-matrix multiply (`qmm_t`) for single token generation.

**Expected**: Should use `affine_qmv_fast` (matrix-vector) for single tokens.

### Issue 3: Excessive SIMD Groups

**Symptom**: Compare SIMD group counts in text report:
- Go: 1,102,224 SIMD groups for `affine_qmv_fast`
- Swift: 132,482 SIMD groups for same operation
- **8.3x more work being dispatched**

## Demo Script

Run the complete workflow:

```bash
cd experiments/gputrace
./demo_shader_pprof.sh
```

This script:
1. Captures a GPU trace from the forward pass benchmark
2. Converts it to all pprof formats
3. Shows top shaders by GPU time
4. Provides commands for further analysis

## References

- Go pprof documentation: https://go.dev/blog/pprof
- Pprof tool: https://github.com/google/pprof
- DBD3 Analysis: See experiments/gpuprof/SHADER_ANALYSIS_GUIDE.md
- GPU Trace Format: See experiments/gputrace/GPU_TRACE_FORMAT.md

## Next Steps

1. **Fix dequantization fusion** - Eliminate the separate `affine_dequantize` kernel
2. **Fix kernel selection** - Use `qmv` instead of `qmm` for single tokens
3. **Reduce GPU commands** - Batch operations to reduce from 1,908 to ~269 commands
4. **Validate with pprof** - After each fix, compare before/after profiles

With these fixes, Go GPU performance should improve from 51.19ms down closer to Swift's 2.87ms.
