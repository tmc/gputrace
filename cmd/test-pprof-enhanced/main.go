package main

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <trace.gputrace> [output.pprof]\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]
	outputPath := "test.pprof"
	if len(os.Args) > 2 {
		outputPath = os.Args[2]
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening trace: %v\n", err)
		os.Exit(1)
	}

	// Generate enhanced pprof
	fmt.Println("Generating enhanced pprof profile...")
	prof, err := gputrace.ToPprofWithMetrics(trace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating pprof: %v\n", err)
		os.Exit(1)
	}

	// Write to file
	f, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if err := prof.Write(f); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing pprof: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Enhanced pprof profile written to: %s\n", outputPath)
	fmt.Printf("\nProfile stats:\n")
	fmt.Printf("  Functions: %d\n", len(prof.Function))
	fmt.Printf("  Locations: %d\n", len(prof.Location))
	fmt.Printf("  Samples: %d\n", len(prof.Sample))
	fmt.Printf("  Duration: %dns (%.2fms)\n", prof.DurationNanos, float64(prof.DurationNanos)/1e6)

	fmt.Printf("\nAnalyze with:\n")
	fmt.Printf("  go tool pprof -top %s\n", outputPath)
	fmt.Printf("  go tool pprof -http=:8080 %s\n", outputPath)
}
