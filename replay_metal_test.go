// +build metal

package gputrace

import (
	"testing"
	"unsafe"
)

const simpleComputeKernel = `
#include <metal_stdlib>
using namespace metal;

kernel void vector_add(device const float* a [[buffer(0)]],
                       device const float* b [[buffer(1)]],
                       device float* c [[buffer(2)]],
                       uint id [[thread_position_in_grid]])
{
    c[id] = a[id] + b[id];
}
`

func TestMetalReplayEngineInit(t *testing.T) {
	// Create a minimal trace (would normally come from actual .gputrace file)
	trace := &Trace{
		Path: "/tmp/test.gputrace",
	}

	engine, err := NewMetalReplayEngine(trace)
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer engine.Close()

	if engine.Bridge == nil {
		t.Fatal("Metal bridge should not be nil")
	}

	if engine.MetalBuffers == nil {
		t.Fatal("MetalBuffers map should not be nil")
	}

	if engine.MetalFunctions == nil {
		t.Fatal("MetalFunctions map should not be nil")
	}

	if engine.MetalPipelines == nil {
		t.Fatal("MetalPipelines map should not be nil")
	}
}

func TestMetalReplayEngineBufferRestoration(t *testing.T) {
	trace := &Trace{
		Path: "/tmp/test.gputrace",
	}

	engine, err := NewMetalReplayEngine(trace)
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer engine.Close()

	// Manually add buffer data to state for testing
	testData := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	metalBuffer, err := engine.Bridge.CreateBuffer(testData)
	if err != nil {
		t.Fatalf("Failed to create test buffer: %v", err)
	}
	defer metalBuffer.Release()

	testAddr := uint64(0x1000)
	engine.MetalBuffers[testAddr] = metalBuffer

	// Verify buffer is stored
	if _, ok := engine.MetalBuffers[testAddr]; !ok {
		t.Error("Buffer should be stored in MetalBuffers map")
	}

	// Read back buffer contents
	readData, err := engine.ReadBackBuffer(testAddr)
	if err != nil {
		t.Fatalf("Failed to read back buffer: %v", err)
	}

	// Verify contents match
	if len(readData) != len(testData) {
		t.Errorf("Buffer length mismatch: got %d, want %d", len(readData), len(testData))
	}

	for i := 0; i < len(testData); i++ {
		if readData[i] != testData[i] {
			t.Errorf("Buffer data mismatch at %d: got %d, want %d", i, readData[i], testData[i])
		}
	}
}

func TestMetalReplayEngineShaderCompilation(t *testing.T) {
	trace := &Trace{
		Path: "/tmp/test.gputrace",
	}

	engine, err := NewMetalReplayEngine(trace)
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer engine.Close()

	// Compile a test kernel
	function, err := engine.Bridge.CreateFunction(simpleComputeKernel, "vector_add")
	if err != nil {
		t.Fatalf("Failed to compile kernel: %v", err)
	}
	defer function.Release()

	// Create pipeline
	pipeline, err := engine.Bridge.CreatePipeline(function)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	defer pipeline.Release()

	// Store in engine maps
	testAddr := uint64(0x2000)
	engine.MetalFunctions[testAddr] = function
	engine.MetalPipelines[testAddr] = pipeline

	// Verify storage
	if _, ok := engine.MetalFunctions[testAddr]; !ok {
		t.Error("Function should be stored in MetalFunctions map")
	}

	if _, ok := engine.MetalPipelines[testAddr]; !ok {
		t.Error("Pipeline should be stored in MetalPipelines map")
	}
}

func TestMetalReplayEngineSimpleExecution(t *testing.T) {
	trace := &Trace{
		Path: "/tmp/test.gputrace",
	}

	engine, err := NewMetalReplayEngine(trace)
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer engine.Close()

	// Compile kernel
	function, err := engine.Bridge.CreateFunction(simpleComputeKernel, "vector_add")
	if err != nil {
		t.Fatalf("Failed to compile kernel: %v", err)
	}
	defer function.Release()

	pipeline, err := engine.Bridge.CreatePipeline(function)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	defer pipeline.Release()

	// Create test buffers
	arraySize := 256
	inputA := make([]float32, arraySize)
	inputB := make([]float32, arraySize)
	output := make([]float32, arraySize)

	for i := 0; i < arraySize; i++ {
		inputA[i] = float32(i)
		inputB[i] = float32(i * 2)
	}

	bufferA, err := engine.Bridge.CreateBuffer(float32ToBytes(inputA))
	if err != nil {
		t.Fatalf("Failed to create buffer A: %v", err)
	}
	defer bufferA.Release()

	bufferB, err := engine.Bridge.CreateBuffer(float32ToBytes(inputB))
	if err != nil {
		t.Fatalf("Failed to create buffer B: %v", err)
	}
	defer bufferB.Release()

	bufferC, err := engine.Bridge.CreateBuffer(float32ToBytes(output))
	if err != nil {
		t.Fatalf("Failed to create buffer C: %v", err)
	}
	defer bufferC.Release()

	// Store buffers in engine
	engine.MetalBuffers[0x1000] = bufferA
	engine.MetalBuffers[0x2000] = bufferB
	engine.MetalBuffers[0x3000] = bufferC
	engine.MetalPipelines[0x4000] = pipeline

	// Create and encode command
	cmd := ReplayCommand{
		Type:         "compute_dispatch",
		FunctionAddr: 0x4000,
		BufferBindings: []uint64{0x1000, 0x2000, 0x3000},
		ThreadsPerGrid: [3]uint32{uint32(arraySize), 1, 1},
		ThreadsPerThreadgroup: [3]uint32{64, 1, 1},
	}

	// Execute
	cmdBuffer := engine.Bridge.CreateCommandBuffer()
	encoder := cmdBuffer.CreateComputeEncoder()

	err = engine.encodeCommand(encoder, cmd)
	if err != nil {
		t.Fatalf("Failed to encode command: %v", err)
	}

	encoder.EndEncoding()
	cmdBuffer.Commit()
	cmdBuffer.WaitUntilCompleted()

	// Read back results
	resultData, err := engine.ReadBackBuffer(0x3000)
	if err != nil {
		t.Fatalf("Failed to read back results: %v", err)
	}

	results := bytesToFloat32(resultData)

	// Verify results
	for i := 0; i < arraySize; i++ {
		expected := inputA[i] + inputB[i]
		if results[i] != expected {
			t.Errorf("Result[%d] = %f, expected %f", i, results[i], expected)
			if i > 10 {
				t.Fatal("Too many errors, stopping")
			}
		}
	}

	t.Logf("Successfully executed Metal replay: %d elements", arraySize)
}

func TestMetalReplayEngineClose(t *testing.T) {
	trace := &Trace{
		Path: "/tmp/test.gputrace",
	}

	engine, err := NewMetalReplayEngine(trace)
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}

	// Create some resources
	buffer, _ := engine.Bridge.CreateBuffer([]byte{1, 2, 3, 4})
	engine.MetalBuffers[0x1000] = buffer

	// Close should release all resources
	err = engine.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// After close, bridge should be closed (this would cause errors if we tried to use it)
	// We don't test this directly as it would crash, but we verify Close() succeeds
}

// Helper functions
func float32ToBytes(data []float32) []byte {
	if len(data) == 0 {
		return []byte{0}
	}
	return (*[1 << 30]byte)(unsafe.Pointer(&data[0]))[:len(data)*4:len(data)*4]
}

func bytesToFloat32(data []byte) []float32 {
	if len(data) == 0 {
		return []float32{}
	}
	return (*[1 << 30]float32)(unsafe.Pointer(&data[0]))[:len(data)/4:len(data)/4]
}
