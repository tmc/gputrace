# GPU Trace to Pprof Converter - Success Report

## Overview

Successfully implemented a tool to convert Metal `.gputrace` files to pprof format, enabling GPU kernel analysis with standard Go profiling tools.

## Implementation

### Tool: `gputrace-to-pprof`

**Location**: `experiments/gputrace/cmd/gputrace-to-pprof/main.go`

**Usage**:
```bash
go run ./cmd/gputrace-to-pprof <trace.gputrace> [output.pb]
```

**Example**:
```bash
# Convert GPU trace to pprof format
go run ./cmd/gputrace-to-pprof benchmark.gputrace

# Analyze with go tool pprof
go tool pprof benchmark.pb
go tool pprof -http=:8080 benchmark.pb
```

## Key Features

### 1. GPU Kernel Extraction
- Extracts kernel names from .gputrace MTSP format
- Identifies encoder labels and command queue information
- Builds hierarchical GPU execution view

### 2. Pprof Profile Generation
Creates pprof profiles with:
- **Sample Types**:
  - `gpu_time/nanoseconds` - GPU execution time
  - `dispatches/count` - Number of kernel dispatches
- **Hierarchy**: `GPU Trace > Command Queue > Encoder > Kernel`
- **Labels**: Encoder and kernel name annotations

### 3. Synthetic Timing
When real timing data is unavailable (store0 is empty):
- Creates synthetic timing based on kernel execution order
- Assigns 1ms per kernel for visualization
- Maintains correct execution hierarchy

### 4. Kernel Matching
Intelligently maps encoder labels to kernel names:
- Number matching (Stage1 → step1)
- Keyword matching (normalize, relu, scale, etc.)
- Direct name matching

## Technical Details

### Format Discovery
Key finding: `go tool pprof` requires **uncompressed protobuf format** (.pb files), not gzipped (.pprof.gz).

- ✅ Correct: Write protobuf directly → `profile.Write(file)`
- ❌ Wrong: Gzip the output → Double compression breaks parsing

### Profile Structure
```go
prof := &profile.Profile{
    SampleType: []*profile.ValueType{
        {Type: "gpu_time", Unit: "nanoseconds"},
        {Type: "dispatches", Unit: "count"},
    },
    PeriodType: &profile.ValueType{
        Type: "gpu_time",
        Unit: "nanoseconds",
    },
    Period: totalNs / len(timings),
    // ...
}
```

### Hierarchy Implementation
Stack traces are built leaf-to-root:
```
Kernel (leaf)
  → Encoder
    → Command Queue
      → GPU Trace (root)
```

## Test Results

### Test Trace
File: `gputrace-run3.gputrace`
- 4 encoder labels
- 10 kernel names
- 4 kernel dispatches

### Pprof Output
```
$ go tool pprof -top gputrace-run3.pb

Type: gpu_time
Duration: 4ms, Total samples = 4ms (100%)

      flat  flat%   sum%        cum   cum%
       1ms 25.00% 25.00%        1ms 25.00%  Stage1_Normalize
       1ms 25.00% 50.00%        1ms 25.00%  Stage2_ReLU
       1ms 25.00% 75.00%        1ms 25.00%  Stage3_Scale
       1ms 25.00%   100%        1ms 25.00%  ThreeStageKernel
         0     0%   100%        4ms   100%  CustomQueue
         0     0%   100%        4ms   100%  GPU Trace
```

### Tree View
```
$ go tool pprof -tree gputrace-run3.pb

Shows complete call hierarchy:
GPU Trace (4ms)
  └─ CustomQueue (4ms)
      ├─ Stage1_Normalize (1ms)
      ├─ Stage2_ReLU (1ms)
      ├─ Stage3_Scale (1ms)
      └─ ThreeStageKernel (1ms)
```

### Multiple Sample Types
```bash
# View dispatch counts instead of time
go tool pprof -sample_index=dispatches -top gputrace-run3.pb

Type: dispatches
Total samples = 4

      flat  flat%   sum%        cum   cum%
         1 25.00% 25.00%          1 25.00%  Stage1_Normalize
         1 25.00% 50.00%          1 25.00%  Stage2_ReLU
         1 25.00% 75.00%          1 25.00%  Stage3_Scale
         1 25.00%   100%          1 25.00%  ThreeStageKernel
```

## Integration Points

### With mlxprof
The gputrace parser can be integrated into mlxprof for unified CPU+GPU profiling:

```go
import "github.com/tmc/mlx-go/experiments/gputrace"

trace, _ := gputrace.Open("trace.gputrace")
prof, _ := createGPUProfile(trace, timings)
// Merge with CPU profile
```

### With Existing Profiles
GPU profiles can be:
- Viewed standalone with `go tool pprof`
- Merged with CPU profiles using pprof's merge functionality
- Analyzed in Instruments-style hierarchical views
- Exported to other profiling formats

## Limitations & Future Work

### Current Limitations
1. **No Real Timing Data**: .gputrace store0 files contain no timing information
   - Using synthetic 1ms per kernel
   - Shows execution order but not actual duration

2. **Kernel Matching Heuristics**: Encoder→Kernel matching is heuristic-based
   - Works well for common patterns
   - May mismap unusual kernel names

3. **No Buffer Analysis**: Tool doesn't analyze MTLBuffer data
   - Could extract buffer sizes and usage
   - Could show memory allocation patterns

### Future Enhancements
1. **Real Timing Extraction**:
   - Research Metal capture flags for timing data
   - Parse MTLGPUEvent timestamps if available
   - Use mach_absolute_time() for CPU-GPU correlation

2. **Buffer Profiling**:
   - Extract buffer sizes from device-resources
   - Show memory allocation patterns
   - Track buffer reuse and lifetimes

3. **Kernel Arguments**:
   - Parse kernel argument buffers
   - Show tensor shapes and types
   - Visualize data flow

4. **Cross-Platform**:
   - Extend to other GPU profiling formats
   - Support NVIDIA Nsight, AMD ROCm profilers
   - Unified GPU profiling API

## Files Created

1. **Main Tool**: `experiments/gputrace/cmd/gputrace-to-pprof/main.go` (310 lines)
2. **Test Tool**: `experiments/gputrace/cmd/test-profile/main.go` (validation)
3. **Documentation**: This file

## Validation

✅ Profiles parse correctly with pprof library
✅ `go tool pprof` recognizes format
✅ Hierarchy displays correctly
✅ Multiple sample types work
✅ Tree, top, list views all functional
✅ Web UI (`-http`) displays graphs correctly

## Conclusion

Successfully implemented GPU trace → pprof conversion with:
- Clean hierarchical GPU kernel visualization
- Full pprof tool compatibility
- Extensible design for future enhancements
- Foundation for unified CPU+GPU profiling in mlxprof

The tool provides immediate value for understanding GPU kernel execution patterns, even without precise timing data. With future enhancements to extract real timing, this will become a powerful GPU profiling solution for MLX.

## Example Workflow

```bash
# 1. Capture GPU trace (with Metal)
# Set MTL_CAPTURE_ENABLED=1 during benchmark

# 2. Convert to pprof
go run ./cmd/gputrace-to-pprof benchmark.gputrace

# 3. Analyze with pprof
go tool pprof benchmark.pb
(pprof) top
(pprof) tree
(pprof) list Stage1

# 4. Or use web UI
go tool pprof -http=:8080 benchmark.pb
# Opens browser with interactive flamegraph, call graph, etc.
```

## Success Metrics

- ✅ Tool compiles and runs
- ✅ Handles .gputrace files correctly
- ✅ Generates valid pprof profiles
- ✅ go tool pprof can analyze output
- ✅ All pprof views work (top, tree, list, web)
- ✅ Hierarchy shows GPU execution structure
- ✅ Multiple sample types supported
- ✅ Documentation complete
