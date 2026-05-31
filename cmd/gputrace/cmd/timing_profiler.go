package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var timingProfilerJSON bool

var timingProfilerCmd = &cobra.Command{
	Use:    "timing-profiler <trace.gputrace>",
	Short:  "Inspect legacy .gpuprofiler_raw timing fallbacks",
	Hidden: true,
	Long: `Inspect legacy GPU timing fallbacks for profiled traces.

Prefer "gputrace timing" for current timing output. The primary supported
profiled timing source is .gpuprofiler_raw/streamData, including APSTimelineData
ReplayerGPUTime, command-buffer timestamps, and encoder/dispatch offsets.

This hidden command uses older fallback paths:
  - kdebug GPU execution events when present
  - counter-file limiter heuristics when kdebug timing is unavailable

Counter files alone are not direct shader timing; limiter-based values are
approximate and should be treated as relative visualization data.

Requirements:
  - Trace must have a .gpuprofiler_raw directory
  - Use "gputrace timing" to read streamData/APSTimelineData timing when present

Examples:
  # Inspect legacy fallback timing for a profiled trace
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
	timingProfilerCmd.Flags().BoolVar(&timingProfilerJSON, "json", false, "Output in JSON format")
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

This trace does not include an exported profiler data directory.

To capture traces with streamData/APSTimelineData timing:
  1. Open the trace in Xcode Instruments
  2. Click the "Profile" button
  3. Export/save the profiled trace with its .gpuprofiler_raw directory

Alternatively, use one of the other timing extraction methods:
  - kdebug/signpost events when present (approximate capture fallback)
  - synthetic timing (approximate visualization fallback)`)
	}

	// Create profiler raw timing extractor
	extractor := gputrace.NewTimingExtractorProfilerRaw(trace)

	// Extract timing
	fmt.Fprintln(os.Stderr, "Inspecting legacy .gpuprofiler_raw timing fallbacks...")
	timings, err := extractor.ExtractTimingFromProfilerRaw()
	if err != nil {
		return fmt.Errorf("failed to extract timing: %w", err)
	}

	if len(timings) == 0 {
		return fmt.Errorf("no timing data found in .gpuprofiler_raw fallback sources")
	}

	if timingProfilerJSON {
		data, err := json.MarshalIndent(timings, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		fmt.Println(string(data))
		return nil
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
