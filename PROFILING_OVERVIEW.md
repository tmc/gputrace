# Go Profiling Tools Overview

## Quick Reference Matrix

| Tool | What | When | Overhead | Output | Best For |
|------|------|------|----------|--------|----------|
| **pprof** | Where time is spent (sampling) | During execution | ~1% | Aggregated profile | Finding hot spots |
| **trace** | Timeline of events | During execution | ~20% | Event stream | Understanding concurrency |
| **Flight Recorder** | Continuous event capture | Always-on | ~5-10% | Circular buffer | Post-mortem debugging |
| **go tool trace** | Visual timeline | After trace | N/A | Web UI | Visual debugging |
| **.gputrace** | GPU execution timeline | During capture | Varies | Metal trace | GPU profiling |

## Visual Comparison

### pprof: Aggregated View (WHERE)
```
Total: 1000ms

  ┌────────────────────────────────────┐
  │ main                         100%  │
  ├────────────────────────────────────┤
  │ ├─ matmul                     60%  │ ← Hot spot!
  │ │  ├─ kernel_launch            50%  │
  │ │  └─ buffer_copy              10%  │
  │ └─ process_results             40%  │
  └────────────────────────────────────┘
```

### trace: Timeline View (WHEN)
```
Time →   0ms     100ms    200ms    300ms

CPU 0:   [main──────────────────────────]
            └─spawn G2
CPU 1:            [G2: matmul───────]
                       │
GPU:                   [kernel1][kernel2]
                       ^         ^
                       submit    done
```

### Flight Recorder: Event Stream (WHAT)
```
Circular Buffer (last 10s):

t=0.000  G1: created
t=0.001  G1: running on P0
t=0.010  G1: syscall (Metal API)
t=0.011  G1: blocked
t=0.015  GPU: command buffer submitted
t=0.050  G1: unblocked (GPU done)
t=0.051  G1: running on P0
t=0.052  G1: exited
```

### go tool trace: Interactive Timeline
```
┌─────────────────────────────────────────────────┐
│  Goroutines ▼   Network ▼   Syscalls ▼   GC ▼  │
├─────────────────────────────────────────────────┤
│ Time: 0ms ──────────────────────── 100ms        │
│                                                  │
│ G1  [──running──][blocked────][running──]       │
│                      │                           │
│ G2         [──────running──────]                │
│                                                  │
│ P0  [─────────busy──────────────][idle]         │
│ P1  [idle][────busy────][idle]                  │
│                                                  │
│ GPU           [═══════════]                      │
│               kernel exec                        │
└─────────────────────────────────────────────────┘
```

## How They Complement Each Other

### Scenario: "My ML training is slow"

**Step 1: Use pprof to find WHERE**
```bash
go tool pprof cpu.pprof
(pprof) top
  60% matmul_forward
  20% loss_backward
  15% optimizer_step
  5%  other
```
Result: `matmul_forward` is the hot spot

**Step 2: Use trace to see WHEN and WHY**
```bash
go tool trace trace.out
```
Result: See that goroutines are blocked waiting for GPU

**Step 3: Use .gputrace to see WHAT GPU is doing**
```bash
mlxprof gputrace trace.gputrace gpu.pprof
go tool pprof gpu.pprof
```
Result: GPU kernel is memory-bound, not compute-bound

**Step 4: If problem is intermittent, use Flight Recorder**
```go
// Always running in background
prof := mlxprof.StartFlightRecorder()

// When slowdown detected:
prof.DumpRecentEvents("slowdown_detected.trace")
```
Result: Captures the exact moment performance degraded

## Integration Example

### Without Integration
```
❌ Separate, disconnected views:

CPU Profile:
  60% "waiting"  ← Why waiting?

Trace:
  G1 blocked     ← What is it waiting for?

GPU Trace:
  Kernel running ← How does this relate to CPU?
```

### With Integration (mlxprof)
```
✅ Unified view:

Combined Profile:
  main                           100%
  ├─ CPU: setup                   10%
  ├─ GPU: matmul                  60%  ← Correlated!
  │  ├─ kernel_launch              5%
  │  ├─ GPU execution             50%
  │  │  ├─ Stage1_Normalize       15%
  │  │  ├─ Stage2_ReLU            20%
  │  │  └─ Stage3_Scale           15%
  │  └─ wait_completion            5%
  └─ CPU: process_results         30%
```

## Real-World Usage Patterns

### Development: Use pprof + trace
```go
// Quick profiling during development
pprof.StartCPUProfile(f)
trace.Start(tf)

// Your code

pprof.StopCPUProfile()
trace.Stop()
```

### Production: Use Flight Recorder
```go
// Always-on in production
func init() {
    if os.Getenv("ENABLE_FLIGHT_RECORDER") == "1" {
        startFlightRecorder()
    }
}

// Dump on anomaly
func onSlowRequest(duration time.Duration) {
    if duration > threshold {
        dumpFlightRecorder("slow_request")
    }
}
```

### Optimization: Use Full Integration
```go
// Complete profiling session
prof := mlxprof.Start(mlxprof.Config{
    CPUProfile:      "cpu.pprof",
    GPUProfile:      "gpu.pprof",
    CombinedProfile: "combined.pprof",
    TraceOutput:     "trace.out",
    EnableGPUTiming: true,
})
defer prof.Stop()

// Load GPU trace afterwards
prof.AddGPUTrace("external.gputrace")
```

## Key Insights

1. **pprof answers "WHERE is the bottleneck?"**
   - Statistical sampling
   - Low overhead
   - Good for finding hot functions

2. **trace answers "WHEN does it happen?"**
   - Timeline view
   - Shows blocking, scheduling
   - Good for concurrency issues

3. **Flight recorder answers "WHAT just happened?"**
   - Always-on capture
   - Post-mortem debugging
   - Good for intermittent issues

4. **GPU trace answers "WHAT is the GPU doing?"**
   - Metal-level visibility
   - Kernel timing
   - Good for GPU optimization

5. **Integration gives COMPLETE PICTURE**
   - CPU + GPU in one view
   - Correlated timelines
   - End-to-end understanding

## Choosing the Right Tool

```
┌─────────────────────────────────────────────────┐
│ Need to find slow function?                     │
│ → Use pprof                                     │
├─────────────────────────────────────────────────┤
│ Need to understand goroutine blocking?          │
│ → Use go tool trace                             │
├─────────────────────────────────────────────────┤
│ Need to debug production issue?                 │
│ → Use flight recorder                           │
├─────────────────────────────────────────────────┤
│ Need to optimize GPU kernels?                   │
│ → Use .gputrace + Xcode Instruments            │
├─────────────────────────────────────────────────┤
│ Need complete CPU + GPU visibility?             │
│ → Use mlxprof (all of the above!)              │
└─────────────────────────────────────────────────┘
```

## Further Reading

- [Go Diagnostics](https://go.dev/doc/diagnostics)
- [Profiling Go Programs](https://go.dev/blog/pprof)
- [Go Execution Tracer](https://go.dev/doc/diagnostics#execution-tracer)
- [Metal Performance Profiling](https://developer.apple.com/metal/profiling-and-debugging/)
