package gputrace

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Metal -framework Foundation

#import <Metal/Metal.h>
#import <Foundation/Foundation.h>

// MetalDevice wraps MTLDevice
typedef struct {
    void* device;
} MetalDevice;

// MetalBuffer wraps MTLBuffer
typedef struct {
    void* buffer;
    unsigned long long length;
} MetalBuffer;

// MetalFunction wraps MTLFunction
typedef struct {
    void* function;
    char* name;
} MetalFunction;

// MetalPipelineState wraps MTLComputePipelineState
typedef struct {
    void* pipelineState;
} MetalPipelineState;

// MetalCommandQueue wraps MTLCommandQueue
typedef struct {
    void* commandQueue;
} MetalCommandQueue;

// MetalCommandBuffer wraps MTLCommandBuffer
typedef struct {
    void* commandBuffer;
} MetalCommandBuffer;

// MetalComputeEncoder wraps MTLComputeCommandEncoder
typedef struct {
    void* encoder;
} MetalComputeEncoder;

// MetalCounterSampleBuffer wraps MTLCounterSampleBuffer
typedef struct {
    void* counterBuffer;
    int sampleCount;
} MetalCounterSampleBuffer;

// MetalCounterSet wraps MTLCounterSet
typedef struct {
    void* counterSet;
    char* name;
} MetalCounterSet;

// Initialize Metal - get default device
MetalDevice* metal_init() {
    @autoreleasepool {
        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        if (!device) {
            return NULL;
        }

        MetalDevice* wrapper = (MetalDevice*)malloc(sizeof(MetalDevice));
        wrapper->device = (void*)CFBridgingRetain(device);
        return wrapper;
    }
}

// Create command queue
MetalCommandQueue* metal_create_command_queue(MetalDevice* device) {
    @autoreleasepool {
        id<MTLDevice> mtlDevice = (__bridge id<MTLDevice>)device->device;
        id<MTLCommandQueue> queue = [mtlDevice newCommandQueue];

        MetalCommandQueue* wrapper = (MetalCommandQueue*)malloc(sizeof(MetalCommandQueue));
        wrapper->commandQueue = (void*)CFBridgingRetain(queue);
        return wrapper;
    }
}

// Create buffer with data
MetalBuffer* metal_create_buffer(MetalDevice* device, void* data, unsigned long long length) {
    @autoreleasepool {
        id<MTLDevice> mtlDevice = (__bridge id<MTLDevice>)device->device;
        id<MTLBuffer> buffer = [mtlDevice newBufferWithBytes:data
                                                      length:length
                                                     options:MTLResourceStorageModeShared];

        MetalBuffer* wrapper = (MetalBuffer*)malloc(sizeof(MetalBuffer));
        wrapper->buffer = (void*)CFBridgingRetain(buffer);
        wrapper->length = length;
        return wrapper;
    }
}

// Create function from library
MetalFunction* metal_create_function(MetalDevice* device, const char* source, const char* name) {
    @autoreleasepool {
        id<MTLDevice> mtlDevice = (__bridge id<MTLDevice>)device->device;

        NSError* error = nil;
        NSString* sourceStr = [NSString stringWithUTF8String:source];
        id<MTLLibrary> library = [mtlDevice newLibraryWithSource:sourceStr
                                                         options:nil
                                                           error:&error];
        if (!library) {
            return NULL;
        }

        NSString* funcName = [NSString stringWithUTF8String:name];
        id<MTLFunction> function = [library newFunctionWithName:funcName];
        if (!function) {
            return NULL;
        }

        MetalFunction* wrapper = (MetalFunction*)malloc(sizeof(MetalFunction));
        wrapper->function = (void*)CFBridgingRetain(function);
        wrapper->name = strdup(name);
        return wrapper;
    }
}

// Create compute pipeline state
MetalPipelineState* metal_create_pipeline(MetalDevice* device, MetalFunction* function) {
    @autoreleasepool {
        id<MTLDevice> mtlDevice = (__bridge id<MTLDevice>)device->device;
        id<MTLFunction> mtlFunction = (__bridge id<MTLFunction>)function->function;

        NSError* error = nil;
        id<MTLComputePipelineState> pipeline = [mtlDevice newComputePipelineStateWithFunction:mtlFunction
                                                                                         error:&error];
        if (!pipeline) {
            return NULL;
        }

        MetalPipelineState* wrapper = (MetalPipelineState*)malloc(sizeof(MetalPipelineState));
        wrapper->pipelineState = (void*)CFBridgingRetain(pipeline);
        return wrapper;
    }
}

// Create command buffer
MetalCommandBuffer* metal_create_command_buffer(MetalCommandQueue* queue) {
    @autoreleasepool {
        id<MTLCommandQueue> mtlQueue = (__bridge id<MTLCommandQueue>)queue->commandQueue;
        id<MTLCommandBuffer> cmdBuffer = [mtlQueue commandBuffer];

        MetalCommandBuffer* wrapper = (MetalCommandBuffer*)malloc(sizeof(MetalCommandBuffer));
        wrapper->commandBuffer = (void*)CFBridgingRetain(cmdBuffer);
        return wrapper;
    }
}

// Create compute command encoder
MetalComputeEncoder* metal_create_compute_encoder(MetalCommandBuffer* cmdBuffer) {
    @autoreleasepool {
        id<MTLCommandBuffer> mtlCmdBuffer = (__bridge id<MTLCommandBuffer>)cmdBuffer->commandBuffer;
        id<MTLComputeCommandEncoder> encoder = [mtlCmdBuffer computeCommandEncoder];

        MetalComputeEncoder* wrapper = (MetalComputeEncoder*)malloc(sizeof(MetalComputeEncoder));
        wrapper->encoder = (void*)CFBridgingRetain(encoder);
        return wrapper;
    }
}

// Set compute pipeline state
void metal_set_pipeline(MetalComputeEncoder* encoder, MetalPipelineState* pipeline) {
    @autoreleasepool {
        id<MTLComputeCommandEncoder> mtlEncoder = (__bridge id<MTLComputeCommandEncoder>)encoder->encoder;
        id<MTLComputePipelineState> mtlPipeline = (__bridge id<MTLComputePipelineState>)pipeline->pipelineState;
        [mtlEncoder setComputePipelineState:mtlPipeline];
    }
}

// Set buffer
void metal_set_buffer(MetalComputeEncoder* encoder, MetalBuffer* buffer, int index) {
    @autoreleasepool {
        id<MTLComputeCommandEncoder> mtlEncoder = (__bridge id<MTLComputeCommandEncoder>)encoder->encoder;
        id<MTLBuffer> mtlBuffer = (__bridge id<MTLBuffer>)buffer->buffer;
        [mtlEncoder setBuffer:mtlBuffer offset:0 atIndex:index];
    }
}

// Dispatch threadgroups
void metal_dispatch(MetalComputeEncoder* encoder,
                   int gridX, int gridY, int gridZ,
                   int threadgroupX, int threadgroupY, int threadgroupZ) {
    @autoreleasepool {
        id<MTLComputeCommandEncoder> mtlEncoder = (__bridge id<MTLComputeCommandEncoder>)encoder->encoder;

        MTLSize gridSize = MTLSizeMake(gridX, gridY, gridZ);
        MTLSize threadgroupSize = MTLSizeMake(threadgroupX, threadgroupY, threadgroupZ);

        [mtlEncoder dispatchThreads:gridSize threadsPerThreadgroup:threadgroupSize];
    }
}

// End encoding
void metal_end_encoding(MetalComputeEncoder* encoder) {
    @autoreleasepool {
        id<MTLComputeCommandEncoder> mtlEncoder = (__bridge id<MTLComputeCommandEncoder>)encoder->encoder;
        [mtlEncoder endEncoding];
    }
}

// Commit command buffer
void metal_commit(MetalCommandBuffer* cmdBuffer) {
    @autoreleasepool {
        id<MTLCommandBuffer> mtlCmdBuffer = (__bridge id<MTLCommandBuffer>)cmdBuffer->commandBuffer;
        [mtlCmdBuffer commit];
    }
}

// Wait until completed
void metal_wait_until_completed(MetalCommandBuffer* cmdBuffer) {
    @autoreleasepool {
        id<MTLCommandBuffer> mtlCmdBuffer = (__bridge id<MTLCommandBuffer>)cmdBuffer->commandBuffer;
        [mtlCmdBuffer waitUntilCompleted];
    }
}

// Get buffer contents
void* metal_get_buffer_contents(MetalBuffer* buffer) {
    @autoreleasepool {
        id<MTLBuffer> mtlBuffer = (__bridge id<MTLBuffer>)buffer->buffer;
        return [mtlBuffer contents];
    }
}

// Cleanup functions
void metal_release_device(MetalDevice* device) {
    if (device) {
        CFBridgingRelease(device->device);
        free(device);
    }
}

void metal_release_buffer(MetalBuffer* buffer) {
    if (buffer) {
        CFBridgingRelease(buffer->buffer);
        free(buffer);
    }
}

void metal_release_function(MetalFunction* function) {
    if (function) {
        CFBridgingRelease(function->function);
        free(function->name);
        free(function);
    }
}

void metal_release_pipeline(MetalPipelineState* pipeline) {
    if (pipeline) {
        CFBridgingRelease(pipeline->pipelineState);
        free(pipeline);
    }
}

void metal_release_command_queue(MetalCommandQueue* queue) {
    if (queue) {
        CFBridgingRelease(queue->commandQueue);
        free(queue);
    }
}

void metal_release_command_buffer(MetalCommandBuffer* cmdBuffer) {
    if (cmdBuffer) {
        CFBridgingRelease(cmdBuffer->commandBuffer);
        free(cmdBuffer);
    }
}

void metal_release_encoder(MetalComputeEncoder* encoder) {
    if (encoder) {
        CFBridgingRelease(encoder->encoder);
        free(encoder);
    }
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// MetalBridge provides Go bindings to Metal APIs for GPU replay.
type MetalBridge struct {
	device       *C.MetalDevice
	commandQueue *C.MetalCommandQueue
}

// NewMetalBridge initializes a Metal bridge with the default GPU device.
func NewMetalBridge() (*MetalBridge, error) {
	device := C.metal_init()
	if device == nil {
		return nil, fmt.Errorf("failed to initialize Metal device")
	}

	queue := C.metal_create_command_queue(device)
	if queue == nil {
		C.metal_release_device(device)
		return nil, fmt.Errorf("failed to create command queue")
	}

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

	buffer := C.metal_create_buffer(mb.device, unsafe.Pointer(&data[0]), C.ulonglong(len(data)))
	if buffer == nil {
		return nil, fmt.Errorf("failed to create buffer")
	}

	return &MetalBufferHandle{buffer: buffer}, nil
}

// CreateFunction creates a Metal function from source code.
func (mb *MetalBridge) CreateFunction(source, name string) (*MetalFunctionHandle, error) {
	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	function := C.metal_create_function(mb.device, cSource, cName)
	if function == nil {
		return nil, fmt.Errorf("failed to create function: %s", name)
	}

	return &MetalFunctionHandle{function: function}, nil
}

// CreatePipeline creates a compute pipeline state from a function.
func (mb *MetalBridge) CreatePipeline(function *MetalFunctionHandle) (*MetalPipelineHandle, error) {
	pipeline := C.metal_create_pipeline(mb.device, function.function)
	if pipeline == nil {
		return nil, fmt.Errorf("failed to create pipeline")
	}

	return &MetalPipelineHandle{pipeline: pipeline}, nil
}

// CreateCommandBuffer creates a new command buffer.
func (mb *MetalBridge) CreateCommandBuffer() *MetalCommandBufferHandle {
	cmdBuffer := C.metal_create_command_buffer(mb.commandQueue)
	return &MetalCommandBufferHandle{cmdBuffer: cmdBuffer}
}

// Close releases Metal resources.
func (mb *MetalBridge) Close() {
	if mb.commandQueue != nil {
		C.metal_release_command_queue(mb.commandQueue)
		mb.commandQueue = nil
	}
	if mb.device != nil {
		C.metal_release_device(mb.device)
		mb.device = nil
	}
}

// MetalBufferHandle wraps a Metal buffer.
type MetalBufferHandle struct {
	buffer *C.MetalBuffer
}

// Contents returns the buffer's CPU-accessible memory.
func (h *MetalBufferHandle) Contents() unsafe.Pointer {
	return C.metal_get_buffer_contents(h.buffer)
}

// Length returns the buffer size in bytes.
func (h *MetalBufferHandle) Length() uint64 {
	return uint64(h.buffer.length)
}

// Release frees the buffer.
func (h *MetalBufferHandle) Release() {
	if h.buffer != nil {
		C.metal_release_buffer(h.buffer)
		h.buffer = nil
	}
}

// MetalFunctionHandle wraps a Metal function.
type MetalFunctionHandle struct {
	function *C.MetalFunction
}

// Release frees the function.
func (h *MetalFunctionHandle) Release() {
	if h.function != nil {
		C.metal_release_function(h.function)
		h.function = nil
	}
}

// MetalPipelineHandle wraps a compute pipeline state.
type MetalPipelineHandle struct {
	pipeline *C.MetalPipelineState
}

// Release frees the pipeline.
func (h *MetalPipelineHandle) Release() {
	if h.pipeline != nil {
		C.metal_release_pipeline(h.pipeline)
		h.pipeline = nil
	}
}

// MetalCommandBufferHandle wraps a command buffer.
type MetalCommandBufferHandle struct {
	cmdBuffer *C.MetalCommandBuffer
}

// CreateComputeEncoder creates a compute command encoder.
func (h *MetalCommandBufferHandle) CreateComputeEncoder() *MetalComputeEncoderHandle {
	encoder := C.metal_create_compute_encoder(h.cmdBuffer)
	return &MetalComputeEncoderHandle{encoder: encoder}
}

// Commit commits the command buffer for execution.
func (h *MetalCommandBufferHandle) Commit() {
	C.metal_commit(h.cmdBuffer)
}

// WaitUntilCompleted waits for GPU execution to finish.
func (h *MetalCommandBufferHandle) WaitUntilCompleted() {
	C.metal_wait_until_completed(h.cmdBuffer)
}

// Release frees the command buffer.
func (h *MetalCommandBufferHandle) Release() {
	if h.cmdBuffer != nil {
		C.metal_release_command_buffer(h.cmdBuffer)
		h.cmdBuffer = nil
	}
}

// MetalComputeEncoderHandle wraps a compute command encoder.
type MetalComputeEncoderHandle struct {
	encoder *C.MetalComputeEncoder
}

// SetPipeline sets the compute pipeline state.
func (h *MetalComputeEncoderHandle) SetPipeline(pipeline *MetalPipelineHandle) {
	C.metal_set_pipeline(h.encoder, pipeline.pipeline)
}

// SetBuffer binds a buffer at the specified index.
func (h *MetalComputeEncoderHandle) SetBuffer(buffer *MetalBufferHandle, index int) {
	C.metal_set_buffer(h.encoder, buffer.buffer, C.int(index))
}

// Dispatch dispatches a compute kernel.
func (h *MetalComputeEncoderHandle) Dispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ int) {
	C.metal_dispatch(h.encoder,
		C.int(gridX), C.int(gridY), C.int(gridZ),
		C.int(threadgroupX), C.int(threadgroupY), C.int(threadgroupZ))
}

// EndEncoding finishes encoding commands.
func (h *MetalComputeEncoderHandle) EndEncoding() {
	C.metal_end_encoding(h.encoder)
}

// Release frees the encoder.
func (h *MetalComputeEncoderHandle) Release() {
	if h.encoder != nil {
		C.metal_release_encoder(h.encoder)
		h.encoder = nil
	}
}
