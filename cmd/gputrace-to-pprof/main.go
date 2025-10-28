// Command gputrace-to-pprof converts .gputrace files to pprof format.
//
// This tool extracts GPU kernel names and execution order from Metal GPU traces
// and generates pprof profiles that can be analyzed with go tool pprof.
//
// Usage:
//
//	gputrace-to-pprof <trace.gputrace> [output.pprof.gz]
//
// If output is not specified, it defaults to <trace-name>.pprof.gz
//
// Example:
//
//	gputrace-to-pprof benchmark.gputrace
//	# Generates: benchmark.pprof.gz
//
//	go tool pprof benchmark.pprof.gz
//	# Analyze with pprof interactive shell
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <trace.gputrace> [output.pprof.gz]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nConverts Metal GPU trace files to pprof format.\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s benchmark.gputrace\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  go tool pprof benchmark.pprof.gz\n")
		os.Exit(1)
	}

	tracePath := os.Args[1]

	// Determine output path
	outputPath := ""
	if len(os.Args) >= 3 {
		outputPath = os.Args[2]
	} else {
		// Default: <tracename>.pb (protobuf, uncompressed)
		base := filepath.Base(tracePath)
		if ext := filepath.Ext(base); ext != "" {
			base = base[:len(base)-len(ext)]
		}
		outputPath = base + ".pb"
	}

	log.Printf("Converting %s to %s...", tracePath, outputPath)

	// Open and parse the trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		log.Fatalf("Failed to open trace: %v", err)
	}

	// Extract timing data (may be empty if store0 has no timing)
	timings, err := trace.ExtractTimingData()
	if err != nil {
		log.Printf("Warning: timing extraction failed: %v", err)
	}

	// If no timing data, create synthetic timing based on kernel order
	if len(timings) == 0 {
		log.Printf("No timing data available in trace, creating synthetic timing based on kernel order")
		timings = createSyntheticTiming(trace)
	}

	// Convert to pprof
	prof, err := createGPUProfile(trace, timings)
	if err != nil {
		log.Fatalf("Failed to create profile: %v", err)
	}

	// Write to file
	if err := writeProfile(prof, outputPath); err != nil {
		log.Fatalf("Failed to write profile: %v", err)
	}

	// Print summary
	log.Printf("Successfully converted %d kernel dispatches", len(timings))
	log.Printf("Output: %s", outputPath)
	log.Printf("\nAnalyze with:")
	log.Printf("  go tool pprof %s", outputPath)
	log.Printf("  go tool pprof -http=:8080 %s", outputPath)
}

// createSyntheticTiming creates synthetic timing data when real timing is not available.
// This uses kernel execution order and assigns approximate equal time to each kernel.
func createSyntheticTiming(trace *gputrace.Trace) []*gputrace.EncoderTiming {
	// Use encoder labels if available, otherwise use kernel names
	labels := trace.EncoderLabels
	if len(labels) == 0 {
		labels = trace.KernelNames
	}

	if len(labels) == 0 {
		return nil
	}

	// Assign synthetic timing: 1ms per kernel
	// This is just for visualization purposes
	const syntheticDurationNs = 1_000_000 // 1ms

	timings := make([]*gputrace.EncoderTiming, 0, len(labels))
	startTime := uint64(0)

	for _, label := range labels {
		timing := &gputrace.EncoderTiming{
			Label:          label,
			StartTimestamp: startTime,
			EndTimestamp:   startTime + syntheticDurationNs,
			DurationNs:     syntheticDurationNs,
			DurationMs:     1.0,
			Percentage:     100.0 / float32(len(labels)),
		}
		timings = append(timings, timing)
		startTime += syntheticDurationNs
	}

	return timings
}

// createGPUProfile creates a pprof profile from GPU trace data.
// The profile shows a hierarchy: GPU Trace > Command Queue > Encoder > Kernel
func createGPUProfile(trace *gputrace.Trace, timings []*gputrace.EncoderTiming) (*profile.Profile, error) {
	// Calculate total duration
	var totalNs uint64
	for _, t := range timings {
		totalNs += t.DurationNs
	}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu_time", Unit: "nanoseconds"},
			{Type: "dispatches", Unit: "count"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu_time",
			Unit: "nanoseconds",
		},
		Period:        int64(totalNs) / int64(len(timings)+1),
		DefaultSampleType: "gpu_time",
		Comments: []string{
			"GPU trace profile from Metal .gputrace capture",
			fmt.Sprintf("Trace: %s", trace.Path),
			fmt.Sprintf("Kernels: %d", len(trace.KernelNames)),
			fmt.Sprintf("Encoders: %d", len(trace.EncoderLabels)),
		},
	}

	if len(timings) > 0 {
		prof.TimeNanos = int64(timings[0].StartTimestamp)
		prof.DurationNanos = int64(totalNs)
	}

	// Create function and location maps for deduplication
	functionMap := make(map[string]*profile.Function)
	locationMap := make(map[string]*profile.Location)

	getFunction := func(name string) *profile.Function {
		if fn, ok := functionMap[name]; ok {
			return fn
		}
		fn := &profile.Function{
			ID:         uint64(len(functionMap) + 1),
			Name:       name,
			SystemName: name,
			Filename:   "GPU",
		}
		functionMap[name] = fn
		prof.Function = append(prof.Function, fn)
		return fn
	}

	getLocation := func(name string) *profile.Location {
		if loc, ok := locationMap[name]; ok {
			return loc
		}
		fn := getFunction(name)
		loc := &profile.Location{
			ID:   uint64(len(locationMap) + 1),
			Line: []profile.Line{{Function: fn}},
		}
		locationMap[name] = loc
		prof.Location = append(prof.Location, loc)
		return loc
	}

	// Build hierarchy: GPU Trace > Command Queue > Encoder > Kernel
	gpuTraceLoc := getLocation("GPU Trace")

	queueLabel := trace.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
	}
	queueLoc := getLocation(queueLabel)

	// Map encoder labels to kernel names
	kernelMap := buildKernelMap(trace)

	// Create samples
	for i, timing := range timings {
		encoderLoc := getLocation(timing.Label)

		// Find matching kernel name
		kernelName := "unknown_kernel"
		if kn, ok := kernelMap[timing.Label]; ok {
			kernelName = kn
		} else if i < len(trace.KernelNames) {
			// Fallback: use kernel by index
			kernelName = trace.KernelNames[i]
		}
		kernelLoc := getLocation(kernelName)

		// Stack trace (leaf to root): Kernel > Encoder > Queue > GPU Trace
		stack := []*profile.Location{
			kernelLoc,
			encoderLoc,
			queueLoc,
			gpuTraceLoc,
		}

		sample := &profile.Sample{
			Location: stack,
			Value:    []int64{int64(timing.DurationNs), 1}, // time, dispatch count
			Label: map[string][]string{
				"encoder": {timing.Label},
				"kernel":  {kernelName},
			},
		}

		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// buildKernelMap creates a mapping from encoder labels to kernel names.
func buildKernelMap(trace *gputrace.Trace) map[string]string {
	m := make(map[string]string)

	// Try to match encoder labels to kernel names by content
	for _, label := range trace.EncoderLabels {
		for _, kernel := range trace.KernelNames {
			if matchesLabel(label, kernel) {
				m[label] = kernel
				break
			}
		}
	}

	return m
}

// matchesLabel checks if a kernel name matches an encoder label.
func matchesLabel(label, kernel string) bool {
	labelLower := strings.ToLower(label)
	kernelLower := strings.ToLower(kernel)

	// Direct match
	if labelLower == kernelLower {
		return true
	}

	// Check for number match (Stage1 -> step1, etc.)
	for i := '1'; i <= '9'; i++ {
		if strings.Contains(labelLower, string(i)) && strings.Contains(kernelLower, string(i)) {
			return true
		}
	}

	// Check for name component match
	keywords := []string{
		"normalize", "relu", "scale", "conv", "matmul", "softmax",
		"attention", "rope", "quantize", "dequantize", "affine",
	}
	for _, keyword := range keywords {
		if strings.Contains(labelLower, keyword) && strings.Contains(kernelLower, keyword) {
			return true
		}
	}

	return false
}

// writeProfile writes a pprof profile to a file.
// Note: pprof profiles should NOT be gzipped when using go tool pprof,
// as the tool expects raw protobuf format.
func writeProfile(prof *profile.Profile, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	// Note: We write uncompressed protobuf format
	// go tool pprof expects raw protobuf, not gzipped
	// If you need compression for storage, use external tools like gzip
	return prof.Write(f)
}
