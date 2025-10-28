# GPU Trace Integration with Go Flight Recorder and Profiling

## Overview

This document explains how .gputrace data should relate to Go's profiling ecosystem: flight recorder, pprof, stdlib tracing, and go tool trace.

## The Go Profiling Ecosystem

### 1. Flight Recorder (runtime/trace)

**What it is**: A low-overhead, always-on tracing system built into the Go runtime.

**Key characteristics**:
- Continuously records events into a circular buffer
- Records EVERYTHING (not sampling): goroutine scheduling, syscalls, GC, network I/O
- Keeps only recent events (like an airplane black box)
- ~5-10% overhead when active
- Can be enabled at startup or triggered on-demand

**Use case**: Post-mortem debugging, understanding concurrency issues, finding blocking operations

```go
import "runtime/trace"

// Enable flight recorder
f, _ := os.Create("trace.out")
trace.Start(f)
defer trace.Stop()
```

### 2. pprof (runtime/pprof, net/http/pprof)

**What it is**: Statistical profiler that samples program state.

**Key characteristics**:
- Samples at regular intervals (default: 100Hz)
- Creates aggregated views (flame graphs, call trees)
- Shows WHERE time is spent, not WHEN
- Very low overhead (~1%)
- Multiple profile types: CPU, heap, goroutines, mutex, block

**Use case**: Finding hot spots, optimizing algorithms, memory leak detection

```go
import "runtime/pprof"

// CPU profiling
pprof.StartCPUProfile(f)
defer pprof.StopCPUProfile()

// Heap profiling (snapshot)
pprof.WriteHeapProfile(f)
```

### 3. Standard Library Tracing (runtime/trace)

**What it is**: Detailed execution event stream.

**Key characteristics**:
- Records goroutine lifecycle, scheduling decisions, blocking
- Binary format (trace.out)
- Shows timeline of events with precise timestamps
- Can see causal relationships between goroutines
- Moderate overhead (~20-30%)

**Use case**: Understanding goroutine behavior, diagnosing deadlocks, performance debugging

```go
import "runtime/trace"

// Start tracing
trace.Start(os.Stdout)
defer trace.Stop()

// Add custom regions
trace.WithRegion(ctx, "gpuCompute", func() {
    // GPU work here
})
```

### 4. go tool trace

**What it is**: Web-based visualizer for trace files.

**Key characteristics**:
- Interactive timeline view
- Shows goroutines, procs, network, syscalls
- Zoom, filter, and navigate events
- Similar to Chrome DevTools Performance tab

**Use case**: Visual debugging, understanding concurrency patterns

```bash
go tool trace trace.out
```

## How GPU Traces Fit In

### The Problem

GPU execution happens on a completely separate processor with its own timeline:

```
CPU (Go):     [func1] → [mlx.Eval] ──wait──→ [func2]
                          ↓
                          submit GPU work

GPU (Metal):            [kernel1] → [kernel2] → [kernel3]
                        ^                       ^
                        start                   complete
```

Traditional Go profiling tools don't see GPU work:
- pprof shows CPU waiting but doesn't know why
- trace shows goroutine blocked but not what GPU is doing
- Flight recorder has no visibility into Metal driver

### The Solution: Unified Timeline

Integrate GPU trace data with Go profiling to create a complete picture:

```
Timeline:  0ms    10ms    20ms    30ms    40ms
CPU:      [────Go Code────][wait][─Continue─]
                           ↓
GPU:                    [kernel1][kernel2]
                        ^                ^
                        GPU start        GPU done
```

## Integration Strategies

### Strategy 1: pprof Labels (Simplest)

Use pprof labels to mark GPU-related operations:

```go
import "runtime/pprof"

func (arr *Array) Eval() {
    labels := pprof.Labels("operation", "gpu_eval", "device", "mps")
    pprof.Do(context.Background(), labels, func(ctx context.Context) {
        // Call Metal compute
        metalEval(arr)
    })
}
```

When you generate a CPU profile, you'll see time attributed to `gpu_eval` label.

### Strategy 2: Trace Regions (Timeline)

Emit trace events that correspond to GPU operations:

```go
import "runtime/trace"

func LaunchKernel(name string) {
    ctx := context.Background()

    // CPU-side setup
    trace.WithRegion(ctx, "gpu_setup_"+name, func() {
        setupBuffers()
        encodeCommands()
    })

    // GPU execution (async)
    task := trace.NewTask(ctx, "gpu_kernel_"+name)
    defer task.End()

    // Submit and wait
    trace.WithRegion(ctx, "gpu_wait_"+name, func() {
        commandBuffer.Commit()
        commandBuffer.WaitUntilCompleted()
    })
}
```

In `go tool trace`, you'll see:
- `gpu_setup_matmul` region on CPU
- `gpu_kernel_matmul` task spanning GPU execution
- `gpu_wait_matmul` region showing CPU blocked

### Strategy 3: Merged pprof (Best for Analysis)

Convert GPU trace to pprof format and merge with CPU profile:

```go
// Load CPU profile
cpuProfile, _ := pprof.Parse(cpuFile)

// Load GPU trace and convert
gpuTrace, _ := gputrace.Open("trace.gputrace")
gpuProfile, _ := gpuTrace.ToPprof()

// Merge profiles
merged := mergeProfiles(cpuProfile, gpuProfile)

// Now you have a single profile showing:
// - CPU functions with their time
// - GPU kernels as "functions" with their time
// - The relationship between them
```

The merged profile creates a virtual call stack:
```
main                    100%
├─ matmul_cpu           40%
└─ matmul_gpu           60%
   ├─ GPU CommandQueue  60%
   │  ├─ Stage1         20%
   │  ├─ Stage2         25%
   │  └─ Stage3         15%
```

### Strategy 4: Flight Recorder Integration (Advanced)

Continuously capture both CPU and GPU activity:

```go
type UnifiedRecorder struct {
    cpuTraceBuffer *circularBuffer
    gpuTraceRing   *gpuTraceRing
    correlation    map[uint64]gpuEvent  // correlate by timestamp
}

func (r *UnifiedRecorder) Start() {
    // Enable Go flight recorder
    trace.Start(r.cpuTraceBuffer)

    // Enable GPU capture
    os.Setenv("MTL_CAPTURE_ENABLED", "1")

    // Periodically rotate .gputrace files
    go r.rotateGPUTraces()
}

func (r *UnifiedRecorder) Dump(reason string) {
    // Capture last N seconds of both
    cpuEvents := r.cpuTraceBuffer.LastNSeconds(10)
    gpuEvents := r.gpuTraceRing.LastNSeconds(10)

    // Correlate by timestamp
    unified := r.correlate(cpuEvents, gpuEvents)

    // Write merged trace
    unified.WriteTo(fmt.Sprintf("incident_%s.trace", reason))
}
```

## Practical Example: MLX Matrix Multiply

### Current State (CPU-only profiling)

```go
func main() {
    f, _ := os.Create("cpu.pprof")
    pprof.StartCPUProfile(f)
    defer pprof.StopCPUProfile()

    a := mlx.RandomUniform(0, 1, []int{1024, 1024}, mlx.Float32)
    b := mlx.RandomUniform(0, 1, []int{1024, 1024}, mlx.Float32)

    c := mlx.MatMul(a, b)
    mlx.Eval(c)
}
```

Profile shows:
```
50ms total
├─ mlx.MatMul        5ms   (graph construction)
└─ mlx.Eval          45ms  (but we don't know why!)
```

### Enhanced State (Unified profiling)

```go
import (
    "github.com/tmc/mlx-go/experiments/mlxprof"
    "github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
    // Start unified profiling
    prof := mlxprof.Start(mlxprof.Config{
        CPUProfile:      "cpu.pprof",
        TraceOutput:     "trace.out",
        GPUTraceDir:     "gputraces/",
        EnableGPUTiming: true,
    })
    defer prof.Stop()

    // Your code with labels
    pprof.Do(context.Background(),
        pprof.Labels("operation", "matmul"),
        func(ctx context.Context) {
            a := mlx.RandomUniform(0, 1, []int{1024, 1024}, mlx.Float32)
            b := mlx.RandomUniform(0, 1, []int{1024, 1024}, mlx.Float32)

            trace.WithRegion(ctx, "gpu_matmul", func() {
                c := mlx.MatMul(a, b)
                mlx.Eval(c)
            })
        })
}
```

Profile now shows:
```
50ms total
├─ CPU: mlx.MatMul           5ms   (graph construction)
└─ GPU: matmul kernel       42ms
   ├─ setup buffers          1ms
   ├─ encode commands        1ms
   ├─ GPU execution         38ms   ← from .gputrace!
   │  ├─ load_tiles         12ms
   │  ├─ multiply           20ms
   │  └─ store_result        6ms
   └─ wait/sync             2ms
```

## Implementation Roadmap

### Phase 1: Basic Labels (✅ Already possible)
- Use pprof.Do() with labels for GPU operations
- Shows up in flame graphs as labeled sections
- Zero code changes to MLX, pure Go runtime

### Phase 2: Trace Integration (Current work)
- Add trace.WithRegion() around GPU calls
- Emit trace.Task for GPU work
- Visible in `go tool trace` timeline
- Requires minimal MLX-Go instrumentation

### Phase 3: Profile Merging (In progress)
- Convert .gputrace to pprof format (✅ Done!)
- Merge CPU and GPU profiles
- Single flame graph showing both
- Already implemented in mlxprof package

### Phase 4: Flight Recorder (Future)
- Circular buffer for .gputrace files
- Automatic capture on performance issues
- Timestamp correlation between CPU and GPU
- Post-mortem debugging capability

## Code Example: Full Integration

```go
package main

import (
    "context"
    "runtime/pprof"
    "runtime/trace"

    "github.com/tmc/mlx-go/mlx"
    "github.com/tmc/mlx-go/experiments/mlxprof"
)

func main() {
    // Setup unified profiling
    prof := mlxprof.Start(mlxprof.Config{
        CPUProfile:      "unified_cpu.pprof",
        GPUProfile:      "unified_gpu.pprof",
        CombinedProfile: "unified_combined.pprof",
        TraceOutput:     "unified.trace",
        EnableGPUTiming: true,
    })
    defer prof.Stop()

    // Your ML workload
    runTraining()
}

func runTraining() {
    ctx := context.Background()

    // Initialize model (CPU work)
    trace.WithRegion(ctx, "init_model", func() {
        model := createModel()
    })

    // Training loop
    for epoch := 0; epoch < 10; epoch++ {
        // Label the epoch
        pprof.Do(ctx, pprof.Labels("epoch", fmt.Sprint(epoch)), func(ctx context.Context) {

            // Forward pass (mostly GPU)
            trace.WithRegion(ctx, "forward", func() {
                output := model.Forward(input)
                mlx.Eval(output)
            })

            // Backward pass (GPU)
            trace.WithRegion(ctx, "backward", func() {
                loss := computeLoss(output, target)
                grads := loss.Backward()
                mlx.Eval(grads...)
            })

            // Update weights (CPU + small GPU)
            trace.WithRegion(ctx, "update", func() {
                optimizer.Step(grads)
            })
        })
    }
}
```

View results:
```bash
# View CPU profile
go tool pprof unified_cpu.pprof

# View GPU profile
go tool pprof unified_gpu.pprof

# View combined profile (best!)
go tool pprof unified_combined.pprof

# View timeline
go tool trace unified.trace
```

## Benefits of Integration

1. **Complete Picture**: See both CPU and GPU utilization in one view
2. **Find Bottlenecks**: Is your code CPU-bound or GPU-bound?
3. **Optimize Transfers**: See time spent copying data to/from GPU
4. **Debug Hangs**: Understand if GPU is stuck or CPU is waiting
5. **Post-Mortem**: Flight recorder captures both CPU and GPU state
6. **Production Ready**: Low overhead suitable for production use

## References

- [Go Execution Tracer Design](https://go.dev/src/runtime/trace/doc.go)
- [pprof Documentation](https://pkg.go.dev/runtime/pprof)
- [runtime/trace Package](https://pkg.go.dev/runtime/trace)
- [Profiling Go Programs](https://go.dev/blog/pprof)
- [Go Execution Tracer](https://go.dev/doc/diagnostics#execution-tracer)
