package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/appledocs/generated/foundation"
	"github.com/tmc/appledocs/generated/metal"
	"github.com/tmc/appledocs/generated/objc"
	"github.com/tmc/appledocs/generated/objectivec"
)

// still need foundation for Range type

var countersCmd = &cobra.Command{
	Use:   "counters",
	Short: "Demonstrate live GPU counter sampling",
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
	devObj := objectivec.ObjectFrom(devPtr)
	device := metal.NewMTLDeviceObject(devObj)

	// Get Name safely
	fmt.Printf("Device: %s\n", device.Name())

	// 2. Discover CounterSets
	var timestampCounterSet metal.MTLCounterSet

	// Access counterSets property via generated binding if available, otherwise fallback or dynamic
	// MTLDevice has CounterSets() []objc.ID
	// But it returns IDs. We need to wrap them.
	// Check if CounterSets() returns objects or IDs.
	// generated/metal/device_protocol.gen.go: CounterSets() []objc.ID

	counterSetIDs := device.CounterSets()
	if counterSetIDs == nil {
		// Try manual send if binding returned nil unexpectedly, or just accept it
	}

	if len(counterSetIDs) > 0 {
		fmt.Printf("Found %d counter sets:\n", len(counterSetIDs))

		for _, csID := range counterSetIDs {
			obj := objectivec.ObjectFromID(csID.GetID())
			cs := metal.NewMTLCounterSetObject(obj)

			// Get name safely
			// Get name safely
			csName := cs.Name()

			fmt.Printf(" - %s\n", csName)

			if csName == "timestamp" {
				timestampCounterSet = cs
			}
		}
	} else {
		fmt.Println("Warning: device.counterSets returned empty")
	}

	if timestampCounterSet == nil {
		return fmt.Errorf("timestamp counter set not found")
	}

	// 3. Create Counter Sample Buffer
	desc := metal.NewMTLCounterSampleBufferDescriptor()
	desc.SetCounterSet(timestampCounterSet)
	desc.SetLabel("GPUTrace Timestamp Buffer")
	desc.SetSampleCount(2)
	desc.SetStorageMode(0) // MTLStorageModeShared

	// Create Sample Buffer
	// Use objc.Send directly to avoid purego interface return type panic
	var nsErr objc.ID
	sampleBufferID := objc.Send[objc.ID](device.GetID(), objc.Sel("newCounterSampleBufferWithDescriptor:error:"), desc.ID, &nsErr)
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
	sampleBuffer := metal.NewMTLCounterSampleBufferObject(objectivec.ObjectFromID(sampleBufferID))
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

	// Create library from source using objc.Send to avoid interface return type panic
	nsStringClass := objc.ID(uintptr(objc.GetClass("NSString")))
	sourceStr := objc.Send[objc.ID](nsStringClass, objc.Sel("stringWithUTF8String:"), source)
	opts := metal.NewMTLCompileOptions()
	var libErr objc.ID
	libraryID := objc.Send[objc.ID](device.GetID(), objc.Sel("newLibraryWithSource:options:error:"), sourceStr, opts.ID, &libErr)
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
	library := metal.NewMTLLibraryObject(objectivec.ObjectFromID(libraryID))

	// Create function using objc.Send
	fnName := objc.Send[objc.ID](nsStringClass, objc.Sel("stringWithUTF8String:"), "compute\x00")
	fnID := objc.Send[objc.ID](library.GetID(), objc.Sel("newFunctionWithName:"), fnName)
	if fnID == 0 {
		return fmt.Errorf("failed to create function 'compute'")
	}
	fn := metal.NewMTLFunctionObject(objectivec.ObjectFromID(fnID))

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
	pipelineState := metal.NewMTLComputePipelineStateObject(objectivec.ObjectFromID(pipelineID))

	// 3. Create Buffers
	dataSize := 1024
	// Use NewBufferWithLengthOptions
	buffer := device.NewBufferWithLengthOptions(uint(dataSize), metal.MTLResourceStorageModeShared)
	resolveBuf := device.NewBufferWithLengthOptions(1024, metal.MTLResourceStorageModeShared)

	// 4. Encode Commands using pass descriptor with sample buffer attachments
	// (explicit sampleCountersInBuffer not supported on M4 Max, must use stage boundaries)
	queue := device.NewCommandQueue()
	cmdBuffer := queue.CommandBuffer()

	// Create compute pass descriptor with counter sampling at stage boundaries
	passDesc := metal.NewMTLComputePassDescriptor()

	// Get sample buffer attachments array and configure attachment 0
	attachmentsPtr := passDesc.SampleBufferAttachments()
	// Get attachment at index 0 using objectAtIndexedSubscript:
	attachment0ID := objc.Send[objc.ID](objc.ID(attachmentsPtr), objc.Sel("objectAtIndexedSubscript:"), uint(0))
	attachment0 := metal.MTLComputePassSampleBufferAttachmentDescriptorFromID(attachment0ID)

	// Configure the attachment with our sample buffer
	objc.Send[objc.ID](attachment0.ID, objc.Sel("setSampleBuffer:"), sampleBuffer.GetID())
	attachment0.SetStartOfEncoderSampleIndex(0)
	attachment0.SetEndOfEncoderSampleIndex(1)

	// Create compute encoder with the pass descriptor
	cmdBufferObj := cmdBuffer.(*metal.MTLCommandBufferObject)
	encoderID := objc.Send[objc.ID](cmdBufferObj.GetID(), objc.Sel("computeCommandEncoderWithDescriptor:"), passDesc.ID)
	if encoderID == 0 {
		return fmt.Errorf("failed to create compute encoder with descriptor")
	}
	encoder := metal.NewMTLComputeCommandEncoderObject(objectivec.ObjectFromID(encoderID))

	encoder.SetComputePipelineState(pipelineState)
	// Use SetBufferWithOffsetAtIndex (bind buffer) instead of SetBufferOffsetAtIndex (update offset)
	encoder.SetBufferWithOffsetAtIndex(buffer, 0, 0)

	// Dispatch
	threadsPerGrid := metal.MTLSize{Width: int(dataSize), Height: 1, Depth: 1}
	threadsPerGroup := metal.MTLSize{Width: 32, Height: 1, Depth: 1}
	encoder.DispatchThreadsThreadsPerThreadgroup(threadsPerGrid, threadsPerGroup)

	// EndEncoding - counters automatically sampled at start and end of pass
	encoder.EndEncoding()

	cmdBuffer.Commit()
	cmdBuffer.WaitUntilCompleted()

	// 7. Resolve Counters
	resolveBuf = device.NewBufferWithLengthOptions(1024, 0)

	resolveCmdBuf := queue.CommandBuffer()

	// BlitCommandEncoder was added to MTLCommandBuffer
	blit := resolveCmdBuf.BlitCommandEncoder()

	rng := foundation.Range{Location: 0, Length: 2}

	// Resolve using objc.Send directly to avoid purego interface parameter panic
	blitObj := blit.(*metal.MTLBlitCommandEncoderObject)
	resolveBufObj := resolveBuf.(*metal.MTLBufferObject)
	objc.Send[objc.ID](blitObj.GetID(), objc.Sel("resolveCounters:inRange:destinationBuffer:destinationOffset:"),
		sampleBuffer.GetID(), rng, resolveBufObj.GetID(), uint(0))
	blit.EndEncoding()

	resolveCmdBuf.Commit()
	resolveCmdBuf.WaitUntilCompleted()

	// Read results
	// Contents() return type fixed to unsafe.Pointer
	ptr := resolveBuf.Contents()

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
