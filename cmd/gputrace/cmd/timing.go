package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

var timingCmd = newTimingCommand(&timingOptions{
	table: true,
})

type timingOptions struct {
	json    string
	csv     string
	compare string
	table   bool
}

func newTimingCommand(opts *timingOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timing <trace.gputrace>",
		Short: "Extract and export GPU timing metrics from traces",
		Long: `Extract GPU timing metrics including per-kernel execution times,
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
  gputrace timing --json timing.json --csv timing.csv trace.gputrace

  # Compare two traces for regressions
  gputrace timing --compare baseline.gputrace current.gputrace

Timing source priority:
  - Profiled exports: .gpuprofiler_raw/streamData with APSTimelineData
    ReplayerGPUTime, command-buffer timestamps, and encoder/dispatch offsets
  - Capture fallback: kdebug/signpost-derived timing when present
  - Last resort: synthetic timing for visualization only

Capture fallbacks and synthetic timing are approximate. Hardware counter files
alone are not treated as direct shader timing unless correlated through a
supported timing source such as streamData/APSTimelineData.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTiming(cmd, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.json, "json", opts.json, "Export timing metrics to JSON file")
	cmd.Flags().StringVar(&opts.csv, "csv", opts.csv, "Export timing metrics to CSV file")
	cmd.Flags().StringVar(&opts.compare, "compare", opts.compare, "Compare with baseline trace for regression detection")
	cmd.Flags().BoolVar(&opts.table, "table", opts.table, "Show human-readable table output")
	return cmd
}

func init() {
	rootCmd.AddCommand(timingCmd)
}

func runTiming(cmd *cobra.Command, args []string, opts *timingOptions) error {
	tracePath := args[0]
	if err := validateTimingOutputPaths(opts); err != nil {
		return err
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Try to open full trace first
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		// Fall back to profiler-only mode if unsorted-capture is missing
		return runTimingFromProfiler(tracePath, opts)
	}

	// Extract timing metrics
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return fmt.Errorf("failed to extract timing metrics: %w", err)
	}

	// Show table if requested
	if opts.table {
		report := gputrace.FormatTimingMetrics(metrics)
		fmt.Fprintln(timingReportWriter(opts), report)
	}

	// Export JSON if requested
	if opts.json != "" {
		if err := writeTimingOutput(opts.json, "JSON", func(w io.Writer) error {
			return gputrace.ExportTimingMetricsJSON(w, metrics)
		}); err != nil {
			return err
		}
	}

	// Export CSV if requested
	if opts.csv != "" {
		if err := writeTimingOutput(opts.csv, "CSV", func(w io.Writer) error {
			return gputrace.ExportTimingMetricsCSV(w, metrics)
		}); err != nil {
			return err
		}
	}

	// Compare traces if requested
	if opts.compare != "" {
		if err := checkTraceFile(opts.compare); err != nil {
			return fmt.Errorf("baseline trace: %w", err)
		}

		baselineTrace, err := gputrace.Open(opts.compare)
		if err != nil {
			return fmt.Errorf("failed to open baseline trace: %w", err)
		}

		baselineExtractor := gputrace.NewTimingMetricsExtractor(baselineTrace)
		baselineMetrics, err := baselineExtractor.Extract()
		if err != nil {
			return fmt.Errorf("failed to extract baseline metrics: %w", err)
		}

		comparison := gputrace.CompareTraces(baselineMetrics, metrics)
		fmt.Fprintln(timingReportWriter(opts), "\n"+gputrace.FormatTimingComparison(comparison))

		if comparison.RegressionCount > 0 {
			// Return error to indicate regressions found
			return fmt.Errorf("found %d performance regressions", comparison.RegressionCount)
		}
	}

	return nil
}

// runTimingFromProfiler extracts timing from .gpuprofiler_raw when unsorted-capture is missing.
func runTimingFromProfiler(tracePath string, opts *timingOptions) error {
	if err := validateTimingOutputPaths(opts); err != nil {
		return err
	}

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
		fmt.Fprintf(os.Stderr, "Hint: To generate profiled timing data with streamData/APSTimelineData, run:\n")
		fmt.Fprintf(os.Stderr, "  gputrace xcode-profile run %s\n\n", tracePath)
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s (and unsorted-capture is missing)", tracePath)
	}

	// Parse streamData for timing info
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Convert to timing metrics
	metrics := convertStreamDataToTimingMetrics(tracePath, stats)

	// Show table if requested
	if opts.table {
		report := formatProfilerTimingMetrics(metrics)
		fmt.Fprintln(timingReportWriter(opts), report)
	}

	// Export JSON if requested
	if opts.json != "" {
		if err := writeTimingOutput(opts.json, "JSON", func(w io.Writer) error {
			return gputrace.ExportTimingMetricsJSON(w, metrics)
		}); err != nil {
			return err
		}
	}

	// Export CSV if requested
	if opts.csv != "" {
		if err := writeTimingOutput(opts.csv, "CSV", func(w io.Writer) error {
			return gputrace.ExportTimingMetricsCSV(w, metrics)
		}); err != nil {
			return err
		}
	}

	return nil
}

func writeTimingOutput(path, format string, write func(io.Writer) error) error {
	w, closeOutput, err := createCommandOutput(path)
	if err != nil {
		return fmt.Errorf("failed to create %s file: %w", format, err)
	}
	if err := write(w); err != nil {
		if closeOutput != nil {
			_ = closeOutput()
		}
		return fmt.Errorf("failed to write %s: %w", format, err)
	}
	if closeOutput != nil {
		if err := closeOutput(); err != nil {
			return fmt.Errorf("failed to close %s file: %w", format, err)
		}
	}
	fmt.Fprintf(os.Stderr, "Exported %s to %s\n", format, path)
	return nil
}

func timingReportWriter(opts *timingOptions) *os.File {
	if timingExportWritesStdout(opts.json) || timingExportWritesStdout(opts.csv) {
		return os.Stderr
	}
	return os.Stdout
}

func timingExportWritesStdout(path string) bool {
	return path != "" && commandOutputPathIsStdout(path)
}

func validateTimingOutputPaths(opts *timingOptions) error {
	stdoutExports := 0
	if timingExportWritesStdout(opts.json) {
		stdoutExports++
	}
	if timingExportWritesStdout(opts.csv) {
		stdoutExports++
	}
	if stdoutExports > 1 {
		return fmt.Errorf("only one timing export can write to stdout")
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
