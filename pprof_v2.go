package gputrace

import (
	"fmt"
	"time"

	"github.com/google/pprof/profile"
)

// PprofOptions controls pprof profile generation.
type PprofOptions struct {
	// IncludeMetadata adds trace metadata as comments
	IncludeMetadata bool

	// IncludeBufferInfo adds buffer information as comments
	IncludeBufferInfo bool

	// SampleTypes specifies which sample types to include
	// If empty, defaults to ["gpu_time", "dispatches"]
	SampleTypes []string

	// AddKernelLabels adds kernel names as sample labels
	AddKernelLabels bool

	// AddEncoderLabels adds encoder names as sample labels
	AddEncoderLabels bool
}

// DefaultPprofOptions returns sensible defaults.
func DefaultPprofOptions() PprofOptions {
	return PprofOptions{
		IncludeMetadata:  true,
		IncludeBufferInfo: true,
		SampleTypes:      []string{"gpu_time", "dispatches"},
		AddKernelLabels:  true,
		AddEncoderLabels: true,
	}
}

// ToPprofV2 creates an improved pprof profile with enhanced metadata.
func (t *Trace) ToPprofV2(timings []*EncoderTiming, opts PprofOptions) (*profile.Profile, error) {
	// Calculate timing statistics
	var totalNs uint64
	var minNs, maxNs uint64
	if len(timings) > 0 {
		minNs = timings[0].DurationNs
		maxNs = timings[0].DurationNs

		for _, timing := range timings {
			totalNs += timing.DurationNs
			if timing.DurationNs < minNs {
				minNs = timing.DurationNs
			}
			if timing.DurationNs > maxNs {
				maxNs = timing.DurationNs
			}
		}
	}

	// Build sample types
	sampleTypes := opts.SampleTypes
	if len(sampleTypes) == 0 {
		sampleTypes = []string{"gpu_time", "dispatches"}
	}

	var st []*profile.ValueType
	for _, sType := range sampleTypes {
		switch sType {
		case "gpu_time":
			st = append(st, &profile.ValueType{Type: "gpu_time", Unit: "nanoseconds"})
		case "dispatches":
			st = append(st, &profile.ValueType{Type: "dispatches", Unit: "count"})
		case "gpu_memory":
			st = append(st, &profile.ValueType{Type: "gpu_memory", Unit: "bytes"})
		}
	}

	prof := &profile.Profile{
		SampleType: st,
		PeriodType: &profile.ValueType{
			Type: "gpu_time",
			Unit: "nanoseconds",
		},
		Period:            int64(totalNs) / int64(len(timings)+1),
		DefaultSampleType: "gpu_time",
	}

	// Add metadata as comments
	if opts.IncludeMetadata {
		prof.Comments = append(prof.Comments,
			"GPU Trace Profile",
			fmt.Sprintf("Source: %s", t.Path),
			fmt.Sprintf("Generated: %s", time.Now().Format(time.RFC3339)),
		)

		if t.Metadata != nil {
			prof.Comments = append(prof.Comments,
				fmt.Sprintf("Capture Version: %d", t.Metadata.CaptureVersion),
				fmt.Sprintf("Graphics API: %d (Metal)", t.Metadata.GraphicsAPI),
			)
		}

		prof.Comments = append(prof.Comments,
			fmt.Sprintf("Kernels: %d unique", len(t.KernelNames)),
			fmt.Sprintf("Encoders: %d", len(t.EncoderLabels)),
			fmt.Sprintf("Command Queue: %s", t.CommandQueueLabel),
		)

		// Add timing statistics
		if len(timings) > 0 {
			prof.Comments = append(prof.Comments,
				fmt.Sprintf("Total GPU Time: %.2f ms", float64(totalNs)/1e6),
				fmt.Sprintf("Min Kernel Time: %.2f ms", float64(minNs)/1e6),
				fmt.Sprintf("Max Kernel Time: %.2f ms", float64(maxNs)/1e6),
				fmt.Sprintf("Avg Kernel Time: %.2f ms", float64(totalNs)/float64(len(timings))/1e6),
			)
		}

		// Add buffer information
		if opts.IncludeBufferInfo {
			if meta, err := t.ExtractEnhancedMetadata(); err == nil && len(meta.BufferBindings) > 0 {
				var totalBufferSize uint64
				for _, buf := range meta.BufferBindings {
					totalBufferSize += buf.Size
				}
				prof.Comments = append(prof.Comments,
					fmt.Sprintf("Buffers: %d (%.2f MB total)", len(meta.BufferBindings), float64(totalBufferSize)/(1024*1024)),
				)
			}
		}
	}

	// Set time range
	if len(timings) > 0 {
		prof.TimeNanos = int64(timings[0].StartTimestamp)
		prof.DurationNanos = int64(totalNs)
	}

	// Create function and location maps
	functionMap := make(map[string]*profile.Function)
	locationMap := make(map[string]*profile.Location)

	getFunction := func(name, filename string) *profile.Function {
		key := fmt.Sprintf("%s:%s", name, filename)
		if fn, ok := functionMap[key]; ok {
			return fn
		}

		fn := &profile.Function{
			ID:         uint64(len(functionMap) + 1),
			Name:       name,
			SystemName: name,
			Filename:   filename,
		}
		functionMap[key] = fn
		prof.Function = append(prof.Function, fn)
		return fn
	}

	getLocation := func(name, filename string) *profile.Location {
		key := fmt.Sprintf("%s:%s", name, filename)
		if loc, ok := locationMap[key]; ok {
			return loc
		}

		fn := getFunction(name, filename)
		loc := &profile.Location{
			ID:   uint64(len(locationMap) + 1),
			Line: []profile.Line{{Function: fn}},
		}
		locationMap[key] = loc
		prof.Location = append(prof.Location, loc)
		return loc
	}

	// Build hierarchy
	gpuTraceLoc := getLocation("GPU Trace", "GPU")

	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
	}
	queueLoc := getLocation(queueLabel, "GPU")

	// Map encoders to kernels
	kernelMap := t.buildKernelMapV2()

	// Extract buffer info for memory samples
	var bufferMap map[string]uint64
	if containsString(sampleTypes, "gpu_memory") {
		if meta, err := t.ExtractEnhancedMetadata(); err == nil {
			bufferMap = make(map[string]uint64)
			for _, buf := range meta.BufferBindings {
				bufferMap[buf.Name] = buf.Size
			}
		}
	}

	// Create samples
	for i, timing := range timings {
		encoderLoc := getLocation(timing.Label, "GPU/Encoder")

		// Find matching kernel
		kernelName := "unknown_kernel"
		if kn, ok := kernelMap[timing.Label]; ok {
			kernelName = kn
		} else if i < len(t.KernelNames) {
			kernelName = t.KernelNames[i]
		}
		kernelLoc := getLocation(kernelName, "GPU/Kernel")

		// Stack trace (leaf to root)
		stack := []*profile.Location{
			kernelLoc,
			encoderLoc,
			queueLoc,
			gpuTraceLoc,
		}

		// Build sample values
		values := make([]int64, len(sampleTypes))
		for idx, sType := range sampleTypes {
			switch sType {
			case "gpu_time":
				values[idx] = int64(timing.DurationNs)
			case "dispatches":
				values[idx] = 1
			case "gpu_memory":
				// Estimate memory usage (very approximate)
				if bufferMap != nil {
					var memUsage uint64
					for _, size := range bufferMap {
						memUsage += size
					}
					values[idx] = int64(memUsage / uint64(len(timings)))
				}
			}
		}

		sample := &profile.Sample{
			Location: stack,
			Value:    values,
		}

		// Add labels if requested
		if opts.AddEncoderLabels || opts.AddKernelLabels {
			sample.Label = make(map[string][]string)

			if opts.AddEncoderLabels {
				sample.Label["encoder"] = []string{timing.Label}
			}
			if opts.AddKernelLabels {
				sample.Label["kernel"] = []string{kernelName}
			}
		}

		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// buildKernelMapV2 creates a mapping from encoder labels to kernel names.
func (t *Trace) buildKernelMapV2() map[string]string {
	m := make(map[string]string)

	// Try to match encoder labels to kernel names by content
	for _, label := range t.EncoderLabels {
		for _, kernel := range t.KernelNames {
			if matchesLabelV2(label, kernel) {
				m[label] = kernel
				break
			}
		}
	}

	return m
}

// matchesLabelV2 checks if a kernel name matches an encoder label.
func matchesLabelV2(label, kernel string) bool {
	labelLower := toLowerSimple(label)
	kernelLower := toLowerSimple(kernel)

	// Direct match
	if labelLower == kernelLower {
		return true
	}

	// Check for number match (Stage1 -> step1, etc.)
	for i := byte('1'); i <= '9'; i++ {
		if containsSubstring(labelLower, string(i)) && containsSubstring(kernelLower, string(i)) {
			return true
		}
	}

	// Check for name component match
	keywords := []string{
		"normalize", "relu", "scale", "conv", "matmul", "softmax",
		"attention", "rope", "quantize", "dequantize", "affine",
	}
	for _, keyword := range keywords {
		if containsSubstring(labelLower, keyword) && containsSubstring(kernelLower, keyword) {
			return true
		}
	}

	return false
}

// Helper function
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
