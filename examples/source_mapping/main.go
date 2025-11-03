// Example: GPU Trace to pprof with Metal Shader Source Mapping
//
// This example demonstrates how to convert a .gputrace file to pprof format
// with Metal shader source code mapping enabled.
//
// Usage:
//   go run main.go <trace.gputrace> [shader-search-path]
//
// Example:
//   go run main.go /tmp/benchmark.gputrace
//   go run main.go /tmp/benchmark.gputrace "/path/to/mlx/backend/metal/*.metal"

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <trace.gputrace> [shader-search-path]\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]
	var shaderPaths []string
	if len(os.Args) > 2 {
		shaderPaths = os.Args[2:]
	}

	fmt.Printf("Loading GPU trace: %s\n", tracePath)

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		log.Fatalf("Failed to open trace: %v", err)
	}

	fmt.Printf("✓ Trace loaded\n")
	fmt.Printf("  Command Queue: %s\n", trace.CommandQueueLabel)
	fmt.Printf("  Kernel Names: %d\n", len(trace.KernelNames))
	fmt.Printf("  Encoder Labels: %d\n\n", len(trace.EncoderLabels))

	// Extract timing
	fmt.Println("Extracting timing data...")
	timings, err := trace.ExtractTimingData()
	if err != nil || len(timings) == 0 {
		// Try synthetic timing
		timings = trace.GenerateSyntheticTiming()
		if len(timings) == 0 {
			log.Fatal("No timing data available")
		}
		fmt.Println("⚠ Using synthetic timing (no real timing data)")
	} else {
		fmt.Printf("✓ Extracted %d timing samples\n", len(timings))
	}

	// Create source mapper
	fmt.Println("\nIndexing Metal shader sources...")
	var mapper *gputrace.ShaderSourceMapper
	if len(shaderPaths) > 0 {
		mapper = gputrace.NewShaderSourceMapper(shaderPaths...)
	} else {
		mapper = gputrace.NewShaderSourceMapper()
		fmt.Println("  Using default search paths")
	}

	if err := mapper.IndexShaderSources(); err != nil {
		log.Printf("Warning: shader indexing encountered errors: %v", err)
	}

	files, kernels := mapper.Stats()
	fmt.Printf("✓ Indexed %d kernels from %d files\n\n", kernels, files)

	// Show some mapped kernels
	if kernels > 0 {
		fmt.Println("Sample kernel mappings:")
		count := 0
		for _, kernelName := range trace.KernelNames {
			if count >= 5 {
				break
			}
			file, line := mapper.GetSourceLocation(kernelName)
			if file != "" {
				fmt.Printf("  %-40s -> %s:%d\n", kernelName, file, line)
				count++
			}
		}
		if count == 0 {
			fmt.Println("  (no kernels mapped - names may not match source)")
		}
		fmt.Println()
	}

	// Generate pprof with source mapping
	fmt.Println("Generating pprof with source mapping...")
	prof, err := trace.ToPprofWithSource(timings, mapper)
	if err != nil {
		log.Fatalf("Failed to generate pprof: %v", err)
	}

	// Write profile
	outputPath := "gpu_with_source.pprof"
	f, err := os.Create(outputPath)
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer f.Close()

	if err := prof.Write(f); err != nil {
		log.Fatalf("Failed to write pprof: %v", err)
	}

	fmt.Printf("✓ Profile written to: %s\n\n", outputPath)

	// Show next steps
	fmt.Println("Next steps:")
	fmt.Println("  1. View in pprof web UI:")
	fmt.Printf("     go tool pprof -http=:8080 %s\n\n", outputPath)
	fmt.Println("  2. In browser, click 'Source' view to see Metal shader code")
	fmt.Println("  3. Click on any kernel function to jump to its source\n")

	// Show profile statistics
	fmt.Println("Profile statistics:")
	fmt.Printf("  Sample types: %d\n", len(prof.SampleType))
	for i, st := range prof.SampleType {
		fmt.Printf("    [%d] %s (%s)\n", i, st.Type, st.Unit)
	}
	fmt.Printf("  Functions: %d\n", len(prof.Function))
	fmt.Printf("  Locations: %d\n", len(prof.Location))
	fmt.Printf("  Samples: %d\n", len(prof.Sample))
	fmt.Printf("  Duration: %.2f ms\n", float64(prof.DurationNanos)/1e6)

	// Count how many locations have source mapping
	mappedCount := 0
	for _, loc := range prof.Location {
		if loc.Mapping != nil && loc.Mapping.File != "" {
			mappedCount++
		}
	}
	fmt.Printf("  Locations with source: %d (%.1f%%)\n",
		mappedCount, 100.0*float64(mappedCount)/float64(len(prof.Location)))
}
