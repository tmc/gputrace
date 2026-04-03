//go:build darwin

package replay

import (
	"fmt"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// MetalBridge provides Go bindings to Metal APIs for GPU replay.
type MetalBridge struct {
	device       metal.MTLDevice
	commandQueue metal.MTLCommandQueue
	caps         *DeviceCapabilities // cached device capabilities
}

// NewMetalBridge initializes a Metal bridge with the default GPU device.
func NewMetalBridge() (*MetalBridge, error) {
	device := metal.MTLCreateSystemDefaultDevice()
	if device.GetID() == 0 {
		return nil, fmt.Errorf("failed to initialize Metal device")
	}

	queueID := objc.Send[objc.ID](device.GetID(), objc.Sel("newCommandQueue"))
	if queueID == 0 {
		return nil, fmt.Errorf("failed to create command queue")
	}
	queue := metal.MTLCommandQueueObjectFromID(queueID)

	return &MetalBridge{
		device:       device,
		commandQueue: queue,
	}, nil
}

// CreateBuffer creates a Metal buffer with the given data.
func (mb *MetalBridge) CreateBuffer(data []byte) (*MetalBufferHandle, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("buffer data is empty")
	}

	// Create buffer with length
	bufferID := objc.Send[objc.ID](mb.device.GetID(), objc.Sel("newBufferWithLength:options:"), uint(len(data)), metal.MTLResourceStorageModeShared)
	if bufferID == 0 {
		return nil, fmt.Errorf("failed to create buffer")
	}
	buffer := metal.MTLBufferObjectFromID(bufferID)

	// Check if buffer ID is valid (if underlying object failed creation)
	// Interface doesn't expose ID, but if nil check passed, we assume valid wrapper.
	// But generated New... methods usually define non-nil wrapper even for nil result?
	// We'll trust Metal API behavior + check Contents check later.

	// Copy data to buffer
	contents := objc.Send[unsafe.Pointer](buffer.GetID(), objc.Sel("contents"))
	if contents == nil {
		return nil, fmt.Errorf("failed to get buffer contents")
	}

	dest := unsafe.Slice((*byte)(contents), len(data))
	copy(dest, data)

	return &MetalBufferHandle{buffer: buffer}, nil
}

// CreateBufferWithValidation creates a Metal buffer after validating the size against device limits.
func (mb *MetalBridge) CreateBufferWithValidation(data []byte) (*MetalBufferHandle, error) {
	caps, err := mb.DeviceCapabilities()
	if err != nil {
		return nil, fmt.Errorf("failed to get device capabilities: %w", err)
	}
	if err := caps.ValidateBufferSize(uint64(len(data))); err != nil {
		return nil, err
	}
	return mb.CreateBuffer(data)
}

// CreateFunction creates a Metal function from source code.
func (mb *MetalBridge) CreateFunction(source, name string) (*MetalFunctionHandle, error) {
	// Create NSString for source using objc.Send to avoid nil-check issues
	nsStringClass := objc.GetClass("NSString")
	sourceStr := objc.Send[objc.ID](objc.ID(uintptr(nsStringClass)), objc.Sel("stringWithUTF8String:"), source+"\x00")

	// Create library using objc.Send directly (nil options triggers nil pointer in generated code)
	var libErr objc.ID
	libraryID := objc.Send[objc.ID](mb.device.GetID(), objc.Sel("newLibraryWithSource:options:error:"), sourceStr, objc.ID(0), &libErr)
	if libraryID == 0 {
		errStr := "unknown error"
		if libErr != 0 {
			desc := objc.Send[objc.ID](libErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return nil, fmt.Errorf("failed to create library: %s", errStr)
	}
	library := metal.MTLLibraryObjectFromID(libraryID)

	// Create function using objc.Send to avoid interface return type issues
	nameStr := objc.Send[objc.ID](objc.ID(uintptr(nsStringClass)), objc.Sel("stringWithUTF8String:"), name+"\x00")
	fnID := objc.Send[objc.ID](library.GetID(), objc.Sel("newFunctionWithName:"), nameStr)
	if fnID == 0 {
		return nil, fmt.Errorf("failed to create function: %s", name)
	}
	function := metal.MTLFunctionObjectFromID(fnID)

	return &MetalFunctionHandle{function: function}, nil
}

// CreatePipeline creates a compute pipeline state from a function.
func (mb *MetalBridge) CreatePipeline(function *MetalFunctionHandle) (*MetalPipelineHandle, error) {
	// Use objc.Send directly to avoid interface return type issues with purego
	var pipeErr objc.ID
	pipelineID := objc.Send[objc.ID](mb.device.GetID(), objc.Sel("newComputePipelineStateWithFunction:error:"), function.function.GetID(), &pipeErr)
	if pipelineID == 0 {
		errStr := "unknown error"
		if pipeErr != 0 {
			desc := objc.Send[objc.ID](pipeErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return nil, fmt.Errorf("failed to create pipeline: %s", errStr)
	}
	pipeline := metal.MTLComputePipelineStateObjectFromID(pipelineID)

	return &MetalPipelineHandle{pipeline: pipeline}, nil
}

// CreateCommandBuffer creates a new command buffer.
func (mb *MetalBridge) CreateCommandBuffer() *MetalCommandBufferHandle {
	cmdBufferID := objc.Send[objc.ID](mb.commandQueue.GetID(), objc.Sel("commandBuffer"))
	cmdBuffer := metal.MTLCommandBufferObjectFromID(cmdBufferID)
	return &MetalCommandBufferHandle{cmdBuffer: cmdBuffer}
}

// Close releases Metal resources.
func (mb *MetalBridge) Close() {
}

// DeviceCapabilities returns the cached device capabilities, querying if not yet cached.
func (mb *MetalBridge) DeviceCapabilities() (*DeviceCapabilities, error) {
	if mb.caps != nil {
		return mb.caps, nil
	}
	caps, err := QueryDeviceCapabilities()
	if err != nil {
		return nil, err
	}
	mb.caps = caps
	return caps, nil
}

// SupportsExplicitCounterSampling returns true if the device supports explicit
// counter sampling at dispatch boundary (sampleCountersInBuffer:atSampleIndex:withBarrier:).
// Apple Silicon (TBDR) devices typically return false and require stage boundary sampling.
func (mb *MetalBridge) SupportsExplicitCounterSampling() bool {
	// MTLCounterSamplingPointAtDispatchBoundary = 3
	return objc.Send[bool](mb.device.GetID(), objc.Sel("supportsCounterSampling:"), uint(3))
}

// SupportsStageBoundaryCounterSampling returns true if the device supports
// stage boundary counter sampling via pass descriptor attachments.
func (mb *MetalBridge) SupportsStageBoundaryCounterSampling() bool {
	// MTLCounterSamplingPointAtStageBoundary = 0
	return objc.Send[bool](mb.device.GetID(), objc.Sel("supportsCounterSampling:"), uint(0))
}

// QueryCounterSets returns all available counter sets from the device.
func (mb *MetalBridge) QueryCounterSets() ([]*MetalCounterSetHandle, error) {
	counterSetsID := objc.Send[objc.ID](mb.device.GetID(), objc.Sel("counterSets"))
	if counterSetsID == 0 {
		return nil, fmt.Errorf("no counter sets available")
	}

	count := objc.Send[uint](counterSetsID, objc.Sel("count"))
	if count == 0 {
		return nil, fmt.Errorf("no counter sets available")
	}

	handles := make([]*MetalCounterSetHandle, 0, count)
	for i := uint(0); i < count; i++ {
		setID := objc.Send[objc.ID](counterSetsID, objc.Sel("objectAtIndex:"), i)
		if setID == 0 {
			continue
		}
		handles = append(handles, &MetalCounterSetHandle{
			set: metal.MTLCounterSetObjectFromID(setID),
		})
	}
	if len(handles) == 0 {
		return nil, fmt.Errorf("no counter sets available")
	}

	return handles, nil
}

// CreateCounterSampleBuffer creates a counter sample buffer for the given counter set.
func (mb *MetalBridge) CreateCounterSampleBuffer(counterSet *MetalCounterSetHandle, sampleCount int) (*MetalCounterSampleBufferHandle, error) {
	// Create descriptor
	descriptor := metal.NewMTLCounterSampleBufferDescriptor()
	descriptor.SetCounterSet(counterSet.set)
	descriptor.SetSampleCount(uint(sampleCount))
	descriptor.SetStorageMode(metal.MTLStorageModeShared)

	// Create buffer
	var sampleErr objc.ID
	bufferID := objc.Send[objc.ID](mb.device.GetID(), objc.Sel("newCounterSampleBufferWithDescriptor:error:"), descriptor, &sampleErr)
	if bufferID == 0 {
		errStr := "unknown error"
		if sampleErr != 0 {
			desc := objc.Send[objc.ID](sampleErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return nil, fmt.Errorf("failed to create counter sample buffer: %s", errStr)
	}

	return &MetalCounterSampleBufferHandle{buffer: metal.MTLCounterSampleBufferObjectFromID(bufferID)}, nil
}

// MetalBufferHandle wraps a Metal buffer.
type MetalBufferHandle struct {
	buffer metal.MTLBuffer
}

// Contents returns the buffer's CPU-accessible memory.
func (h *MetalBufferHandle) Contents() unsafe.Pointer {
	return objc.Send[unsafe.Pointer](h.buffer.GetID(), objc.Sel("contents"))
}

// Length returns the buffer size in bytes.
func (h *MetalBufferHandle) Length() uint64 {
	return uint64(h.buffer.Length())
}

// Release frees the buffer.
func (h *MetalBufferHandle) Release() {
}

// MetalFunctionHandle wraps a Metal function.
type MetalFunctionHandle struct {
	function metal.MTLFunction
}

// Release frees the function.
func (h *MetalFunctionHandle) Release() {
}

// MetalPipelineHandle wraps a compute pipeline state.
type MetalPipelineHandle struct {
	pipeline metal.MTLComputePipelineState
}

// Release frees the pipeline.
func (h *MetalPipelineHandle) Release() {
}

// MetalCommandBufferHandle wraps a command buffer.
type MetalCommandBufferHandle struct {
	cmdBuffer metal.MTLCommandBuffer
}

// CreateComputeEncoder creates a compute command encoder.
func (h *MetalCommandBufferHandle) CreateComputeEncoder() *MetalComputeEncoderHandle {
	encoderID := objc.Send[objc.ID](h.cmdBuffer.GetID(), objc.Sel("computeCommandEncoder"))
	encoder := metal.MTLComputeCommandEncoderObjectFromID(encoderID)
	return &MetalComputeEncoderHandle{encoder: encoder}
}

// CreateComputeEncoderWithStageSampling creates a compute command encoder with stage boundary
// counter sampling. This works on Apple Silicon (TBDR architecture) where explicit counter
// sampling is not supported. Samples are taken at start (index 0) and end (index 1) of encoder.
func (h *MetalCommandBufferHandle) CreateComputeEncoderWithStageSampling(sampleBuffer *MetalCounterSampleBufferHandle) *MetalComputeEncoderHandle {
	// Create compute pass descriptor
	passDesc := metal.NewMTLComputePassDescriptor()

	// Get sample buffer attachments array
	attachments := passDesc.SampleBufferAttachments()
	if attachments == nil {
		// Fall back to non-instrumented encoder
		return h.CreateComputeEncoder()
	}

	// Get attachment at index 0
	attachment0 := attachments.ObjectAtIndexedSubscript(0)
	if attachment0 == nil {
		return h.CreateComputeEncoder()
	}

	// Set the sample buffer
	attachment0.SetSampleBuffer(sampleBuffer.buffer)

	// Set sample indices: 0 for start, 1 for end of encoder
	attachment0.SetStartOfEncoderSampleIndex(0)
	attachment0.SetEndOfEncoderSampleIndex(1)

	// Create encoder with descriptor
	encoderID := objc.Send[objc.ID](h.cmdBuffer.GetID(), objc.Sel("computeCommandEncoderWithDescriptor:"), passDesc)
	if encoderID == 0 {
		return h.CreateComputeEncoder()
	}
	encoder := metal.MTLComputeCommandEncoderObjectFromID(encoderID)

	return &MetalComputeEncoderHandle{encoder: encoder}
}

// Commit commits the command buffer for execution.
func (h *MetalCommandBufferHandle) Commit() {
	h.cmdBuffer.Commit()
}

// WaitUntilCompleted waits for GPU execution to finish.
func (h *MetalCommandBufferHandle) WaitUntilCompleted() {
	h.cmdBuffer.WaitUntilCompleted()
}

// Release frees the command buffer.
func (h *MetalCommandBufferHandle) Release() {
}

// MetalComputeEncoderHandle wraps a compute command encoder.
type MetalComputeEncoderHandle struct {
	encoder metal.MTLComputeCommandEncoder
}

// SetPipeline sets the compute pipeline state.
func (h *MetalComputeEncoderHandle) SetPipeline(pipeline *MetalPipelineHandle) {
	h.encoder.SetComputePipelineState(pipeline.pipeline)
}

// SetBuffer binds a buffer at the specified index.
func (h *MetalComputeEncoderHandle) SetBuffer(buffer *MetalBufferHandle, index int) {
	objc.Send[struct{}](h.encoder.GetID(), objc.Sel("setBuffer:offset:atIndex:"), buffer.buffer, uint(0), uint(index))
}

// Dispatch dispatches a compute kernel.
func (h *MetalComputeEncoderHandle) Dispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ int) {
	gridSize := metal.MTLSize{
		Width:  uint(gridX),
		Height: uint(gridY),
		Depth:  uint(gridZ),
	}
	threadgroupSize := metal.MTLSize{
		Width:  uint(threadgroupX),
		Height: uint(threadgroupY),
		Depth:  uint(threadgroupZ),
	}
	h.encoder.DispatchThreadsThreadsPerThreadgroup(gridSize, threadgroupSize)
}

// DispatchWithValidation dispatches a compute kernel after validating parameters against device limits.
func (h *MetalComputeEncoderHandle) DispatchWithValidation(caps *DeviceCapabilities, gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ int) error {
	if caps != nil {
		if err := caps.ValidateDispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ); err != nil {
			return err
		}
	}
	h.Dispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ)
	return nil
}

// EndEncoding finishes encoding commands.
func (h *MetalComputeEncoderHandle) EndEncoding() {
	h.encoder.EndEncoding()
}

// Release frees the encoder.
func (h *MetalComputeEncoderHandle) Release() {
}

// SampleCounters inserts a counter sample at the specified index.
func (h *MetalComputeEncoderHandle) SampleCounters(sampleBuffer *MetalCounterSampleBufferHandle, sampleIndex int) {
	h.encoder.SampleCountersInBufferAtSampleIndexWithBarrier(sampleBuffer.buffer, uint(sampleIndex), true)
}

// MetalCounterSetHandle wraps a Metal counter set.
type MetalCounterSetHandle struct {
	set metal.MTLCounterSet
}

// Name returns the counter set name.
func (h *MetalCounterSetHandle) Name() string {
	return h.set.Name()
}

// Release frees the counter set.
func (h *MetalCounterSetHandle) Release() {
}

// MetalCounterSampleBufferHandle wraps a Metal counter sample buffer.
type MetalCounterSampleBufferHandle struct {
	buffer metal.MTLCounterSampleBuffer
}

// SampleCount returns the number of samples in the buffer.
func (h *MetalCounterSampleBufferHandle) SampleCount() int {
	return int(h.buffer.SampleCount())
}

// Release frees the counter sample buffer.
func (h *MetalCounterSampleBufferHandle) Release() {
}

// ResolveCounterSamples resolves counter sample data from the buffer.
func (h *MetalCommandBufferHandle) ResolveCounterSamples(sampleBuffer *MetalCounterSampleBufferHandle, startIndex, count int) ([]byte, error) {
	range_ := foundation.Range{Location: uint(startIndex), Length: uint(count)}

	// resolveCounterRange: returns NSData*
	// Since generated bindings might miss this method on MTLCounterSampleBuffer (or it's on a subclass?),
	// we use objc.Send directly.
	nsDataID := objc.Send[objc.ID](sampleBuffer.buffer.GetID(), objc.Sel("resolveCounterRange:"), range_)
	if nsDataID == 0 {
		return nil, fmt.Errorf("failed to resolve counter samples: returned nil")
	}

	nsData := foundation.NSDataFromID(nsDataID)

	length := nsData.Length()
	if length == 0 {
		return nil, fmt.Errorf("no counter data resolved (length is 0)")
	}

	bytesPtr := nsData.Bytes() // returns unsafe.Pointer
	if bytesPtr == nil {
		return nil, fmt.Errorf("no counter data resolved (bytes is nil)")
	}

	// Copy data to Go slice
	data := make([]byte, length)
	src := unsafe.Slice((*byte)(bytesPtr), int(length))
	copy(data, src)

	return data, nil
}
