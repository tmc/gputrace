package export

import (
	"strings"

	"github.com/google/pprof/profile"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/shader"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases
type ShaderSourceMapper = shader.ShaderSourceMapper

// ToPprofWithSource converts GPU trace to pprof format with Metal shader source mapping.
// This version includes file paths and line numbers for kernels, enabling source code
// navigation in pprof tools.
func ToPprofWithSource(t *trace.Trace, timings []*EncoderTiming, mapper *ShaderSourceMapper) (*profile.Profile, error) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu_time", Unit: "nanoseconds"},
			{Type: "dispatches", Unit: "count"},
		},
	}

	if len(timings) > 0 {
		prof.TimeNanos = int64(timings[0].StartTimestamp)
		var totalDuration uint64
		for _, t := range timings {
			totalDuration += t.DurationNs
		}
		prof.DurationNanos = int64(totalDuration)
	}

	// Add comment with trace info
	prof.Comments = []string{
		"GPU Trace with Metal shader source mapping",
		"Trace: " + t.Path,
		"Kernels: " + string(rune(len(t.KernelNames))),
	}

	// Create hierarchy: GPU Trace > Command Queue > Encoders > Kernels
	gpuTraceFunc := &profile.Function{
		ID:         1,
		Name:       "GPU Trace",
		SystemName: "GPU Trace",
	}
	gpuTraceLoc := &profile.Location{
		ID:   1,
		Line: []profile.Line{{Function: gpuTraceFunc}},
	}

	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
	}

	queueFunc := &profile.Function{
		ID:         2,
		Name:       queueLabel,
		SystemName: queueLabel,
	}
	queueLoc := &profile.Location{
		ID:   2,
		Line: []profile.Line{{Function: queueFunc}},
	}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	funcID := uint64(3)
	locID := uint64(3)

	// Map encoders to kernels
	kernelMap := buildKernelMap(t)

	// Add encoder and kernel samples with source mapping
	for _, timing := range timings {
		// Create encoder function
		encoderFunc := &profile.Function{
			ID:         funcID,
			Name:       timing.Label,
			SystemName: timing.Label,
		}
		prof.Function = append(prof.Function, encoderFunc)
		funcID++

		encoderLoc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: encoderFunc}},
		}
		prof.Location = append(prof.Location, encoderLoc)
		locID++

		// Get associated kernel
		kernelName := "unknown_kernel"
		if kn, ok := kernelMap[timing.Label]; ok {
			kernelName = kn
		}

		// Create kernel function with source mapping
		kernelFunc := &profile.Function{
			ID:         funcID,
			Name:       kernelName,
			SystemName: kernelName,
		}

		// Try to add source file information
		if mapper != nil {
			if sourceFile, lineNum := mapper.GetSourceLocation(kernelName); sourceFile != "" {
				kernelFunc.Filename = sourceFile
				kernelFunc.StartLine = int64(lineNum)
			}
		}

		prof.Function = append(prof.Function, kernelFunc)
		funcID++

		// Create location for kernel with source info
		kernelLoc := &profile.Location{
			ID: locID,
			Line: []profile.Line{
				{
					Function: kernelFunc,
					Line:     kernelFunc.StartLine,
				},
			},
		}

		// Add source mapping to the location if available
		if kernelFunc.Filename != "" {
			kernelLoc.Mapping = &profile.Mapping{
				ID:   1,
				File: kernelFunc.Filename,
			}
			// Ensure mapping is in profile
			if len(prof.Mapping) == 0 {
				prof.Mapping = []*profile.Mapping{kernelLoc.Mapping}
			}
		}

		prof.Location = append(prof.Location, kernelLoc)
		locID++

		// Stack: kernel -> encoder -> queue -> root (leaf to root)
		sample := &profile.Sample{
			Location: []*profile.Location{kernelLoc, encoderLoc, queueLoc, gpuTraceLoc},
			Value:    []int64{int64(timing.DurationNs), 1}, // gpu_time, dispatch_count
		}

		// Add labels
		sample.Label = map[string][]string{
			"encoder": {timing.Label},
			"kernel":  {kernelName},
		}

		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// buildKernelMap creates a mapping from encoder labels to kernel names.
func buildKernelMap(t *trace.Trace) map[string]string {
	m := make(map[string]string)

	// Simple heuristic: match by numbers or keywords
	for _, label := range t.EncoderLabels {
		for _, kernel := range t.KernelNames {
			if matchesEncoderLabel(label, kernel) {
				m[label] = kernel
				break
			}
		}
	}

	// If we have timings but no encoder labels, map directly to kernels
	if len(m) == 0 && len(t.KernelNames) > 0 {
		for i, kernel := range t.KernelNames {
			m[kernel] = kernel
			// Also try to match numbered encoders
			if i < len(t.EncoderLabels) {
				m[t.EncoderLabels[i]] = kernel
			}
		}
	}

	return m
}

// matchesEncoderLabel checks if a kernel name matches an encoder label.
func matchesEncoderLabel(label, kernel string) bool {
	labelLower := strings.ToLower(label)
	kernelLower := strings.ToLower(kernel)

	// Check for number match
	for i := '1'; i <= '9'; i++ {
		if strings.ContainsRune(labelLower, i) && strings.ContainsRune(kernelLower, i) {
			return true
		}
	}

	// Check for keyword match
	keywords := []string{
		"rope", "attention", "norm", "layer", "matmul", "gemm", "gemv",
		"softmax", "relu", "sigmoid", "conv", "affine", "quantize",
		"gather", "copy", "add", "mul", "sub", "div",
	}

	for _, keyword := range keywords {
		if strings.Contains(labelLower, keyword) && strings.Contains(kernelLower, keyword) {
			return true
		}
	}

	return false
}
