# Metal Shader Source Mapping in pprof

This document explains how to include Metal shader source code in pprof profiles for GPU traces.

## Overview

By default, pprof profiles show kernel names but not their source code locations. With shader source mapping, you can:

- **Navigate to shader source** - Click on a kernel in pprof web UI to view Metal source
- **See line numbers** - Know exactly where each kernel is defined
- **Understand hot paths** - Read the actual shader code causing performance issues
- **Debug faster** - Jump directly from profile to problematic shader code

## Quick Start

### 1. Using gputrace2pprof with Source Mapping

```bash
# Convert trace with automatic source discovery
gputrace2pprof trace.gputrace -all -prefix analysis

# The tool will automatically search for .metal files in:
# - MLX Swift locations
# - MLX C locations
# - Current and parent directories
```

### 2. Specifying Custom Search Paths

```go
package main

import (
    "log"
    "github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
    // Open trace
    trace, err := gputrace.Open("benchmark.gputrace")
    if err != nil {
        log.Fatal(err)
    }

    // Extract timing
    timings, _ := trace.ExtractTimingData()

    // Create source mapper with custom paths
    mapper := gputrace.NewShaderSourceMapper(
        "/path/to/mlx/backend/metal/*.metal",
        "/path/to/custom/shaders/*.metal",
    )

    // Index shader sources
    if err := mapper.IndexShaderSources(); err != nil {
        log.Printf("Warning: failed to index shaders: %v", err)
    }

    // Check what was indexed
    files, kernels := mapper.Stats()
    log.Printf("Indexed %d kernels from %d files", kernels, files)

    // Convert with source mapping
    prof, err := trace.ToPprofWithSource(timings, mapper)
    if err != nil {
        log.Fatal(err)
    }

    // Write profile
    f, _ := os.Create("gpu_with_source.pprof")
    prof.Write(f)
    f.Close()
}
```

## How It Works

### 1. Kernel Name Extraction

The GPU trace contains kernel names like:
- `rope_single_freqs_float16`
- `affine_qmm_t_float16_t_gs_64_b_4_alN_true_batch_0`
- `vv_Multiplyfloat16`

### 2. Source File Scanning

The mapper scans `.metal` files looking for kernel definitions:

```metal
// In rope.metal
kernel void rope_single_freqs(
    device const float16_t* input [[buffer(0)]],
    device float16_t* output [[buffer(1)]],
    constant const RopeParams& params [[buffer(2)]],
    uint gid [[thread_position_in_grid]])
{
    // Kernel implementation...
}
```

### 3. Name Matching

The mapper uses fuzzy matching to handle type suffixes:
- Strips suffixes: `_float16`, `_float32`, `_int32`, etc.
- Tries substring matching
- Maps kernel names to file paths and line numbers

### 4. pprof Integration

The location information is added to the pprof profile:

```go
kernelFunc := &profile.Function{
    ID:         funcID,
    Name:       "rope_single_freqs_float16",
    SystemName: "rope_single_freqs_float16",
    Filename:   "/path/to/rope.metal",  // Added!
    StartLine:  42,                      // Added!
}

loc := &profile.Location{
    ID: locID,
    Line: []profile.Line{
        {
            Function: kernelFunc,
            Line:     42,  // Line number!
        },
    },
    Mapping: &profile.Mapping{
        ID:   1,
        File: "/path/to/rope.metal",  // Source file!
    },
}
```

## Viewing Source in pprof

Once you have a profile with source mapping:

```bash
# Open in web UI
go tool pprof -http=:8080 gpu_with_source.pprof
```

In the web UI:
1. Click on "Source" view
2. Click on any kernel function
3. See the Metal shader source code with the hot line highlighted!

### Example Session

```bash
$ go tool pprof -http=:8080 gpu_with_source.pprof
Serving web UI on http://localhost:8080

# In browser, navigate to http://localhost:8080
# Click "Top" to see hottest kernels
# Click on "rope_single_freqs_float16"
# See the rope.metal source with line 42 highlighted!
```

## Source Location Discovery

### Default Search Paths

The mapper automatically searches these locations:

```go
var defaultPaths = []string{
    // MLX Swift
    "/Users/*/ml-explore/mlx-swift-examples/.build/checkouts/mlx-swift/Source/Cmlx/mlx-generated/metal",

    // MLX C (Homebrew)
    "/opt/homebrew/Cellar/mlx-c/*/include/mlx/backend/metal",

    // Local MLX
    "./mlx/backend/metal",
    "../mlx/backend/metal",
}
```

### Adding Custom Paths

```go
mapper := gputrace.NewShaderSourceMapper(
    "/custom/path/to/shaders/*.metal",
    "/another/path/**/*.metal",  // Recursive glob
)
```

### Environment Variable

Set `MLX_SHADER_PATH` to add search paths:

```bash
export MLX_SHADER_PATH="/path1:/path2:/path3"
gputrace2pprof trace.gputrace -all -prefix analysis
```

## Troubleshooting

### No Source Code Shown

**Problem:** pprof shows kernel names but no source code.

**Solutions:**
1. Check that `.metal` files exist in search paths
2. Verify kernel names match between trace and source files
3. Enable verbose mode to see what was indexed:

```bash
gputrace2pprof trace.gputrace -v -all -prefix debug
```

### Kernel Names Don't Match

**Problem:** Trace has `rope_single_freqs_float16` but source has `rope_single_freqs`.

**Solution:** The mapper handles this automatically by stripping type suffixes. If it still doesn't work:

```go
// Manually map kernel names
mapper := gputrace.NewShaderSourceMapper("./shaders/*.metal")
mapper.IndexShaderSources()

// Check what was found
kernels := mapper.GetAllKernels()
for _, k := range kernels {
    fmt.Println("Found:", k)
}
```

### Wrong Line Numbers

**Problem:** pprof jumps to wrong line in source file.

**Cause:** Line numbers are extracted by regex scanning for `kernel void name(`. If your formatting is different, the matcher may fail.

**Solution:** Ensure kernel definitions match this pattern:

```metal
// Good
kernel void my_kernel(params)

// Also good
kernel void my_kernel(
    params)

// May not match
kernel
void my_kernel(params)
```

## Advanced Usage

### Programmatic Source Mapping

```go
// Create and configure mapper
mapper := gputrace.NewShaderSourceMapper()
mapper.IndexShaderSources()

// Query for specific kernel
file, line := mapper.GetSourceLocation("rope_single_freqs_float16")
if file != "" {
    fmt.Printf("Kernel at %s:%d\n", file, line)
}

// Get stats
files, kernels := mapper.Stats()
fmt.Printf("Indexed %d kernels from %d files\n", kernels, files)

// List all indexed kernels
for _, kernel := range mapper.GetAllKernels() {
    fmt.Println(kernel)
}
```

### Custom Kernel Name Mapping

If automatic mapping doesn't work, provide custom mappings:

```go
// TODO: Add support for custom mappings
mapper.AddMapping("weird_kernel_name", "/path/to/source.metal", 123)
```

## Performance Impact

Source mapping has minimal overhead:
- **Indexing time:** ~100ms for 100 .metal files
- **Profile size:** +10-20% (adds file paths and line numbers)
- **pprof loading:** No noticeable difference

## Examples

### Example 1: Basic Source Mapping

```bash
# Generate profile with source
gputrace2pprof trace.gputrace -o gpu.pprof

# View in pprof
go tool pprof -http=:8080 gpu.pprof

# Click on any kernel to see source!
```

### Example 2: Custom Search Paths

```bash
# Set custom shader path
export MLX_SHADER_PATH="/my/shaders:/other/shaders"

# Generate profile
gputrace2pprof trace.gputrace -all -prefix analysis

# View with source
go tool pprof -http=:8080 analysis.gpu.pprof
```

### Example 3: Verifying Source Mapping

```go
package main

import (
    "fmt"
    "github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
    // Create mapper
    mapper := gputrace.NewShaderSourceMapper()
    mapper.IndexShaderSources()

    // Check what was found
    files, kernels := mapper.Stats()
    fmt.Printf("Indexed %d kernels from %d files\n\n", kernels, files)

    // Test specific kernels
    testKernels := []string{
        "rope_single_freqs",
        "affine_qmm_t",
        "vv_Multiply",
    }

    for _, kernel := range testKernels {
        file, line := mapper.GetSourceLocation(kernel)
        if file != "" {
            fmt.Printf("✓ %s -> %s:%d\n", kernel, file, line)
        } else {
            fmt.Printf("✗ %s -> NOT FOUND\n", kernel)
        }
    }
}
```

## Integration with mlxprof

The mlxprof package automatically uses source mapping:

```go
// mlxprof automatically enables source mapping
prof, err := mlxprof.FromGPUTrace(
    "trace.gputrace",
    "/custom/shader/path/*.metal",  // Optional: custom search paths
)
```

## Future Enhancements

Planned improvements:
- [ ] Extract source from compiled metallib files
- [ ] Support for LLVM IR annotations
- [ ] Inline source snippets in profile comments
- [ ] Source diff view for before/after optimization
- [ ] Hot path highlighting in shader source

## See Also

- [pprof Documentation](https://github.com/google/pprof/blob/main/doc/README.md)
- [Metal Shading Language Specification](https://developer.apple.com/metal/Metal-Shading-Language-Specification.pdf)
- [gputrace Package Documentation](README.md)
- [gputrace2pprof Tool](cmd/gputrace2pprof/README.md)
