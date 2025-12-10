package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var timingProfilerCmd = &cobra.Command{
	Use:   "timing-profiler <trace.gputrace>",
	Short: "Extract timing from .gpuprofiler_raw performance counter files",
	Long: `Extract GPU timing data from .gpuprofiler_raw hardware performance counters.

This command parses the binary performance counter files that Xcode Instruments
creates when profiling GPU workloads. These files contain the same data that
Instruments uses to calculate shader cost percentages.

Requirements:
  - Trace must have a .gpuprofiler_raw directory
  - This directory is created when capturing with Xcode Instruments profiling enabled

The timing data extracted from performance counters is the most accurate available,
as it comes directly from GPU hardware measurements.

Examples:
  # Extract timing from profiled trace
  gputrace timing-profiler trace.gputrace

  # Show detailed breakdown
  gputrace timing-profiler trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runTimingProfiler,
}

var timingProfilerVerbose bool

func init() {
	rootCmd.AddCommand(timingProfilerCmd)
	timingProfilerCmd.Flags().BoolVarP(&timingProfilerVerbose, "verbose", "v", false, "Show verbose output")
}

func runTimingProfiler(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}
	defer trace.Close()

	// Check if trace has profiler raw data
	if !trace.HasPerfCounters() {
		return fmt.Errorf(`trace does not have .gpuprofiler_raw directory

This trace was not captured with hardware performance counters enabled.

To capture traces with performance counters:
  1. Open the trace in Xcode Instruments
  2. Click the "Profile" button to enable hardware profiling
  3. Xcode will create a .gpuprofiler_raw directory with counter data

Alternatively, use one of the other timing extraction methods:
  - kdebug events (most accurate for live capture)
  - synthetic timing (for visualization only)`)
	}

	// Create profiler raw timing extractor
	extractor := gputrace.NewTimingExtractorProfilerRaw(trace)

	// Extract timing
	fmt.Println("Extracting timing from .gpuprofiler_raw files...")
	timings, err := extractor.ExtractTimingFromProfilerRaw()
	if err != nil {
		return fmt.Errorf("failed to extract timing: %w", err)
	}

	if len(timings) == 0 {
		return fmt.Errorf("no timing data found in performance counter files")
	}

	// Generate report
	report := extractor.ProfilerRawTimingReport(timings)
	fmt.Print(report)

	if timingProfilerVerbose {
		// Show additional details
		fmt.Println("\n=== Detailed Information ===")
		fmt.Printf("Data Source: %s.gpuprofiler_raw\n", tracePath)
		fmt.Printf("Encoders with timing: %d\n", len(timings))

		// Show per-encoder details
		fmt.Println("\nPer-Encoder Details:")
		for i, timing := range timings {
			fmt.Printf("  [%d] %s\n", i, timing.Label)
			fmt.Printf("      Duration: %.2f ms (%d ns)\n", timing.DurationMs, timing.DurationNs)
			fmt.Printf("      Percentage: %.1f%%\n", timing.Percentage)
		}
	}

	return nil
}
