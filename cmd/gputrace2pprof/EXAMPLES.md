# gputrace2pprof Usage Examples

This document provides practical, real-world examples of using `gputrace2pprof` to analyze GPU performance.

## Table of Contents

- [Basic Examples](#basic-examples)
- [Workflow Examples](#workflow-examples)
- [Analysis Examples](#analysis-examples)
- [Automation Examples](#automation-examples)
- [Advanced Examples](#advanced-examples)

## Basic Examples

### Example 1: Simple Conversion

Convert a single trace to pprof format:

```bash
$ gputrace2pprof benchmark.gputrace
✅ GPU profile written to: benchmark.pprof

View with: go tool pprof -top benchmark.pprof
Or:        go tool pprof -http=:8080 benchmark.pprof
```

### Example 2: Custom Output Path

Specify where to save the output:

```bash
$ gputrace2pprof trace.gputrace -o /tmp/my_analysis.pprof
✅ GPU profile written to: /tmp/my_analysis.pprof
```

### Example 3: Generate All Formats

Get all available output formats:

```bash
$ gputrace2pprof trace.gputrace -all -prefix analysis
✅ Generated profiles:
   analysis.gpu.pprof       - Hierarchical GPU profile
   analysis.gpu-flat.pprof  - Flat GPU profile
   analysis.combined.pprof  - Combined multi-view profile
   analysis.txt             - Human-readable report

View with: go tool pprof -top analysis.gpu.pprof
Or:        go tool pprof -http=:8080 analysis.gpu.pprof
```

### Example 4: Quick Statistics

View trace statistics without generating files:

```bash
$ gputrace2pprof trace.gputrace -stats
GPU Trace Statistics
====================

Trace: trace.gputrace
Command Queue: CommandQueue
Encoders: 15
Kernels: 8

MTSP Records:
  CS (Command Submission): 15
  Ct (Compute Command): 42
  CU (Command UUID): 105

Buffer Bindings: 28
Total Buffer Memory: 45.2 MB
```

### Example 5: Verbose Mode

See detailed information during conversion:

```bash
$ gputrace2pprof trace.gputrace -v -o detailed.pprof
Loading GPU trace: trace.gputrace
GPU Trace Profile Summary
=========================

Trace: trace.gputrace
Command Queue: CommandQueue
Encoders: 15
Kernels: 8

Total GPU Time: 127.45 ms

Top Encoders:
   1. matmul_encoder                 45.23 ms ( 35.5%)
   2. softmax_encoder                32.10 ms ( 25.2%)
   3. layer_norm_encoder             28.67 ms ( 22.5%)
   4. attention_encoder              21.45 ms ( 16.8%)

Writing pprof to: detailed.pprof
✅ GPU profile written to: detailed.pprof
```

## Workflow Examples

### Example 6: Benchmark Profiling Workflow

Complete workflow from benchmark to analysis:

```bash
# Step 1: Run benchmark with GPU capture enabled
$ MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkMatMul -benchtime=1x
goos: darwin
goarch: arm64
BenchmarkMatMul-10    1    1523984250 ns/op
PASS
ok      example.com/mlx  1.534s

# Step 2: Find the generated trace
$ ls -lt /tmp/*.gputrace | head -1
drwxr-xr-x  8 user  wheel  256 Jan  2 15:04 /tmp/matmul_trace.gputrace

# Step 3: Convert to pprof with all formats
$ gputrace2pprof /tmp/matmul_trace.gputrace -all -prefix matmul_analysis
✅ Generated profiles:
   matmul_analysis.gpu.pprof
   matmul_analysis.gpu-flat.pprof
   matmul_analysis.combined.pprof
   matmul_analysis.txt

# Step 4: Quick review with text report
$ cat matmul_analysis.txt
GPU Trace Profile Report
========================

Trace: /tmp/matmul_trace.gputrace
Command Queue: CommandQueue
Encoders: 3
Kernel Names: 1

Total GPU Time: 1520.34 ms

Encoder Breakdown:
Label                          Duration (ms) Duration (ns) Percent
--------------------------------------------------------------------------------
MatMulEncoder                       1520.34   1520340000    100.0%

Kernel Names:
  1. matmul_kernel

# Step 5: Open interactive analysis
$ go tool pprof -http=:8080 matmul_analysis.gpu.pprof
Serving web UI on http://localhost:8080
```

### Example 7: Comparing Multiple Runs

Compare performance across different implementations:

```bash
# Capture baseline
$ MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkBaseline -benchtime=1x
$ gputrace2pprof /tmp/baseline.gputrace -all -prefix baseline

# Capture optimized version
$ MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkOptimized -benchtime=1x
$ gputrace2pprof /tmp/optimized.gputrace -all -prefix optimized

# Compare text reports
$ diff -u baseline.txt optimized.txt
--- baseline.txt
+++ optimized.txt
@@ -7,7 +7,7 @@
-Total GPU Time: 1520.34 ms
+Total GPU Time: 892.15 ms

# Compare in pprof
$ go tool pprof -http=:8080 -base=baseline.gpu.pprof optimized.gpu.pprof
```

### Example 8: Batch Processing Multiple Traces

Process all traces in a directory:

```bash
#!/bin/bash
# process_all_traces.sh

TRACE_DIR="/tmp/gpu_traces"
OUTPUT_DIR="./analysis_results"
mkdir -p "$OUTPUT_DIR"

for trace in "$TRACE_DIR"/*.gputrace; do
    name=$(basename "$trace" .gputrace)
    echo "Processing $name..."

    gputrace2pprof "$trace" -all -prefix "$OUTPUT_DIR/$name"

    echo "✅ Completed $name"
    echo ""
done

echo "All traces processed. Results in $OUTPUT_DIR/"
```

## Analysis Examples

### Example 9: Finding GPU Hotspots

Identify which kernels consume the most GPU time:

```bash
# Generate profile
$ gputrace2pprof trace.gputrace -o hotspots.pprof

# Show top kernels by time
$ go tool pprof -top hotspots.pprof
File: hotspots.pprof
Type: gpu_time
Showing nodes accounting for 127.45ms, 100% of 127.45ms total
      flat  flat%   sum%        cum   cum%
   45.23ms 35.48% 35.48%    45.23ms 35.48%  matmul_kernel
   32.10ms 25.19% 60.67%    32.10ms 25.19%  softmax_kernel
   28.67ms 22.49% 83.16%    28.67ms 22.49%  layer_norm_kernel
   21.45ms 16.84% 100.00%    21.45ms 16.84%  attention_kernel
```

**Analysis:** The `matmul_kernel` is the hotspot, consuming 35% of GPU time. Optimize this kernel first for maximum impact.

### Example 10: Understanding Execution Flow

Visualize the hierarchical structure of GPU work:

```bash
$ gputrace2pprof trace.gputrace -o flow.pprof

$ go tool pprof -tree flow.pprof
Showing nodes accounting for 127.45ms, 100% of 127.45ms total
      flat  flat%   sum%        cum   cum%   calls calls% + context
                                           127.45ms   100% |   GPU Trace
   0.00ms  0.00%  0.00%   127.45ms   100%                | CommandQueue
                                           127.45ms   100% |   CommandQueue
   0.00ms  0.00%  0.00%   127.45ms   100%                | MatMulEncoder
                                            45.23ms 35.48% |   MatMulEncoder
  45.23ms 35.48% 35.48%    45.23ms 35.48%                | matmul_kernel
```

### Example 11: Flame Graph Visualization

Create a flame graph for visual analysis:

```bash
$ gputrace2pprof trace.gputrace -o flamegraph.pprof

# Open web UI and select "Flame Graph" view
$ go tool pprof -http=:8080 flamegraph.pprof
# In browser: View > Flame Graph
```

The flame graph shows:
- Width = time spent in kernel
- Color = different kernels
- Hierarchy = encoder → kernel relationship

### Example 12: Multi-Metric Analysis

Analyze multiple metrics from combined profile:

```bash
$ gputrace2pprof trace.gputrace -all -prefix multi

# View GPU time
$ go tool pprof -top -sample_index=gpu_time multi.combined.pprof
      flat  flat%   sum%        cum   cum%
   45.23ms 35.48% 35.48%    45.23ms 35.48%  matmul_kernel

# View GPU utilization
$ go tool pprof -top -sample_index=gpu_utilization multi.combined.pprof
      flat  flat%   sum%        cum   cum%
   35.48  35.48% 35.48%     35.48  35.48%  matmul_kernel
```

## Automation Examples

### Example 13: Automated Profiling Script

Script that runs tests, captures traces, and generates reports:

```bash
#!/bin/bash
# auto_profile.sh - Automated GPU profiling

set -e

BENCHMARK="${1:-Benchmark}"
OUTPUT_DIR="gpu_profiles/$(date +%Y%m%d_%H%M%S)"
mkdir -p "$OUTPUT_DIR"

echo "🔍 Running benchmark: $BENCHMARK"
MTL_CAPTURE_ENABLED=1 go test -bench="$BENCHMARK" -benchtime=1x -benchmem | tee "$OUTPUT_DIR/benchmark.txt"

echo ""
echo "📊 Converting GPU traces..."

for trace in /tmp/*.gputrace; do
    [ -e "$trace" ] || continue

    name=$(basename "$trace" .gputrace)
    echo "  Processing $name..."

    gputrace2pprof "$trace" -all -prefix "$OUTPUT_DIR/$name" -v

    # Move trace to output directory
    mv "$trace" "$OUTPUT_DIR/"
done

echo ""
echo "✅ Profiling complete!"
echo "📁 Results: $OUTPUT_DIR"
echo ""
echo "To view:"
echo "  go tool pprof -http=:8080 $OUTPUT_DIR/*.gpu.pprof"
```

### Example 14: CI/CD Integration

GitHub Actions workflow for automated profiling:

```yaml
# .github/workflows/gpu-profile.yml
name: GPU Profiling

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  profile:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v3
        with:
          go-version: '1.21'

      - name: Install gputrace2pprof
        run: |
          cd experiments/gputrace/cmd/gputrace2pprof
          go install

      - name: Run benchmarks with GPU capture
        run: |
          MTL_CAPTURE_ENABLED=1 go test -bench=. -benchtime=1x

      - name: Convert traces to pprof
        run: |
          mkdir -p artifacts
          for trace in /tmp/*.gputrace; do
            [ -e "$trace" ] || continue
            name=$(basename "$trace" .gputrace)
            gputrace2pprof "$trace" -all -prefix "artifacts/$name"
          done

      - name: Generate summary
        run: |
          echo "# GPU Profiling Results" > artifacts/SUMMARY.md
          echo "" >> artifacts/SUMMARY.md
          for txt in artifacts/*.txt; do
            [ -e "$txt" ] || continue
            echo "## $(basename $txt .txt)" >> artifacts/SUMMARY.md
            echo '```' >> artifacts/SUMMARY.md
            cat "$txt" >> artifacts/SUMMARY.md
            echo '```' >> artifacts/SUMMARY.md
            echo "" >> artifacts/SUMMARY.md
          done

      - name: Upload profiles
        uses: actions/upload-artifact@v3
        with:
          name: gpu-profiles
          path: artifacts/
          retention-days: 30

      - name: Comment PR
        if: github.event_name == 'pull_request'
        uses: actions/github-script@v6
        with:
          script: |
            const fs = require('fs');
            const summary = fs.readFileSync('artifacts/SUMMARY.md', 'utf8');
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: summary
            });
```

### Example 15: Performance Regression Detection

Script to detect performance regressions:

```bash
#!/bin/bash
# check_regression.sh - Detect GPU performance regressions

BASELINE="$1"
CURRENT="$2"
THRESHOLD_PERCENT=10

if [ -z "$BASELINE" ] || [ -z "$CURRENT" ]; then
    echo "Usage: $0 <baseline.pprof> <current.pprof>"
    exit 1
fi

echo "Comparing GPU performance..."
echo "Baseline: $BASELINE"
echo "Current:  $CURRENT"
echo ""

# Extract total GPU time from each profile
baseline_time=$(go tool pprof -top "$BASELINE" | grep -m1 "GPU Trace" | awk '{print $1}')
current_time=$(go tool pprof -top "$CURRENT" | grep -m1 "GPU Trace" | awk '{print $1}')

# Convert to milliseconds (assuming input is in ms)
baseline_ms=${baseline_time%ms}
current_ms=${current_time%ms}

# Calculate difference
diff=$(echo "$current_ms - $baseline_ms" | bc)
percent=$(echo "scale=2; ($diff / $baseline_ms) * 100" | bc)

echo "Baseline time: ${baseline_ms}ms"
echo "Current time:  ${current_ms}ms"
echo "Difference:    ${diff}ms (${percent}%)"
echo ""

# Check for regression
if (( $(echo "$percent > $THRESHOLD_PERCENT" | bc -l) )); then
    echo "❌ REGRESSION DETECTED: ${percent}% slower than baseline"
    echo "   Threshold: ${THRESHOLD_PERCENT}%"
    exit 1
else
    echo "✅ No significant regression detected"
    exit 0
fi
```

## Advanced Examples

### Example 16: Custom Analysis with pprof API

Go program that programmatically analyzes pprof output:

```go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/google/pprof/profile"
)

func main() {
    if len(os.Args) != 2 {
        log.Fatal("Usage: analyze <profile.pprof>")
    }

    // Open profile
    f, err := os.Open(os.Args[1])
    if err != nil {
        log.Fatal(err)
    }
    defer f.Close()

    // Parse profile
    p, err := profile.Parse(f)
    if err != nil {
        log.Fatal(err)
    }

    // Analyze samples
    fmt.Printf("Profile: %s\n", os.Args[1])
    fmt.Printf("Samples: %d\n", len(p.Sample))
    fmt.Printf("Duration: %v\n", p.DurationNanos)
    fmt.Printf("\n")

    // Find top kernels
    kernelTime := make(map[string]int64)
    for _, sample := range p.Sample {
        if len(sample.Location) > 0 {
            loc := sample.Location[0]
            if len(loc.Line) > 0 {
                fn := loc.Line[0].Function
                kernelTime[fn.Name] += sample.Value[0]
            }
        }
    }

    fmt.Println("Top Kernels by GPU Time:")
    for kernel, time := range kernelTime {
        ms := float64(time) / 1e6
        fmt.Printf("  %-30s %10.2f ms\n", kernel, ms)
    }
}
```

### Example 17: Filtering and Focusing

Focus analysis on specific kernels:

```bash
# Generate full profile
$ gputrace2pprof trace.gputrace -o full.pprof

# Focus on matmul kernels only
$ go tool pprof -focus=matmul full.pprof -top

# Ignore small kernels
$ go tool pprof -hide=".*relu.*|.*bias.*" full.pprof -top

# Show only attention-related kernels
$ go tool pprof -focus=".*attention.*" full.pprof -http=:8080
```

### Example 18: Combining with CPU Profiling

Correlate GPU and CPU profiles:

```bash
# Run benchmark with both CPU and GPU profiling
$ go test -bench=BenchmarkModel \
    -cpuprofile=cpu.pprof \
    -benchtime=1x
$ export MTL_CAPTURE_ENABLED=1
$ go test -bench=BenchmarkModel -benchtime=1x

# Convert GPU trace
$ gputrace2pprof /tmp/model.gputrace -o gpu.pprof

# View both profiles
$ go tool pprof -http=:8080 cpu.pprof
$ go tool pprof -http=:8081 gpu.pprof

# Compare to identify CPU vs GPU bottlenecks
```

### Example 19: Export to Other Formats

Convert pprof to other visualization formats:

```bash
# Generate pprof
$ gputrace2pprof trace.gputrace -o analysis.pprof

# Generate SVG graph
$ go tool pprof -svg analysis.pprof > gpu_graph.svg

# Generate PDF report
$ go tool pprof -pdf analysis.pprof > gpu_report.pdf

# Generate text report with call graph
$ go tool pprof -tree analysis.pprof > gpu_tree.txt

# Generate dot file for custom visualization
$ go tool pprof -dot analysis.pprof > gpu_graph.dot
$ dot -Tpng gpu_graph.dot -o gpu_graph.png
```

### Example 20: Long-Running Profiling

Profile a long-running process:

```bash
#!/bin/bash
# continuous_profile.sh - Profile multiple iterations

ITERATIONS=10
OUTPUT_DIR="continuous_profiles"
mkdir -p "$OUTPUT_DIR"

for i in $(seq 1 $ITERATIONS); do
    echo "Iteration $i/$ITERATIONS"

    # Run one iteration with GPU capture
    MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkInference -benchtime=1x

    # Convert latest trace
    trace=$(ls -t /tmp/*.gputrace | head -1)
    gputrace2pprof "$trace" -all -prefix "$OUTPUT_DIR/iter_$i"

    # Clean up trace
    rm -rf "$trace"

    echo "Completed iteration $i"
    echo ""
done

echo "Generating comparison report..."
for i in $(seq 1 $ITERATIONS); do
    echo "Iteration $i:" >> "$OUTPUT_DIR/comparison.txt"
    grep "Total GPU Time" "$OUTPUT_DIR/iter_$i.txt" >> "$OUTPUT_DIR/comparison.txt"
done

echo "✅ Continuous profiling complete"
echo "Results in: $OUTPUT_DIR"
```

## Tips and Best Practices

1. **Always use `-all`** for comprehensive analysis - the different views complement each other
2. **Start with text reports** for quick insights before diving into interactive analysis
3. **Use `-stats`** to verify traces contain expected data before full conversion
4. **Enable `-v`** when debugging profiling issues
5. **Automate profiling** in your development workflow for continuous performance monitoring
6. **Compare profiles** across commits to catch regressions early
7. **Focus analysis** using pprof filters to zoom in on specific kernels
8. **Export visualizations** for documentation and presentations

## Getting Help

- Run `gputrace2pprof -h` for help
- See [README.md](README.md) for full documentation
- Check [Troubleshooting](README.md#troubleshooting) section
- Open an issue on GitHub for bugs or feature requests
