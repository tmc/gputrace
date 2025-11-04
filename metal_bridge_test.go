// +build metal

package gputrace

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

	if bridge.device == nil {
		t.Fatal("Device should not be nil")
	}
	if bridge.commandQueue == nil {
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
	return (*[1 << 30]byte)(unsafe.Pointer(&data[0]))[:len(data)*4:len(data)*4]
}
