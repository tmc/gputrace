// Command gputrace2pprof converts .gputrace files to pprof format with shader-level timing breakdowns.
//
// Usage:
//
//	gputrace2pprof trace.gputrace
//	gputrace2pprof trace.gputrace -o output.pprof
//	gputrace2pprof trace.gputrace -all -prefix results
//
// This tool generates pprof profiles showing GPU shader timing breakdowns.
// The resulting pprof files can be analyzed with standard Go profiling tools:
//
//	go tool pprof output.pprof
//	go tool pprof -http=:8080 output.pprof
//
// Example workflow:
//
//	# 1. Capture GPU trace from benchmark
//	MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=1x
//
//	# 2. Convert to pprof
//	gputrace2pprof /tmp/forward_pass_*.gputrace -all -prefix gpu_analysis
//
//	# 3. Analyze with pprof
//	go tool pprof -top gpu_analysis.gpu.pprof
//	go tool pprof -http=:8080 gpu_analysis.gpu.pprof
//
// The pprof profile shows GPU time organized hierarchically:
//
//	GPU Trace
//	  └─ CommandQueue
//	      └─ Encoder
//	          └─ Kernel (shader)
//
// This makes it easy to identify which shaders are consuming the most GPU time.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tmc/mlx-go/experiments/gputrace"
	"github.com/tmc/mlx-go/experiments/mlxprof"
)

var (
	output     = flag.String("o", "", "Output pprof file path (default: trace_name.pprof)")
	prefix     = flag.String("prefix", "", "Output prefix for -all mode (default: trace name)")
	all        = flag.Bool("all", false, "Generate all profile formats (gpu, combined, text)")
	verbose    = flag.Bool("v", false, "Verbose output")
	textReport = flag.Bool("text", false, "Generate text report only")
	stats      = flag.Bool("stats", false, "Show trace statistics only")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <trace.gputrace>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Convert .gputrace files to pprof format with shader-level timing breakdowns.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Convert to single pprof file\n")
		fmt.Fprintf(os.Stderr, "  %s trace.gputrace -o output.pprof\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Generate all formats\n")
		fmt.Fprintf(os.Stderr, "  %s trace.gputrace -all -prefix analysis\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # View with pprof\n")
		fmt.Fprintf(os.Stderr, "  go tool pprof -top output.pprof\n")
		fmt.Fprintf(os.Stderr, "  go tool pprof -http=:8080 output.pprof\n")
	}

	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	tracePath := flag.Arg(0)

	// Verify trace file exists
	info, err := os.Stat(tracePath)
	if os.IsNotExist(err) {
		log.Fatalf("Trace file not found: %s", tracePath)
	}
	if err != nil {
		log.Fatalf("Error accessing trace file: %v", err)
	}

	// Verify it's a directory (gputrace bundles are directories)
	if !info.IsDir() {
		log.Fatalf("Trace path must be a .gputrace directory bundle, got file: %s", tracePath)
	}

	// Verify it has .gputrace extension
	if filepath.Ext(tracePath) != ".gputrace" {
		log.Printf("Warning: trace path does not have .gputrace extension: %s", tracePath)
	}

	if *verbose {
		fmt.Printf("Loading GPU trace: %s\n", tracePath)
	}

	// If stats-only mode, just extract and display statistics
	if *stats {
		trace, err := gputrace.Open(tracePath)
		if err != nil {
			log.Fatalf("Failed to open trace: %v", err)
		}

		statistics, err := trace.ExtractStatistics()
		if err != nil {
			log.Fatalf("Failed to extract statistics: %v", err)
		}

		fmt.Print(statistics.FormatStatistics())
		return
	}

	// Create profiler
	prof, err := mlxprof.FromGPUTrace(tracePath)
	if err != nil {
		log.Fatalf("Failed to load trace: %v\n\nPlease ensure this is a valid .gputrace directory bundle.", err)
	}
	defer func() {
		if closeErr := prof.Close(); closeErr != nil {
			log.Printf("Warning: error closing profiler: %v", closeErr)
		}
	}()

	// Show summary if verbose (including statistics)
	if *verbose {
		prof.PrintSummary()
		fmt.Println()

		// Also show statistics
		trace, err := gputrace.Open(tracePath)
		if err == nil {
			statistics, err := trace.ExtractStatistics()
			if err == nil {
				fmt.Print(statistics.FormatStatistics())
			}
		}
	}

	// Determine output paths
	baseName := filepath.Base(tracePath)
	if ext := filepath.Ext(baseName); ext != "" {
		baseName = baseName[:len(baseName)-len(ext)]
	}

	outputPrefix := *prefix
	if outputPrefix == "" {
		outputPrefix = baseName
	}

	// Generate outputs
	if *all {
		// Generate all formats
		if *verbose {
			fmt.Printf("Generating all profile formats with prefix: %s\n", outputPrefix)
		}

		if err := prof.WriteAll(outputPrefix); err != nil {
			log.Fatalf("Failed to write profiles: %v", err)
		}

		fmt.Printf("✅ Generated profiles:\n")
		fmt.Printf("   %s.gpu.pprof       - Hierarchical GPU profile\n", outputPrefix)
		fmt.Printf("   %s.gpu-flat.pprof  - Flat GPU profile\n", outputPrefix)
		fmt.Printf("   %s.combined.pprof  - Combined multi-view profile\n", outputPrefix)
		fmt.Printf("   %s.txt             - Human-readable report\n", outputPrefix)
		fmt.Printf("\nView with: go tool pprof -top %s.gpu.pprof\n", outputPrefix)
		fmt.Printf("Or:        go tool pprof -http=:8080 %s.gpu.pprof\n", outputPrefix)

	} else if *textReport {
		// Generate text report only
		outputPath := *output
		if outputPath == "" {
			outputPath = outputPrefix + ".txt"
		}

		if err := prof.WriteTextReport(outputPath); err != nil {
			log.Fatalf("Failed to write text report: %v", err)
		}

		fmt.Printf("✅ Text report written to: %s\n", outputPath)

	} else {
		// Generate single pprof file
		outputPath := *output
		if outputPath == "" {
			outputPath = outputPrefix + ".pprof"
		}

		if *verbose {
			fmt.Printf("Writing pprof to: %s\n", outputPath)
		}

		if err := prof.WriteGPUProfile(outputPath); err != nil {
			log.Fatalf("Failed to write pprof: %v", err)
		}

		fmt.Printf("✅ GPU profile written to: %s\n", outputPath)
		fmt.Printf("\nView with: go tool pprof -top %s\n", outputPath)
		fmt.Printf("Or:        go tool pprof -http=:8080 %s\n", outputPath)
	}
}
