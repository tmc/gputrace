// Command enhanced-timing demonstrates enhanced GPU timing extraction
// from multiple sources: MTSP, kdebug, and signposts.
//
// Usage:
//   enhanced-timing <trace.gputrace>
//
// Example:
//   enhanced-timing /tmp/benchmark.gputrace

package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <trace.gputrace>\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]

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

	// Create enhanced timing extractor
	extractor := gputrace.NewEnhancedTimingExtractor(trace)

	fmt.Println("Extracting timing from multiple sources...")
	fmt.Println("  - MTSP records (command structure)")
	fmt.Println("  - kdebug events (accurate timing)")
	fmt.Println("  - OS signposts (shader profiling)")
	fmt.Println()

	// Extract enhanced timing
	timings, err := extractor.ExtractEnhancedTiming()
	if err != nil {
		log.Printf("Warning: %v", err)
		log.Println("Falling back to standard timing extraction...")

		// Try standard extraction
		standardTimings, err := trace.ExtractTimingData()
		if err != nil || len(standardTimings) == 0 {
			log.Fatal("No timing data available from any source")
		}

		// Convert to enhanced format for display
		timings = make([]*gputrace.EnhancedTiming, len(standardTimings))
		for i, st := range standardTimings {
			timings[i] = &gputrace.EnhancedTiming{
				EncoderLabel:   st.Label,
				EncoderIndex:   i,
				StartTimestamp: st.StartTimestamp,
				EndTimestamp:   st.EndTimestamp,
			}
		}
	}

	fmt.Printf("✓ Extracted %d timing samples\n\n", len(timings))

	// Generate and print report
	report := gputrace.EnhancedTimingReport(timings)
	fmt.Println(report)

	// Show top 10 by execution time
	if len(timings) > 0 {
		fmt.Println("\nTop 10 by Execution Time:")
		fmt.Println(strings.Repeat("-", 120))

		// Sort by execution time
		sorted := make([]*gputrace.EnhancedTiming, len(timings))
		copy(sorted, timings)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].ExecutionTime > sorted[j].ExecutionTime
		})

		for i := 0; i < 10 && i < len(sorted); i++ {
			fmt.Printf("%2d. %s\n", i+1, gputrace.FormatEnhancedTiming(sorted[i]))
		}
	}

	// Try to extract kdebug events separately
	fmt.Println("\n" + strings.Repeat("=", 120))
	fmt.Println("kdebug Event Analysis")
	fmt.Println(strings.Repeat("=", 120))

	kdebugParser := gputrace.NewKDebugParser(trace)
	kdebugEvents, err := kdebugParser.ParseKDebugEvents()
	if err != nil {
		fmt.Printf("⚠ kdebug events not available: %v\n", err)
	} else {
		fmt.Printf("✓ Found %d kdebug GPU events\n\n", len(kdebugEvents))

		// Show first 10
		fmt.Println("First 10 kdebug events:")
		for i := 0; i < 10 && i < len(kdebugEvents); i++ {
			fmt.Printf("  %2d. %s\n", i+1, gputrace.FormatKDebugEvent(kdebugEvents[i]))
		}

		// Correlate into intervals
		intervals := gputrace.CorrelateGPUExecution(kdebugEvents)
		fmt.Printf("\n✓ Correlated into %d execution intervals\n", len(intervals))

		if len(intervals) > 0 {
			fmt.Println("\nExecution Intervals:")
			fmt.Printf("%-15s %-15s %-12s %-12s %-12s\n",
				"CmdBuf ID", "Encoder ID", "Duration", "Queue Latency", "Total")
			fmt.Println(strings.Repeat("-", 80))

			for i := 0; i < 10 && i < len(intervals); i++ {
				interval := intervals[i]
				fmt.Printf("0x%-13x 0x%-13x %10.2fms %10.2fms %10.2fms\n",
					interval.CommandBufferID,
					interval.EncoderID,
					float64(interval.Duration())/1e6,
					float64(interval.QueueLatency())/1e6,
					float64(interval.Duration()+interval.QueueLatency())/1e6,
				)
			}
		}
	}

	// Try to extract signpost data
	fmt.Println("\n" + strings.Repeat("=", 120))
	fmt.Println("Signpost Analysis")
	fmt.Println(strings.Repeat("=", 120))

	signpostParser := gputrace.NewSignpostParser(trace)
	signposts, err := signpostParser.ParseMetalSignposts()
	if err != nil {
		fmt.Printf("⚠ Signpost data not available: %v\n", err)
	} else {
		fmt.Printf("✓ Found %d Metal signpost intervals\n\n", len(signposts))

		// Show statistics
		stats := gputrace.CalculateSignpostStatistics(signposts)
		fmt.Printf("Total Intervals: %d\n", stats.TotalIntervals)
		fmt.Printf("Metal Intervals: %d\n", stats.MetalIntervals)
		fmt.Printf("Shader Intervals: %d\n", stats.ShaderIntervals)
		fmt.Printf("Total Duration: %.2f ms\n", float64(stats.TotalDurationNs)/1e6)
		fmt.Printf("Average Duration: %.2f ms\n", float64(stats.AverageDurationNs)/1e6)
		fmt.Printf("\nUnique Subsystems: %v\n", stats.UniqueSubsystems)
		fmt.Printf("Unique Categories: %v\n\n", stats.UniqueCategories)

		// Show first 10 signpost intervals
		fmt.Println("First 10 signpost intervals:")
		for i := 0; i < 10 && i < len(signposts); i++ {
			fmt.Printf("  %2d. %s\n", i+1, gputrace.FormatSignpostInterval(signposts[i]))
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 120))
	fmt.Println("Analysis Complete")
	fmt.Println(strings.Repeat("=", 120))
}
