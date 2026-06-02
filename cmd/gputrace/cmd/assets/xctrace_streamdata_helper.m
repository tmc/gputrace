#import <Foundation/Foundation.h>
#import <dlfcn.h>
#import <objc/message.h>
#import <objc/runtime.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/stat.h>

typedef struct {
    uint64_t sequenceID;
    uint64_t startTimestamp;
    uint64_t endOffsetMicros;
    uint32_t labelStringIndex;
    uint32_t commandBufferIndex;
    uint32_t flags;
    uint32_t reserved;
} GTEncoderInfoRow;

typedef struct {
    uint32_t functionIndex;
    uint32_t subCommandIndex;
    uint32_t reserved0;
    uint32_t pipelineIndex;
    uint64_t endOffsetMicros;
    uint32_t encoderIndex;
    int32_t reserved1;
} GTGPUCommandInfoRow;

typedef struct {
    uint64_t sequenceID;
    uint64_t startTimestamp;
    uint64_t endOffsetMicros;
    uint32_t flags;
    uint32_t encoderCount;
} GTCommandBufferInfoRow;

static NSString *stringFromObject(id object) {
    if (!object || object == [NSNull null]) {
        return @"";
    }
    return [NSString stringWithFormat:@"%@", object];
}

static uint64_t unsignedValue(id object) {
    if ([object respondsToSelector:@selector(unsignedLongLongValue)]) {
        return [object unsignedLongLongValue];
    }
    return 0;
}

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc != 3) {
            fprintf(stderr, "usage: %s rows.json out.gpuprofiler_raw\n", argv[0]);
            return 2;
        }
        NSString *jsonPath = [NSString stringWithUTF8String:argv[1]];
        NSString *outDir = [NSString stringWithUTF8String:argv[2]];
        NSData *jsonData = [NSData dataWithContentsOfFile:jsonPath];
        if (!jsonData) {
            fprintf(stderr, "failed to read rows json\n");
            return 3;
        }
        NSError *jsonError = nil;
        NSArray *rows = [NSJSONSerialization JSONObjectWithData:jsonData options:0 error:&jsonError];
        if (![rows isKindOfClass:[NSArray class]] || [rows count] == 0) {
            fprintf(stderr, "invalid rows json: %s\n", [[jsonError description] UTF8String]);
            return 4;
        }
        void *handle = dlopen("/System/Library/PrivateFrameworks/GPUToolsReplay.framework/GPUToolsReplay", RTLD_NOW);
        fprintf(stdout, "GPUToolsReplay dlopen=%p err=%s\n", handle, dlerror());
        Class streamDataClass = NSClassFromString(@"GTMutableShaderProfilerStreamData");
        if (!streamDataClass) {
            fprintf(stderr, "GTMutableShaderProfilerStreamData missing\n");
            return 5;
        }
        id streamData = nil;
        if ([streamDataClass instancesRespondToSelector:@selector(initWithNewFileFormatV2Support:)]) {
            streamData = ((id (*)(id, SEL, BOOL))objc_msgSend)([streamDataClass alloc], @selector(initWithNewFileFormatV2Support:), YES);
        } else {
            streamData = [streamDataClass new];
        }
        if ([streamData respondsToSelector:@selector(setTraceName:)]) {
            [streamData setValue:@"xctrace-metal-gpu-intervals" forKey:@"traceName"];
        }
        NSUInteger count = [rows count];
        GTEncoderInfoRow *encoders = calloc(count, sizeof(GTEncoderInfoRow));
        GTGPUCommandInfoRow *commands = calloc(count, sizeof(GTGPUCommandInfoRow));
        if (!encoders || !commands) {
            return 6;
        }
        uint64_t firstStart = unsignedValue(rows[0][@"start_ns"]);
        uint64_t cumulativeUs = 0;
        for (NSUInteger i = 0; i < count; i++) {
            NSDictionary *row = rows[i];
            uint64_t startNs = unsignedValue(row[@"start_ns"]);
            uint64_t durationNs = unsignedValue(row[@"duration_ns"]);
            uint64_t durationUs = durationNs / 1000;
            if (durationUs == 0 && durationNs > 0) {
                durationUs = 1;
            }
            cumulativeUs += durationUs;
            encoders[i].sequenceID = (uint64_t)i + 1;
            encoders[i].startTimestamp = startNs;
            encoders[i].endOffsetMicros = cumulativeUs;
            encoders[i].labelStringIndex = 0;
            encoders[i].commandBufferIndex = 0;
            commands[i].functionIndex = 0;
            commands[i].subCommandIndex = (uint32_t)i;
            commands[i].pipelineIndex = 0;
            commands[i].endOffsetMicros = cumulativeUs;
            commands[i].encoderIndex = (uint32_t)i;
        }
        GTCommandBufferInfoRow commandBuffer = {1, firstStart, cumulativeUs, 0, (uint32_t)count};
        if ([streamData respondsToSelector:@selector(addCommandBuffers:count:)]) {
            ((void (*)(id, SEL, GTCommandBufferInfoRow *, uint64_t))objc_msgSend)(streamData, @selector(addCommandBuffers:count:), &commandBuffer, 1);
        }
        if ([streamData respondsToSelector:@selector(addEncoders:count:)]) {
            ((void (*)(id, SEL, GTEncoderInfoRow *, uint64_t))objc_msgSend)(streamData, @selector(addEncoders:count:), encoders, (uint64_t)count);
        }
        if ([streamData respondsToSelector:@selector(addGPUCommands:count:)]) {
            ((void (*)(id, SEL, GTGPUCommandInfoRow *, uint64_t))objc_msgSend)(streamData, @selector(addGPUCommands:count:), commands, (uint64_t)count);
        }
        mkdir([outDir fileSystemRepresentation], 0777);
        NSError *encodeError = nil;
        id encoded = ((id (*)(id, SEL, id, NSError **))objc_msgSend)(streamData, @selector(encode:error:), [NSURL fileURLWithPath:outDir isDirectory:YES], &encodeError);
        fprintf(stdout, "encoded=%s err=%s rows=%lu cumulative_us=%llu streamData_exists=%d\n",
                [stringFromObject(encoded) UTF8String],
                [[encodeError description] UTF8String],
                (unsigned long)count,
                (unsigned long long)cumulativeUs,
                [[NSFileManager defaultManager] fileExistsAtPath:[outDir stringByAppendingPathComponent:@"streamData"]] ? 1 : 0);
        free(encoders);
        free(commands);
        return encodeError ? 7 : 0;
    }
}
