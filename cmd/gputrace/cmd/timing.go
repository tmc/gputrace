package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

var (
	timingJSON        string
	timingCSV         string
	timingCompare     string
	timingTable       bool
	timingRequireReal bool
)

var timingCmd = &cobra.Command{
	Use:   "timing <trace.gputrace>",
	Short: "Extract and export comprehensive timing metrics from GPU traces",
	Long: `Extract comprehensive timing metrics including per-kernel execution times,
command buffer timings, and statistical analysis.

This command extracts timing data from GPU traces and provides:
  - Per-kernel execution statistics (min/max/avg/percentiles)
  - Command buffer and encoder timing
  - Memory transfer timing (when available)
  - Export formats: JSON, CSV, and human-readable tables
  - Trace comparison for regression detection

Examples:
  # Show timing table
  gputrace timing trace.gputrace

  # Export to JSON and CSV
  gputrace timing -json timing.json -csv timing.csv trace.gputrace

  # Compare two traces for regressions
  gputrace timing -compare baseline.gputrace current.gputrace

Note: Timing data depends on trace capture method. Use --require-real when the
      result must come from .gpuprofiler_raw/streamData rather than structural
      trace data or synthetic estimates.`,
	Args: cobra.ExactArgs(1),
	RunE: runTiming,
}

func init() {
	rootCmd.AddCommand(timingCmd)

	timingCmd.Flags().StringVar(&timingJSON, "json", "", "Export timing metrics to JSON file")
	timingCmd.Flags().StringVar(&timingCSV, "csv", "", "Export timing metrics to CSV file")
	timingCmd.Flags().StringVar(&timingCompare, "compare", "", "Compare with baseline trace for regression detection")
	timingCmd.Flags().BoolVar(&timingTable, "table", true, "Show human-readable table output")
	timingCmd.Flags().BoolVar(&timingRequireReal, "require-real", false, "Fail unless .gpuprofiler_raw/streamData is present")
}

func runTiming(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}
	if timingRequireReal {
		if err := requireUsableProfilerTiming(tracePath); err != nil {
			return err
		}
	}

	// Try to open full trace first
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		// Fall back to profiler-only mode if unsorted-capture is missing
		return runTimingFromProfiler(tracePath)
	}

	// Extract timing metrics
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return fmt.Errorf("failed to extract timing metrics: %w", err)
	}

	// Show table if requested
	if timingTable {
		report := gputrace.FormatTimingMetrics(metrics)
		fmt.Println(report)
	}

	// Export JSON if requested
	if timingJSON != "" {
		f, err := os.Create(timingJSON)
		if err != nil {
			return fmt.Errorf("failed to create JSON file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsJSON(f, metrics); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported JSON to %s\n", timingJSON)
	}

	// Export CSV if requested
	if timingCSV != "" {
		f, err := os.Create(timingCSV)
		if err != nil {
			return fmt.Errorf("failed to create CSV file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsCSV(f, metrics); err != nil {
			return fmt.Errorf("failed to write CSV: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported CSV to %s\n", timingCSV)
	}

	// Compare traces if requested
	if timingCompare != "" {
		if err := checkTraceFile(timingCompare); err != nil {
			return fmt.Errorf("baseline trace: %w", err)
		}

		baselineTrace, err := gputrace.Open(timingCompare)
		if err != nil {
			return fmt.Errorf("failed to open baseline trace: %w", err)
		}

		baselineExtractor := gputrace.NewTimingMetricsExtractor(baselineTrace)
		baselineMetrics, err := baselineExtractor.Extract()
		if err != nil {
			return fmt.Errorf("failed to extract baseline metrics: %w", err)
		}

		comparison := gputrace.CompareTraces(baselineMetrics, metrics)
		fmt.Println("\n" + gputrace.FormatTimingComparison(comparison))

		if comparison.RegressionCount > 0 {
			// Return error to indicate regressions found
			return fmt.Errorf("found %d performance regressions", comparison.RegressionCount)
		}
	}

	return nil
}

func requireUsableProfilerTiming(tracePath string) error {
	profilerDir := findProfilerDir(tracePath)
	if profilerDir == "" {
		return fmt.Errorf("real timing unavailable: no .gpuprofiler_raw directory found in %s", tracePath)
	}
	if _, err := os.Stat(filepath.Join(profilerDir, "streamData")); err != nil {
		return fmt.Errorf("real timing unavailable: no streamData in %s", profilerDir)
	}
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return fmt.Errorf("real timing unavailable: parse streamData: %w", err)
	}
	if len(stats.EncoderTimings) == 0 && len(stats.Dispatches) == 0 && stats.TotalTimeUs == 0 {
		return fmt.Errorf("real timing unavailable: streamData in %s contains no timing rows", profilerDir)
	}
	return nil
}

// runTimingFromProfiler extracts timing from .gpuprofiler_raw when unsorted-capture is missing.
func runTimingFromProfiler(tracePath string) error {
	// Find .gpuprofiler_raw directory
	profilerDir := ""

	// Check if it's directly a .gpuprofiler_raw directory
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		profilerDir = tracePath
	} else {
		// Look inside for .gpuprofiler_raw
		entries, err := os.ReadDir(tracePath)
		if err != nil {
			return fmt.Errorf("read directory: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
				profilerDir = filepath.Join(tracePath, e.Name())
				break
			}
		}
	}

	if profilerDir == "" {
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s (and unsorted-capture is missing)", tracePath)
	}

	// Parse streamData for timing info
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Convert to timing metrics
	metrics := convertStreamDataToTimingMetrics(tracePath, stats)

	// Show table if requested
	if timingTable {
		report := formatProfilerTimingMetrics(metrics)
		fmt.Println(report)
	}

	// Export JSON if requested
	if timingJSON != "" {
		f, err := os.Create(timingJSON)
		if err != nil {
			return fmt.Errorf("failed to create JSON file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsJSON(f, metrics); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported JSON to %s\n", timingJSON)
	}

	// Export CSV if requested
	if timingCSV != "" {
		f, err := os.Create(timingCSV)
		if err != nil {
			return fmt.Errorf("failed to create CSV file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsCSV(f, metrics); err != nil {
			return fmt.Errorf("failed to write CSV: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported CSV to %s\n", timingCSV)
	}

	return nil
}

// convertStreamDataToTimingMetrics converts StreamDataStats to TimingMetrics.
func convertStreamDataToTimingMetrics(tracePath string, stats *counter.StreamDataStats) *gputrace.TimingMetrics {
	cbCount := 0
	if stats.Timeline != nil {
		cbCount = len(stats.Timeline.CommandBufferTimestamps)
	}
	metrics := &gputrace.TimingMetrics{
		TracePath:            tracePath,
		TotalDuration:        time.Duration(stats.TotalTimeUs) * time.Microsecond,
		TotalEncoders:        len(stats.EncoderTimings),
		TotalCommandBuffers:  cbCount,
		KernelTimings:        make([]*gputrace.KernelTiming, 0),
		EncoderTimings:       make([]*gputrace.EncoderTiming, 0),
		CommandBufferTimings: make([]*gputrace.CommandBufferTiming, 0),
	}

	// Convert encoder timings
	for _, et := range stats.EncoderTimings {
		label := et.Label
		if label == "" {
			label = fmt.Sprintf("encoder_%d", et.Index)
		}
		metrics.EncoderTimings = append(metrics.EncoderTimings, &gputrace.EncoderTiming{
			Label:      label,
			DurationNs: uint64(et.DurationMicros) * 1000,
			DurationMs: float64(et.DurationMicros) / 1000.0,
		})
	}

	// Aggregate by function name
	kernelMap := make(map[string]*gputrace.KernelTiming)
	for _, d := range stats.Dispatches {
		name := d.FunctionName
		if name == "" {
			name = fmt.Sprintf("(pipeline_%d)", d.PipelineIndex)
		}
		duration := time.Duration(d.DurationUs) * time.Microsecond

		kt, exists := kernelMap[name]
		if !exists {
			kt = &gputrace.KernelTiming{
				Name:        name,
				MinDuration: duration,
				MaxDuration: duration,
			}
			kernelMap[name] = kt
		}

		kt.InvocationCount++
		kt.TotalDuration += duration

		if duration < kt.MinDuration {
			kt.MinDuration = duration
		}
		if duration > kt.MaxDuration {
			kt.MaxDuration = duration
		}
	}

	// Calculate averages and percentages
	var totalDuration time.Duration
	for _, kt := range kernelMap {
		if kt.InvocationCount > 0 {
			kt.AvgDuration = kt.TotalDuration / time.Duration(kt.InvocationCount)
		}
		totalDuration += kt.TotalDuration
		metrics.KernelTimings = append(metrics.KernelTimings, kt)
	}

	for _, kt := range metrics.KernelTimings {
		if totalDuration > 0 {
			kt.PercentOfTotal = float64(kt.TotalDuration) / float64(totalDuration) * 100.0
		}
	}

	// Sort by total duration descending
	sort.Slice(metrics.KernelTimings, func(i, j int) bool {
		return metrics.KernelTimings[i].TotalDuration > metrics.KernelTimings[j].TotalDuration
	})

	return metrics
}

// formatProfilerTimingMetrics formats timing metrics from profiler data.
func formatProfilerTimingMetrics(metrics *gputrace.TimingMetrics) string {
	var out string

	// Summary line
	out += fmt.Sprintf("%d %s, %d %s (%s)\n\n",
		metrics.TotalEncoders, Pluralize(metrics.TotalEncoders, "encoder", "encoders"),
		len(metrics.KernelTimings), Pluralize(len(metrics.KernelTimings), "kernel", "kernels"),
		FormatDuration(int(metrics.TotalDuration.Microseconds())))

	out += Colorize("Top Kernels by Time", ColorBold) + "\n"
	out += TableSeparator(80) + "\n"
	out += fmt.Sprintf("%-50s %8s %10s %10s %10s %8s\n",
		"Kernel Name", "Invokes", "Total(us)", "Avg(us)", "Max(us)", "Cost")
	out += TableSeparator(100) + "\n"

	for _, kt := range metrics.KernelTimings {
		name := kt.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		out += fmt.Sprintf("%-50s %8s %10s %10s %10s %7s\n",
			name,
			FormatCount(kt.InvocationCount),
			FormatCount(int(kt.TotalDuration.Microseconds())),
			FormatCount(int(kt.AvgDuration.Microseconds())),
			FormatCount(int(kt.MaxDuration.Microseconds())),
			FormatPercent(kt.PercentOfTotal))
	}

	return out
}
