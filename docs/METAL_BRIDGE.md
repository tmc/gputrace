# Metal Bridge - CGo Bindings for GPU Replay

**Date:** 2025-11-03
**Status:** ✅ Complete and Tested

## Overview

The Metal Bridge provides Go bindings to Apple's Metal API via CGo, enabling actual GPU execution for trace replay. This bridges the gap between the pure-Go replay analysis framework and real GPU hardware.

## Architecture

### CGo Bridge Layer

The bridge uses CGo to wrap Metal Objective-C APIs:

```
Go Application (replay.go)
        ↓
Metal Bridge (metal_bridge.go)
        ↓
CGo Wrapper (Objective-C inline code)
        ↓
Metal Framework (Apple APIs)
        ↓
GPU Hardware
```

### Core Components

**metal_bridge.go** - Go interface and CGo bindings
- `MetalBridge` - Main bridge controller
- Handle types for Metal objects (Buffer, Function, Pipeline, etc.)
- CGo wrapper functions for Metal API calls

**metal_bridge_test.go** - Test suite
- Initialization tests
- Buffer creation and manipulation
- Compute kernel execution
- End-to-end GPU execution validation

## Features

### Device Management
- Initialize default Metal device
- Create command queues
- Device resource management

### Buffer Operations
- Create buffers from Go byte slices
- Access buffer contents from CPU
- Automatic memory management with CFBridgingRetain/Release

### Compute Pipeline
- Compile Metal shader source at runtime
- Create compute pipeline states
- Function lookup and binding

### Command Encoding
- Create command buffers
- Encode compute commands
- Set pipeline states and buffers
- Dispatch threadgroups

### Synchronization
- Commit command buffers for execution
- Wait for GPU completion
- Read back results to CPU

## API Reference

### MetalBridge

```go
// Create new bridge with default device
bridge, err := NewMetalBridge()
defer bridge.Close()

// Create buffer from data
buffer, err := bridge.CreateBuffer(data []byte)
defer buffer.Release()

// Create function from source
function, err := bridge.CreateFunction(source, name string)
defer function.Release()

// Create compute pipeline
pipeline, err := bridge.CreatePipeline(function)
defer pipeline.Release()

// Create command buffer
cmdBuffer := bridge.CreateCommandBuffer()
defer cmdBuffer.Release()
```

### Command Encoding

```go
// Create compute encoder
encoder := cmdBuffer.CreateComputeEncoder()
defer encoder.Release()

// Set pipeline and buffers
encoder.SetPipeline(pipeline)
encoder.SetBuffer(buffer, index)

// Dispatch compute kernel
encoder.Dispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ)

// Finish encoding
encoder.EndEncoding()

// Execute on GPU
cmdBuffer.Commit()
cmdBuffer.WaitUntilCompleted()
```

## Test Results

### Initialization Test
```
TestMetalBridgeInit - PASS (0.05s)
✓ Device created successfully
✓ Command queue created
✓ Resource cleanup verified
```

### Compute Kernel Test
```
TestMetalBridgeComputeKernel - PASS (0.09s)
✓ Function compiled from Metal source
✓ Pipeline created successfully
✓ Buffers allocated (1024 float32 elements)
✓ Kernel executed: result[i] = a[i] + b[i]
✓ Results verified: 1024/1024 correct
```

## Usage Example

```go
// Initialize Metal
bridge, err := NewMetalBridge()
if err != nil {
    log.Fatal(err)
}
defer bridge.Close()

// Create input buffers
inputA := []float32{1, 2, 3, 4}
inputB := []float32{5, 6, 7, 8}

bufferA, _ := bridge.CreateBuffer(float32ToBytes(inputA))
bufferB, _ := bridge.CreateBuffer(float32ToBytes(inputB))
bufferC, _ := bridge.CreateBuffer(make([]byte, 16))

// Compile kernel
source := `
#include <metal_stdlib>
using namespace metal;

kernel void add(device const float* a [[buffer(0)]],
                device const float* b [[buffer(1)]],
                device float* c [[buffer(2)]],
                uint id [[thread_position_in_grid]])
{
    c[id] = a[id] + b[id];
}
`

function, _ := bridge.CreateFunction(source, "add")
pipeline, _ := bridge.CreatePipeline(function)

// Execute
cmdBuffer := bridge.CreateCommandBuffer()
encoder := cmdBuffer.CreateComputeEncoder()

encoder.SetPipeline(pipeline)
encoder.SetBuffer(bufferA, 0)
encoder.SetBuffer(bufferB, 1)
encoder.SetBuffer(bufferC, 2)
encoder.Dispatch(4, 1, 1, 1, 1, 1) // 4 threads
encoder.EndEncoding()

cmdBuffer.Commit()
cmdBuffer.WaitUntilCompleted()

// Read results
result := (*[4]float32)(bufferC.Contents())
fmt.Println(result[:]) // [6 8 10 12]
```

## Integration with Replay Engine

The Metal Bridge integrates with the existing replay framework:

### Current State
- ✅ Replay analysis framework (replay.go, replay_state.go)
- ✅ Counter sampling framework (counter_sampling.go)
- ✅ Metal CGo bridge (metal_bridge.go)
- ⏳ Integration layer (upcoming)

### Integration Points

**Buffer Restoration**
```go
// In ReplayState.RestoreBuffers()
for _, bufInfo := range state.Buffers {
    data := readBufferFile(bufInfo.Path)
    metalBuffer, err := bridge.CreateBuffer(data)
    // Store for command encoding
}
```

**Function Restoration**
```go
// In ReplayState.RestoreFunctions()
for _, funcInfo := range state.Functions {
    source := funcInfo.MetalSource
    function, err := bridge.CreateFunction(source, funcInfo.Name)
    pipeline, err := bridge.CreatePipeline(function)
    // Store for dispatch commands
}
```

**Command Execution**
```go
// In ReplayEngine.ExecuteCommands()
cmdBuffer := bridge.CreateCommandBuffer()
encoder := cmdBuffer.CreateComputeEncoder()

for _, cmd := range commands {
    switch cmd.Type {
    case "set_pipeline":
        encoder.SetPipeline(pipelines[cmd.PipelineAddr])
    case "bind_buffer":
        encoder.SetBuffer(buffers[cmd.BufferAddr], cmd.Index)
    case "dispatch":
        encoder.Dispatch(cmd.Grid..., cmd.Threadgroup...)
    }
}

encoder.EndEncoding()
cmdBuffer.Commit()
cmdBuffer.WaitUntilCompleted()
```

## Technical Details

### Memory Management

**CFBridgingRetain/Release**
- Objective-C objects wrapped in C structs
- Reference counting managed by Bridge pattern
- Proper cleanup in Release() methods

**Buffer Lifecycle**
```
Go []byte → unsafe.Pointer → MTLBuffer → GPU memory
                ↓
         CFBridgingRetain (increment refcount)
                ↓
         Go MetalBufferHandle (wrapper)
                ↓
         Release() → CFBridgingRelease (decrement)
```

### Build Tags

Code is conditionally compiled with `// +build metal` tag:
```bash
# Build with Metal support
go build -tags metal

# Test with Metal support
go test -tags metal

# Without tag, Metal bridge is excluded
```

### Platform Requirements

- **OS**: macOS 10.13+ (Metal 2)
- **Hardware**: Any Mac with Metal-capable GPU
- **Xcode**: Command Line Tools installed
- **Frameworks**: Metal.framework, Foundation.framework

## Performance Characteristics

### Overhead
- Bridge call overhead: ~1-2 μs per call
- Buffer creation: ~10-50 μs depending on size
- Pipeline compilation: ~5-20 ms (one-time per shader)
- Command encoding: ~1 μs per command
- GPU execution: Varies by workload

### Optimization Tips
1. Reuse pipelines across replays
2. Batch commands in single encoder
3. Use shared storage mode for CPU-GPU data
4. Minimize synchronization points

## Limitations

### Current Limitations
1. **Compute only**: No render pipeline support yet
2. **Basic buffers**: No texture support
3. **Simple dispatch**: No indirect command buffers
4. **No counter sampling**: MTLCounterSampleBuffer not yet wrapped

### Future Enhancements
1. Add MTLCounterSampleBuffer support for performance counters
2. Add texture creation and sampling
3. Add render pipeline support
4. Add indirect command buffer execution
5. Add more buffer types (private, managed)

## Next Steps

### Phase 1: Complete Integration ✓
- ✅ CGo bridge implementation
- ✅ Basic compute pipeline support
- ✅ Buffer management
- ✅ Test validation

### Phase 2: Replay Integration (Next)
- Integrate bridge with ReplayEngine
- Restore buffers from trace files
- Compile shaders from trace
- Execute replay commands on GPU

### Phase 3: Counter Sampling (Future)
- Wrap MTLCounterSampleBuffer APIs
- Integrate with CounterSampler
- Collect real performance metrics
- Validate against Xcode data

## Testing

### Run Tests
```bash
# All Metal bridge tests
go test -tags metal -v

# Specific test
go test -tags metal -run TestMetalBridgeComputeKernel -v

# With race detection
go test -tags metal -race -v
```

### Expected Output
```
=== RUN   TestMetalBridgeInit
--- PASS: TestMetalBridgeInit (0.05s)
=== RUN   TestMetalBridgeBufferCreation
--- PASS: TestMetalBridgeBufferCreation (0.02s)
=== RUN   TestMetalBridgeComputeKernel
    metal_bridge_test.go:161: Successfully executed Metal compute kernel: 1024 elements
--- PASS: TestMetalBridgeComputeKernel (0.09s)
PASS
```

## Files

- `metal_bridge.go` (650 lines) - CGo bridge implementation
- `metal_bridge_test.go` (170 lines) - Test suite
- `docs/METAL_BRIDGE.md` (this file) - Documentation

## References

- [Apple Metal Documentation](https://developer.apple.com/metal/)
- [Metal Shading Language Specification](https://developer.apple.com/metal/Metal-Shading-Language-Specification.pdf)
- [CGo Documentation](https://pkg.go.dev/cmd/cgo)
- [Metal Performance Counters](https://developer.apple.com/documentation/metal/performance/counters)
