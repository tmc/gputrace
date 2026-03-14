//go:build metal
// +build metal

package replay

import (
	"testing"
	"unsafe"
)

// Simple Metal kernel for testing
const testKernelSource = `
#include <metal_stdlib>
using namespace metal;

kernel void add_arrays(device const float* inA [[buffer(0)]],
                      device const float* inB [[buffer(1)]],
                      device float* result [[buffer(2)]],
                      uint index [[thread_position_in_grid]])
{
    result[index] = inA[index] + inB[index];
}
`

func TestMetalBridgeInit(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	if bridge.device.GetID() == 0 {
		t.Fatal("Device should not be nil")
	}
	if bridge.commandQueue.GetID() == 0 {
		t.Fatal("Command queue should not be nil")
	}
}

func TestMetalBridgeBufferCreation(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	// Create test data
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	// Create buffer
	buffer, err := bridge.CreateBuffer(data)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}
	defer buffer.Release()

	// Verify buffer
	if buffer.Length() != uint64(len(data)) {
		t.Errorf("Buffer length mismatch: got %d, want %d", buffer.Length(), len(data))
	}

	// Verify buffer contents
	contents := buffer.Contents()
	if contents == nil {
		t.Fatal("Buffer contents should not be nil")
	}
}

func TestMetalBridgeComputeKernel(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	// Create function
	function, err := bridge.CreateFunction(testKernelSource, "add_arrays")
	if err != nil {
		t.Fatalf("Failed to create function: %v", err)
	}
	defer function.Release()

	// Create pipeline
	pipeline, err := bridge.CreatePipeline(function)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	defer pipeline.Release()

	// Create input buffers
	arraySize := 1024
	inputA := make([]float32, arraySize)
	inputB := make([]float32, arraySize)
	output := make([]float32, arraySize)

	// Fill with test data
	for i := 0; i < arraySize; i++ {
		inputA[i] = float32(i)
		inputB[i] = float32(i * 2)
	}

	// Create Metal buffers
	bufferA, err := bridge.CreateBuffer(float32SliceToBytes(inputA))
	if err != nil {
		t.Fatalf("Failed to create buffer A: %v", err)
	}
	defer bufferA.Release()

	bufferB, err := bridge.CreateBuffer(float32SliceToBytes(inputB))
	if err != nil {
		t.Fatalf("Failed to create buffer B: %v", err)
	}
	defer bufferB.Release()

	bufferOutput, err := bridge.CreateBuffer(float32SliceToBytes(output))
	if err != nil {
		t.Fatalf("Failed to create output buffer: %v", err)
	}
	defer bufferOutput.Release()

	// Create command buffer
	cmdBuffer := bridge.CreateCommandBuffer()
	defer cmdBuffer.Release()

	// Create compute encoder
	encoder := cmdBuffer.CreateComputeEncoder()
	defer encoder.Release()

	// Set pipeline and buffers
	encoder.SetPipeline(pipeline)
	encoder.SetBuffer(bufferA, 0)
	encoder.SetBuffer(bufferB, 1)
	encoder.SetBuffer(bufferOutput, 2)

	// Dispatch
	threadsPerThreadgroup := 64
	threadgroups := (arraySize + threadsPerThreadgroup - 1) / threadsPerThreadgroup
	encoder.Dispatch(threadgroups*threadsPerThreadgroup, 1, 1, threadsPerThreadgroup, 1, 1)

	// End encoding
	encoder.EndEncoding()

	// Commit and wait
	cmdBuffer.Commit()
	cmdBuffer.WaitUntilCompleted()

	// Read results
	resultPtr := bufferOutput.Contents()
	resultSlice := (*[1 << 30]float32)(resultPtr)[:arraySize:arraySize]

	// Verify results
	for i := 0; i < arraySize; i++ {
		expected := inputA[i] + inputB[i]
		if resultSlice[i] != expected {
			t.Errorf("Result[%d] = %f, expected %f", i, resultSlice[i], expected)
			if i > 10 {
				t.Fatal("Too many errors, stopping")
			}
		}
	}

	t.Logf("Successfully executed Metal compute kernel: %d elements", arraySize)
}

// Helper function to convert float32 slice to byte slice
func float32SliceToBytes(data []float32) []byte {
	return (*[1 << 30]byte)(unsafe.Pointer(&data[0]))[: len(data)*4 : len(data)*4]
}

func TestMetalBridgeQueryCounterSets(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	counterSets, err := bridge.QueryCounterSets()
	if err != nil {
		t.Fatalf("Failed to query counter sets: %v", err)
	}

	if len(counterSets) == 0 {
		t.Skip("No counter sets available on this device")
	}

	t.Logf("Found %d counter sets:", len(counterSets))
	for i, set := range counterSets {
		t.Logf("  [%d] %s", i, set.Name())
	}

	// Clean up
	for _, set := range counterSets {
		set.Release()
	}
}

func TestMetalBridgeCounterSampleBuffer(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	// Query counter sets
	counterSets, err := bridge.QueryCounterSets()
	if err != nil || len(counterSets) == 0 {
		t.Skip("No counter sets available")
	}
	defer func() {
		for _, set := range counterSets {
			set.Release()
		}
	}()

	// Create sample buffer with first counter set
	sampleCount := 10
	sampleBuffer, err := bridge.CreateCounterSampleBuffer(counterSets[0], sampleCount)
	if err != nil {
		t.Fatalf("Failed to create counter sample buffer: %v", err)
	}
	defer sampleBuffer.Release()

	// Verify sample count
	if sampleBuffer.SampleCount() != sampleCount {
		t.Errorf("Sample count mismatch: got %d, want %d", sampleBuffer.SampleCount(), sampleCount)
	}

	t.Logf("Successfully created counter sample buffer with %d samples for counter set '%s'",
		sampleCount, counterSets[0].Name())
}

func TestMetalBridgeCounterSampling(t *testing.T) {
	bridge, err := NewMetalBridge()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer bridge.Close()

	// Check what type of counter sampling is supported
	supportsExplicit := bridge.SupportsExplicitCounterSampling()
	supportsStage := bridge.SupportsStageBoundaryCounterSampling()

	t.Logf("Counter sampling support: explicit=%v, stage_boundary=%v", supportsExplicit, supportsStage)

	if !supportsExplicit && !supportsStage {
		t.Skip("No counter sampling support on this device")
	}

	// Query counter sets
	counterSets, err := bridge.QueryCounterSets()
	if err != nil || len(counterSets) == 0 {
		t.Skip("No counter sets available")
	}
	defer func() {
		for _, set := range counterSets {
			set.Release()
		}
	}()

	// Create sample buffer
	sampleBuffer, err := bridge.CreateCounterSampleBuffer(counterSets[0], 4)
	if err != nil {
		t.Fatalf("Failed to create counter sample buffer: %v", err)
	}
	defer sampleBuffer.Release()

	// Compile test kernel
	function, err := bridge.CreateFunction(testKernelSource, "add_arrays")
	if err != nil {
		t.Fatalf("Failed to compile kernel: %v", err)
	}
	defer function.Release()

	pipeline, err := bridge.CreatePipeline(function)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	defer pipeline.Release()

	// Create test buffers
	arraySize := 64
	inputA := make([]float32, arraySize)
	inputB := make([]float32, arraySize)
	output := make([]float32, arraySize)

	for i := 0; i < arraySize; i++ {
		inputA[i] = float32(i)
		inputB[i] = float32(i * 2)
	}

	bufferA, _ := bridge.CreateBuffer(float32SliceToBytes(inputA))
	defer bufferA.Release()
	bufferB, _ := bridge.CreateBuffer(float32SliceToBytes(inputB))
	defer bufferB.Release()
	bufferC, _ := bridge.CreateBuffer(float32SliceToBytes(output))
	defer bufferC.Release()

	// Execute with counter sampling
	cmdBuffer := bridge.CreateCommandBuffer()

	var encoder *MetalComputeEncoderHandle
	if supportsStage && !supportsExplicit {
		// Apple Silicon: use stage boundary sampling via pass descriptor
		t.Log("Using stage boundary counter sampling")
		encoder = cmdBuffer.CreateComputeEncoderWithStageSampling(sampleBuffer)
	} else {
		// Intel/AMD: use explicit sampling
		t.Log("Using explicit counter sampling")
		encoder = cmdBuffer.CreateComputeEncoder()
		encoder.SampleCounters(sampleBuffer, 0)
	}

	// Set pipeline and execute
	encoder.SetPipeline(pipeline)
	encoder.SetBuffer(bufferA, 0)
	encoder.SetBuffer(bufferB, 1)
	encoder.SetBuffer(bufferC, 2)
	encoder.Dispatch(arraySize, 1, 1, 32, 1, 1)

	if supportsExplicit {
		// Sample after dispatch
		encoder.SampleCounters(sampleBuffer, 1)
	}

	encoder.EndEncoding()
	cmdBuffer.Commit()
	cmdBuffer.WaitUntilCompleted()

	// Resolve counter samples
	data, err := cmdBuffer.ResolveCounterSamples(sampleBuffer, 0, 2)
	if err != nil {
		t.Fatalf("Failed to resolve counter samples: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("Resolved counter data is empty")
	}

	t.Logf("Successfully collected %d bytes of counter data from %d samples", len(data), 2)
	t.Logf("Counter set: %s", counterSets[0].Name())

	// Clean up
	encoder.Release()
	cmdBuffer.Release()
}
