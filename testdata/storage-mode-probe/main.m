// Probe: create MTLBuffers with distinct MTLResourceOptions and distinct
// lengths, so a capture reader can map Culul records (keyed by length)
// back to the options used at creation. One trivial dispatch triggers the
// capture stream.
#import <Metal/Metal.h>
#import <Foundation/Foundation.h>

static const char *kSource =
    "#include <metal_stdlib>\n"
    "using namespace metal;\n"
    "kernel void probe_touch(device float *buf [[buffer(0)]],\n"
    "                        uint gid [[thread_position_in_grid]]) {\n"
    "    buf[gid] = buf[gid] + 1.0f;\n"
    "}\n";

int main(void) {
    @autoreleasepool {
        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        if (!device) { fprintf(stderr, "no device\n"); return 1; }

        struct { NSUInteger len; MTLResourceOptions opts; const char *name; } specs[] = {
            {4096,  0,                                                                  "shared_default"},
            {8192,  MTLResourceStorageModePrivate,                                      "private"},
            {12288, MTLResourceStorageModeShared | MTLResourceHazardTrackingModeUntracked, "shared_untracked"},
            {16384, MTLResourceStorageModePrivate | MTLResourceHazardTrackingModeUntracked, "private_untracked"},
            {20480, MTLResourceStorageModeShared | MTLResourceHazardTrackingModeTracked,   "shared_tracked"},
        };
        id<MTLBuffer> bufs[5];
        for (int i = 0; i < 5; i++) {
            bufs[i] = [device newBufferWithLength:specs[i].len options:specs[i].opts];
            printf("buffer len=%lu options=0x%lx (%s)\n",
                   (unsigned long)specs[i].len, (unsigned long)specs[i].opts, specs[i].name);
        }

        MTLHeapDescriptor *hd = [MTLHeapDescriptor new];
        hd.size = 1 << 20;
        hd.storageMode = MTLStorageModePrivate;
        id<MTLHeap> heap = [device newHeapWithDescriptor:hd];
        id<MTLBuffer> heapBuf = [heap newBufferWithLength:24576 options:MTLResourceStorageModePrivate];
        printf("heap buffer len=24576 heap=%p buf=%p\n", (__bridge void *)heap, (__bridge void *)heapBuf);

        NSError *err = nil;
        id<MTLLibrary> lib = [device newLibraryWithSource:@(kSource) options:nil error:&err];
        id<MTLComputePipelineState> pso =
            [device newComputePipelineStateWithFunction:[lib newFunctionWithName:@"probe_touch"] error:&err];
        if (!pso) { fprintf(stderr, "pipeline: %s\n", err.description.UTF8String); return 1; }

        id<MTLCommandQueue> queue = [device newCommandQueue];
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:bufs[0] offset:0 atIndex:0];
        [enc dispatchThreads:MTLSizeMake(1024, 1, 1) threadsPerThreadgroup:MTLSizeMake(64, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
        printf("done\n");
    }
    return 0;
}
