// +build metal

package gputrace

import (
	"fmt"
	"unsafe"
)

// MetalReplayEngine extends ReplayEngine with actual Metal execution capabilities.
type MetalReplayEngine struct {
	*ReplayEngine
	Bridge         *MetalBridge
	MetalBuffers   map[uint64]*MetalBufferHandle   // trace address -> Metal buffer
	MetalFunctions map[uint64]*MetalFunctionHandle // trace address -> Metal function
	MetalPipelines map[uint64]*MetalPipelineHandle // trace address -> Metal pipeline
}

// NewMetalReplayEngine creates a replay engine with Metal execution support.
func NewMetalReplayEngine(trace *Trace) (*MetalReplayEngine, error) {
	bridge, err := NewMetalBridge()
	if err != nil {
		return nil, fmt.Errorf("initialize Metal bridge: %w", err)
	}

	return &MetalReplayEngine{
		ReplayEngine:   NewReplayEngine(trace),
		Bridge:         bridge,
		MetalBuffers:   make(map[uint64]*MetalBufferHandle),
		MetalFunctions: make(map[uint64]*MetalFunctionHandle),
		MetalPipelines: make(map[uint64]*MetalPipelineHandle),
	}, nil
}

// Close releases Metal resources.
func (mre *MetalReplayEngine) Close() error {
	// Release all Metal objects
	for _, buffer := range mre.MetalBuffers {
		buffer.Release()
	}
	for _, function := range mre.MetalFunctions {
		function.Release()
	}
	for _, pipeline := range mre.MetalPipelines {
		pipeline.Release()
	}

	// Release bridge
	if mre.Bridge != nil {
		mre.Bridge.Close()
	}

	return nil
}

// RestoreBuffersToMetal restores trace buffers to actual Metal buffers.
func (mre *MetalReplayEngine) RestoreBuffersToMetal() error {
	// Discover buffers from trace
	buffers, err := mre.State.DiscoverBuffers()
	if err != nil {
		return fmt.Errorf("discover buffers: %w", err)
	}

	// Correlate buffer addresses
	buffers, err = mre.State.CorrelateBufferAddresses(buffers)
	if err != nil {
		return fmt.Errorf("correlate buffer addresses: %w", err)
	}

	// Create Metal buffers from trace data
	for _, bufInfo := range buffers {
		if len(bufInfo.Contents) == 0 {
			continue
		}

		// Create Metal buffer with data from trace
		metalBuffer, err := mre.Bridge.CreateBuffer(bufInfo.Contents)
		if err != nil {
			return fmt.Errorf("create buffer %s: %w", bufInfo.Name, err)
		}

		// Store in map keyed by trace address
		if bufInfo.Address != 0 {
			mre.MetalBuffers[bufInfo.Address] = metalBuffer
			mre.State.Buffers[bufInfo.Address] = metalBuffer
		}
	}

	return nil
}

// RestoreFunctionsToMetal compiles shader source and creates Metal functions/pipelines.
func (mre *MetalReplayEngine) RestoreFunctionsToMetal() error {
	// Discover functions from device resources
	functions, err := mre.State.DiscoverFunctions()
	if err != nil {
		return fmt.Errorf("discover functions: %w", err)
	}

	// For each function, we need shader source code
	// The trace contains compiled .metallib files, but we need source for compilation
	// For now, we'll look for shader source in trace metadata
	shaderSources := mre.extractShaderSources()

	for _, funcInfo := range functions {
		// Try to find shader source for this function
		source, ok := shaderSources[funcInfo.Name]
		if !ok {
			// If no source available, skip this function
			// In a complete implementation, we would extract from .metallib
			continue
		}

		// Compile Metal function from source
		metalFunction, err := mre.Bridge.CreateFunction(source, funcInfo.Name)
		if err != nil {
			// Non-fatal: log and continue
			fmt.Printf("Warning: failed to compile function %s: %v\n", funcInfo.Name, err)
			continue
		}

		// Create pipeline state
		metalPipeline, err := mre.Bridge.CreatePipeline(metalFunction)
		if err != nil {
			metalFunction.Release()
			return fmt.Errorf("create pipeline for %s: %w", funcInfo.Name, err)
		}

		// Store function and pipeline
		mre.MetalFunctions[funcInfo.Address] = metalFunction
		mre.MetalPipelines[funcInfo.Address] = metalPipeline
		mre.State.Functions[funcInfo.Address] = metalFunction
		mre.State.PipelineStates[funcInfo.Address] = metalPipeline
	}

	return nil
}

// extractShaderSources extracts shader source code from trace metadata.
// Returns a map of function name -> shader source.
func (mre *MetalReplayEngine) extractShaderSources() map[string]string {
	sources := make(map[string]string)

	// TODO: Implement shader source extraction from trace
	// Options:
	// 1. Parse -shaders.txt file if available
	// 2. Extract from .metallib files in trace
	// 3. Use precompiled pipeline state objects
	//
	// For now, return empty map - callers will skip functions without source

	return sources
}

// ExecuteReplayPlan executes a replay plan on actual Metal GPU.
func (mre *MetalReplayEngine) ExecuteReplayPlan(plan *ReplayPlan) (*MetalReplayResult, error) {
	result := &MetalReplayResult{
		TraceePath:    plan.TraceePath,
		Success:       true,
		EncodersRun:   0,
		DispatchesRun: 0,
	}

	// Restore buffers and functions first
	if err := mre.RestoreBuffersToMetal(); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("restore buffers: %v", err)
		return result, err
	}

	if err := mre.RestoreFunctionsToMetal(); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("restore functions: %v", err)
		return result, err
	}

	// Group commands by encoder
	encoderCommands := make(map[int][]ReplayCommand)
	for _, cmd := range plan.Commands {
		encoderCommands[cmd.EncoderIndex] = append(encoderCommands[cmd.EncoderIndex], cmd)
	}

	// Execute each encoder's commands
	for encoderIdx := 0; encoderIdx < len(plan.Encoders); encoderIdx++ {
		commands := encoderCommands[encoderIdx]
		if len(commands) == 0 {
			continue
		}

		// Create command buffer for this encoder
		cmdBuffer := mre.Bridge.CreateCommandBuffer()
		encoder := cmdBuffer.CreateComputeEncoder()

		// Encode all commands for this encoder
		for _, cmd := range commands {
			if err := mre.encodeCommand(encoder, cmd); err != nil {
				encoder.Release()
				cmdBuffer.Release()
				result.Success = false
				result.Error = fmt.Sprintf("encode command: %v", err)
				return result, err
			}

			if cmd.Type == "compute_dispatch" {
				result.DispatchesRun++
			}
		}

		// End encoding and execute
		encoder.EndEncoding()
		cmdBuffer.Commit()
		cmdBuffer.WaitUntilCompleted()

		// Cleanup
		encoder.Release()
		cmdBuffer.Release()

		result.EncodersRun++
	}

	return result, nil
}

// encodeCommand encodes a single replay command into a Metal compute encoder.
func (mre *MetalReplayEngine) encodeCommand(encoder *MetalComputeEncoderHandle, cmd ReplayCommand) error {
	switch cmd.Type {
	case "compute_dispatch":
		// Set pipeline state
		pipeline, ok := mre.MetalPipelines[cmd.FunctionAddr]
		if !ok {
			return fmt.Errorf("pipeline not found for function 0x%x", cmd.FunctionAddr)
		}
		encoder.SetPipeline(pipeline)

		// Bind buffers
		for i, bufAddr := range cmd.BufferBindings {
			if bufAddr == 0 {
				continue
			}
			buffer, ok := mre.MetalBuffers[bufAddr]
			if !ok {
				// Non-fatal: skip missing buffers
				continue
			}
			encoder.SetBuffer(buffer, i)
		}

		// Dispatch compute kernel
		gridX := int(cmd.ThreadsPerGrid[0])
		gridY := int(cmd.ThreadsPerGrid[1])
		gridZ := int(cmd.ThreadsPerGrid[2])
		threadgroupX := int(cmd.ThreadsPerThreadgroup[0])
		threadgroupY := int(cmd.ThreadsPerThreadgroup[1])
		threadgroupZ := int(cmd.ThreadsPerThreadgroup[2])

		// Ensure valid dimensions (default to 1 if zero)
		if gridX == 0 {
			gridX = 1
		}
		if gridY == 0 {
			gridY = 1
		}
		if gridZ == 0 {
			gridZ = 1
		}
		if threadgroupX == 0 {
			threadgroupX = 1
		}
		if threadgroupY == 0 {
			threadgroupY = 1
		}
		if threadgroupZ == 0 {
			threadgroupZ = 1
		}

		encoder.Dispatch(gridX, gridY, gridZ, threadgroupX, threadgroupY, threadgroupZ)

	case "execute_icb":
		// Indirect command buffer execution not yet supported
		return fmt.Errorf("ICB execution not yet implemented")

	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}

	return nil
}

// ReadBackBuffer reads buffer contents from GPU back to CPU.
func (mre *MetalReplayEngine) ReadBackBuffer(address uint64) ([]byte, error) {
	buffer, ok := mre.MetalBuffers[address]
	if !ok {
		return nil, fmt.Errorf("buffer not found at address 0x%x", address)
	}

	// Get buffer contents
	contents := buffer.Contents()
	length := buffer.Length()

	// Copy data to Go slice
	data := make([]byte, length)
	for i := uint64(0); i < length; i++ {
		data[i] = *(*byte)(unsafe.Pointer(uintptr(contents) + uintptr(i)))
	}

	return data, nil
}

// ValidateExecution compares replay output with original trace.
func (mre *MetalReplayEngine) ValidateExecution(plan *ReplayPlan) (*MetalValidationResult, error) {
	validation := &MetalValidationResult{
		BuffersChecked: 0,
		BuffersMatched: 0,
		BuffersMismatched: 0,
		Differences: make([]string, 0),
	}

	// For each buffer, compare replay output with original trace data
	for addr := range mre.MetalBuffers {
		// Find original buffer data
		var originalData []byte
		if stateAnalysis := plan.StateAnalysis; stateAnalysis != nil {
			for _, bufInfo := range stateAnalysis.Buffers {
				if bufInfo.Address == addr {
					originalData = bufInfo.Contents
					break
				}
			}
		}

		if len(originalData) == 0 {
			continue
		}

		// Read back from GPU
		replayData, err := mre.ReadBackBuffer(addr)
		if err != nil {
			validation.Differences = append(validation.Differences,
				fmt.Sprintf("Buffer 0x%x: failed to read back: %v", addr, err))
			continue
		}

		validation.BuffersChecked++

		// Compare contents
		if len(originalData) != len(replayData) {
			validation.BuffersMismatched++
			validation.Differences = append(validation.Differences,
				fmt.Sprintf("Buffer 0x%x: size mismatch (original=%d, replay=%d)",
					addr, len(originalData), len(replayData)))
			continue
		}

		// Check for differences
		differences := 0
		for i := 0; i < len(originalData); i++ {
			if originalData[i] != replayData[i] {
				differences++
			}
		}

		if differences == 0 {
			validation.BuffersMatched++
		} else {
			validation.BuffersMismatched++
			validation.Differences = append(validation.Differences,
				fmt.Sprintf("Buffer 0x%x: %d bytes differ", addr, differences))
		}
	}

	return validation, nil
}

// MetalReplayResult contains results from Metal GPU execution.
type MetalReplayResult struct {
	TraceePath    string
	Success       bool
	Error         string
	EncodersRun   int
	DispatchesRun int
}

// MetalValidationResult contains buffer validation results.
type MetalValidationResult struct {
	BuffersChecked    int
	BuffersMatched    int
	BuffersMismatched int
	Differences       []string
}

// FormatMetalReplayResult generates a human-readable report.
func FormatMetalReplayResult(result *MetalReplayResult) string {
	output := "=== Metal Replay Result ===\n\n"

	output += fmt.Sprintf("Trace: %s\n\n", result.TraceePath)

	if result.Success {
		output += "Status: ✓ Replay completed successfully\n\n"
	} else {
		output += "Status: ✗ Replay failed\n\n"
		if result.Error != "" {
			output += fmt.Sprintf("Error: %s\n\n", result.Error)
		}
	}

	output += "Execution Summary:\n"
	output += fmt.Sprintf("  Encoders executed: %d\n", result.EncodersRun)
	output += fmt.Sprintf("  Dispatches executed: %d\n", result.DispatchesRun)

	return output
}

// FormatMetalValidationResult generates a validation report.
func FormatMetalValidationResult(validation *MetalValidationResult) string {
	output := "=== Metal Validation Result ===\n\n"

	output += "Buffer Validation:\n"
	output += fmt.Sprintf("  Checked: %d\n", validation.BuffersChecked)
	output += fmt.Sprintf("  Matched: %d\n", validation.BuffersMatched)
	output += fmt.Sprintf("  Mismatched: %d\n\n", validation.BuffersMismatched)

	if len(validation.Differences) > 0 {
		output += "Differences:\n"
		for _, diff := range validation.Differences {
			output += fmt.Sprintf("  - %s\n", diff)
		}
		output += "\n"
	}

	if validation.BuffersMatched == validation.BuffersChecked && validation.BuffersChecked > 0 {
		output += "✓ All buffers match original trace\n"
	}

	return output
}
