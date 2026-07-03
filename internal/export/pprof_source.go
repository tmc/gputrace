package export

import (
	"fmt"
	"strings"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/trace"
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
		fmt.Sprintf("Kernels: %d", len(t.KernelNames)),
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
			if sourceFile, lineNum := mapper.SourceLocation(kernelName); sourceFile != "" {
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

// ToPprofWithSourceLines creates a pprof profile with per-source-line samples.
// This enables 'go tool pprof -list kernel_name' to show line-by-line costs.
// Each source line gets a separate sample with timing distributed by estimated cost.
func ToPprofWithSourceLines(t *trace.Trace, timings []*EncoderTiming, mapper *ShaderSourceMapper) (*profile.Profile, error) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu_time", Unit: "nanoseconds"},
			{Type: "dispatches", Unit: "count"},
		},
	}

	if len(timings) > 0 {
		prof.TimeNanos = int64(timings[0].StartTimestamp)
		var totalDuration uint64
		for _, timing := range timings {
			totalDuration += timing.DurationNs
		}
		prof.DurationNanos = int64(totalDuration)
	}

	// Add metadata
	prof.Comments = []string{
		"GPU Trace Profile with Source Line Attribution",
		"Trace: " + t.Path,
		"Use: go tool pprof -list <kernel_name> to see per-line costs",
	}

	// Create root hierarchy
	gpuTraceFunc := &profile.Function{ID: 1, Name: "GPU Trace", SystemName: "GPU Trace"}
	gpuTraceLoc := &profile.Location{ID: 1, Line: []profile.Line{{Function: gpuTraceFunc}}}

	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
	}
	queueFunc := &profile.Function{ID: 2, Name: queueLabel, SystemName: queueLabel}
	queueLoc := &profile.Location{ID: 2, Line: []profile.Line{{Function: queueFunc}}}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	funcID := uint64(3)
	locID := uint64(3)

	// Build kernel map
	kernelMap := buildKernelMap(t)

	// Track files for mappings
	fileToMapping := make(map[string]*profile.Mapping)
	mappingID := uint64(1)

	// Process each encoder timing
	for _, timing := range timings {
		// Create encoder function and location
		encoderFunc := &profile.Function{ID: funcID, Name: timing.Label, SystemName: timing.Label}
		prof.Function = append(prof.Function, encoderFunc)
		funcID++

		encoderLoc := &profile.Location{ID: locID, Line: []profile.Line{{Function: encoderFunc}}}
		prof.Location = append(prof.Location, encoderLoc)
		locID++

		// Get associated kernel name
		kernelName := timing.Label
		if kernelName == "" {
			kernelName = "unknown_kernel"
		}
		if kn, ok := kernelMap[timing.Label]; ok {
			kernelName = kn
		}

		// Try to get source attribution for this kernel
		if mapper != nil {
			sourceFile, startLine := mapper.SourceLocation(kernelName)
			if sourceFile != "" {
				// Get or create mapping for this source file
				mapping, exists := fileToMapping[sourceFile]
				if !exists {
					mapping = &profile.Mapping{ID: mappingID, File: sourceFile}
					prof.Mapping = append(prof.Mapping, mapping)
					fileToMapping[sourceFile] = mapping
					mappingID++
				}

				// Create kernel function with source info
				kernelFunc := &profile.Function{
					ID:         funcID,
					Name:       kernelName,
					SystemName: kernelName,
					Filename:   sourceFile,
					StartLine:  int64(startLine),
				}
				prof.Function = append(prof.Function, kernelFunc)
				funcID++

				// Get line-level attribution
				attribution, err := shader.ExtractShaderSourceAttributionWithMapper(t, kernelName, mapper)
				if err == nil && len(attribution.Lines) > 0 {
					// Create samples for each source line with non-zero cost
					for _, lineAttr := range attribution.Lines {
						if lineAttr.EstimatedCost == 0 {
							continue
						}

						// Create function for this specific line
						lineFunc := &profile.Function{
							ID:         funcID,
							Name:       kernelName,
							SystemName: kernelName,
							Filename:   sourceFile,
							StartLine:  int64(lineAttr.LineNumber),
						}
						prof.Function = append(prof.Function, lineFunc)
						funcID++

						// Create location for this line
						lineLoc := &profile.Location{
							ID:      locID,
							Mapping: mapping,
							Line: []profile.Line{
								{Function: lineFunc, Line: int64(lineAttr.LineNumber)},
							},
						}
						prof.Location = append(prof.Location, lineLoc)
						locID++

						// Distribute timing based on cost percentage
						lineTimeNs := int64(float64(timing.DurationNs) * lineAttr.GPUTimePercent / 100.0)
						if lineTimeNs < 1 {
							lineTimeNs = 1 // Minimum 1ns to show in profile
						}

						// Create sample: line -> kernel -> encoder -> queue -> root
						sample := &profile.Sample{
							Location: []*profile.Location{lineLoc, encoderLoc, queueLoc, gpuTraceLoc},
							Value:    []int64{lineTimeNs, 1},
							Label: map[string][]string{
								"encoder":     {timing.Label},
								"kernel":      {kernelName},
								"source_line": {strings.TrimSpace(lineAttr.SourceCode)},
								"instruction": {lineAttr.InstructionType},
							},
						}
						prof.Sample = append(prof.Sample, sample)
					}
				} else {
					// Fallback: create single sample at function level
					kernelLoc := &profile.Location{
						ID:      locID,
						Mapping: mapping,
						Line:    []profile.Line{{Function: kernelFunc, Line: int64(startLine)}},
					}
					prof.Location = append(prof.Location, kernelLoc)
					locID++

					sample := &profile.Sample{
						Location: []*profile.Location{kernelLoc, encoderLoc, queueLoc, gpuTraceLoc},
						Value:    []int64{int64(timing.DurationNs), 1},
						Label: map[string][]string{
							"encoder": {timing.Label},
							"kernel":  {kernelName},
						},
					}
					prof.Sample = append(prof.Sample, sample)
				}
			} else {
				// No source mapping - create simple sample
				kernelFunc := &profile.Function{
					ID:         funcID,
					Name:       kernelName,
					SystemName: kernelName,
				}
				prof.Function = append(prof.Function, kernelFunc)
				funcID++

				kernelLoc := &profile.Location{
					ID:   locID,
					Line: []profile.Line{{Function: kernelFunc}},
				}
				prof.Location = append(prof.Location, kernelLoc)
				locID++

				sample := &profile.Sample{
					Location: []*profile.Location{kernelLoc, encoderLoc, queueLoc, gpuTraceLoc},
					Value:    []int64{int64(timing.DurationNs), 1},
					Label: map[string][]string{
						"encoder": {timing.Label},
						"kernel":  {kernelName},
					},
				}
				prof.Sample = append(prof.Sample, sample)
			}
		}
	}

	return prof, nil
}
