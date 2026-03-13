package cmd

import (
	"fmt"
	"unsafe"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// still need foundation for Range type

var countersCmd = &cobra.Command{
	Use:    "counters",
	Short:  "Demonstrate live GPU counter sampling",
	Hidden: true,
	Long: `Runs a minimal Metal compute workload with hardware performance counter sampling enabled.
This command verifies that gputrace can successfully configure, capture, and read back
Metal performance counters (timestamp, etc.) on the current device.`,
	RunE: runCounters,
}

func init() {
	rootCmd.AddCommand(countersCmd)
}

func runCounters(cmd *cobra.Command, args []string) error {
	// 1. Get Default Device
	devPtr := metal.MTLCreateSystemDefaultDevice()
	if devPtr == nil {
		return fmt.Errorf("failed to get default metal device")
	}
	device := metal.MTLDeviceObjectFromID(objc.IDFrom(devPtr))

	nameID := objc.Send[objc.ID](device.GetID(), objc.Sel("name"))
	if nameID != 0 {
		cstr := objc.Send[*byte](nameID, objc.Sel("UTF8String"))
		if cstr != nil {
			fmt.Printf("Device: %s\n", objc.GoString(cstr))
		}
	}

	// 2. Discover CounterSets
	var timestampCounterSet metal.MTLCounterSetObject
	counterSetsID := objc.Send[objc.ID](device.GetID(), objc.Sel("counterSets"))
	if counterSetsID == 0 {
		return fmt.Errorf("device returned no counter sets")
	}
	counterSetCount := objc.Send[uint](counterSetsID, objc.Sel("count"))
	if counterSetCount == 0 {
		return fmt.Errorf("device returned no counter sets")
	}
	fmt.Printf("Found %d counter sets:\n", counterSetCount)
	for i := uint(0); i < counterSetCount; i++ {
		setID := objc.Send[objc.ID](counterSetsID, objc.Sel("objectAtIndex:"), i)
		if setID == 0 {
			continue
		}
		cs := metal.MTLCounterSetObjectFromID(setID)
		csName := cs.Name()
		fmt.Printf(" - %s\n", csName)
		if csName == "timestamp" {
			timestampCounterSet = cs
		}
	}
	if timestampCounterSet.GetID() == 0 {
		return fmt.Errorf("timestamp counter set not found")
	}

	// 3. Create Counter Sample Buffer
	desc := metal.NewMTLCounterSampleBufferDescriptor()
	desc.SetCounterSet(timestampCounterSet)
	desc.SetLabel("GPUTrace Timestamp Buffer")
	desc.SetSampleCount(2)
	desc.SetStorageMode(0) // MTLStorageModeShared

	// Create sample buffer.
	var nsErr objc.ID
	sampleBufferID := objc.Send[objc.ID](device.GetID(), objc.Sel("newCounterSampleBufferWithDescriptor:error:"), desc, &nsErr)
	if sampleBufferID == 0 {
		if nsErr != 0 {
			errDesc := objc.Send[objc.ID](nsErr, objc.Sel("localizedDescription"))
			errStr := ""
			if errDesc != 0 {
				cstr := objc.Send[*byte](errDesc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
			return fmt.Errorf("failed to create counter sample buffer: %s", errStr)
		}
		return fmt.Errorf("failed to create counter sample buffer: unknown error")
	}
	sampleBuffer := metal.MTLCounterSampleBufferObjectFromID(sampleBufferID)
	fmt.Println("Created Sample Buffer")

	// 4. Create Library and Pipeline
	// 2. Load Kernel
	source := `
    #include <metal_stdlib>
    using namespace metal;
    kernel void compute(device float *buffer [[buffer(0)]], uint id [[thread_position_in_grid]]) {
        buffer[id] = float(id);
    }
    `

	// Create library from source using objc.Send.
	sourceStr := objc.String(source)
	opts := metal.NewMTLCompileOptions()
	var libErr objc.ID
	libraryID := objc.Send[objc.ID](device.GetID(), objc.Sel("newLibraryWithSource:options:error:"), sourceStr, opts, &libErr)
	if libraryID == 0 {
		errStr := "unknown error"
		if libErr != 0 {
			desc := objc.Send[objc.ID](libErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return fmt.Errorf("failed to create library: %s", errStr)
	}
	library := metal.MTLLibraryObjectFromID(libraryID)

	// Create function using objc.Send
	fnName := objc.String("compute")
	fnID := objc.Send[objc.ID](library.GetID(), objc.Sel("newFunctionWithName:"), fnName)
	if fnID == 0 {
		return fmt.Errorf("failed to create function 'compute'")
	}
	fn := metal.MTLFunctionObjectFromID(fnID)

	// Create pipeline state using objc.Send
	var pipeErr objc.ID
	pipelineID := objc.Send[objc.ID](device.GetID(), objc.Sel("newComputePipelineStateWithFunction:error:"), fn.GetID(), &pipeErr)
	if pipelineID == 0 {
		errStr := "unknown error"
		if pipeErr != 0 {
			desc := objc.Send[objc.ID](pipeErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return fmt.Errorf("failed to create pipeline state: %s", errStr)
	}
	pipelineState := metal.MTLComputePipelineStateObjectFromID(pipelineID)

	// 3. Create Buffers
	dataSize := 1024
	bufferID := objc.Send[objc.ID](device.GetID(), objc.Sel("newBufferWithLength:options:"), uint(dataSize), metal.MTLResourceStorageModeShared)
	if bufferID == 0 {
		return fmt.Errorf("failed to create compute buffer")
	}
	buffer := metal.MTLBufferObjectFromID(bufferID)
	resolveBufID := objc.Send[objc.ID](device.GetID(), objc.Sel("newBufferWithLength:options:"), uint(1024), metal.MTLResourceStorageModeShared)
	if resolveBufID == 0 {
		return fmt.Errorf("failed to create resolve buffer")
	}
	resolveBuf := metal.MTLBufferObjectFromID(resolveBufID)

	// 4. Encode Commands using pass descriptor with sample buffer attachments
	// (explicit sampleCountersInBuffer not supported on M4 Max, must use stage boundaries)
	queueID := objc.Send[objc.ID](device.GetID(), objc.Sel("newCommandQueue"))
	if queueID == 0 {
		return fmt.Errorf("failed to create command queue")
	}
	queue := metal.MTLCommandQueueObjectFromID(queueID)
	cmdBufferID := objc.Send[objc.ID](queue.GetID(), objc.Sel("commandBuffer"))
	if cmdBufferID == 0 {
		return fmt.Errorf("failed to create command buffer")
	}
	cmdBuffer := metal.MTLCommandBufferObjectFromID(cmdBufferID)

	// Create compute pass descriptor with counter sampling at stage boundaries
	passDesc := metal.NewMTLComputePassDescriptor()

	// Get sample buffer attachments array and configure attachment 0
	attachments := passDesc.SampleBufferAttachments()
	if attachments == nil {
		return fmt.Errorf("compute pass descriptor returned nil attachments")
	}
	attachment0 := attachments.ObjectAtIndexedSubscript(0)
	if attachment0 == nil {
		return fmt.Errorf("failed to get sample buffer attachment 0")
	}

	// Configure the attachment with our sample buffer
	attachment0.SetSampleBuffer(sampleBuffer)
	attachment0.SetStartOfEncoderSampleIndex(0)
	attachment0.SetEndOfEncoderSampleIndex(1)

	// Create compute encoder with the pass descriptor
	encoderID := objc.Send[objc.ID](cmdBuffer.GetID(), objc.Sel("computeCommandEncoderWithDescriptor:"), passDesc)
	if encoderID == 0 {
		return fmt.Errorf("failed to create compute encoder with descriptor")
	}
	encoder := metal.MTLComputeCommandEncoderObjectFromID(encoderID)

	encoder.SetComputePipelineState(pipelineState)
	objc.Send[struct{}](encoder.GetID(), objc.Sel("setBuffer:offset:atIndex:"), buffer, uint(0), uint(0))

	// Dispatch
	threadsPerGrid := metal.MTLSize{Width: uint(dataSize), Height: 1, Depth: 1}
	threadsPerGroup := metal.MTLSize{Width: 32, Height: 1, Depth: 1}
	encoder.DispatchThreadsThreadsPerThreadgroup(threadsPerGrid, threadsPerGroup)

	// EndEncoding - counters automatically sampled at start and end of pass
	encoder.EndEncoding()

	cmdBuffer.Commit()
	cmdBuffer.WaitUntilCompleted()

	// 7. Resolve Counters
	resolveBufID = objc.Send[objc.ID](device.GetID(), objc.Sel("newBufferWithLength:options:"), uint(1024), uint(0))
	if resolveBufID == 0 {
		return fmt.Errorf("failed to allocate resolve buffer")
	}
	resolveBuf = metal.MTLBufferObjectFromID(resolveBufID)

	resolveCmdBufID := objc.Send[objc.ID](queue.GetID(), objc.Sel("commandBuffer"))
	if resolveCmdBufID == 0 {
		return fmt.Errorf("failed to create resolve command buffer")
	}
	resolveCmdBuf := metal.MTLCommandBufferObjectFromID(resolveCmdBufID)

	blitID := objc.Send[objc.ID](resolveCmdBuf.GetID(), objc.Sel("blitCommandEncoder"))
	if blitID == 0 {
		return fmt.Errorf("failed to create blit encoder")
	}
	blit := metal.MTLBlitCommandEncoderObjectFromID(blitID)

	rng := foundation.Range{Location: 0, Length: 2}

	objc.Send[struct{}](blit.GetID(), objc.Sel("resolveCounters:inRange:destinationBuffer:destinationOffset:"),
		sampleBuffer, rng, resolveBuf, uint(0))
	blit.EndEncoding()

	resolveCmdBuf.Commit()
	resolveCmdBuf.WaitUntilCompleted()

	ptr := objc.Send[unsafe.Pointer](resolveBuf.GetID(), objc.Sel("contents"))
	if ptr == nil {
		return fmt.Errorf("resolve buffer contents is nil")
	}

	data := (*[2]uint64)(ptr)
	t0 := data[0]
	t1 := data[1]
	durationTicks := t1 - t0

	// Get timestamp frequency for conversion
	freq := objc.Send[uint64](device.GetID(), objc.Sel("queryTimestampFrequency"))

	fmt.Printf("Timestamp 0: %d\n", t0)
	fmt.Printf("Timestamp 1: %d\n", t1)
	fmt.Printf("Duration: %d ticks\n", durationTicks)

	if freq > 0 {
		durationNs := float64(durationTicks) * 1e9 / float64(freq)
		durationUs := durationNs / 1000
		fmt.Printf("Timestamp Frequency: %d Hz (%.1f MHz)\n", freq, float64(freq)/1e6)
		fmt.Printf("Duration: %.2f ns (%.2f µs)\n", durationNs, durationUs)
	}

	return nil
}
