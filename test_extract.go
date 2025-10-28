// +build ignore

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <path-to-.gputrace>\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]

	trace, err := gputrace.Open(tracePath)
	if err != nil {
		log.Fatalf("Failed to open trace: %v", err)
	}

	fmt.Printf("=== Trace: %s ===\n\n", tracePath)

	fmt.Printf("Encoder Labels (%d):\n", len(trace.EncoderLabels))
	for i, label := range trace.EncoderLabels {
		fmt.Printf("  [%d] %q\n", i, label)
	}
	fmt.Println()

	fmt.Printf("Kernel Names (%d):\n", len(trace.KernelNames))
	for i, name := range trace.KernelNames {
		fmt.Printf("  [%d] %q\n", i, name)
	}
	fmt.Println()

	fmt.Println("Attempting to extract timing data...")
	timings, err := trace.ExtractTimingData()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	fmt.Printf("Timings extracted: %d\n", len(timings))
	for _, timing := range timings {
		fmt.Printf("  %s: %.2f ms\n", timing.Label, timing.DurationMs)
	}
}
