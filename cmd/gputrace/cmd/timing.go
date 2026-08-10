package cmd

import (
	"fmt"
	"io"
	"os"
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
	json        string
	csv         string
	compare     string
	table       bool
	minCalls    int
	benchfmt    bool
	benchConfig benchfmtConfigFlags
}

func newTimingCommand(opts *timingOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timing <trace.gputrace>",
		Short: "Summarize measured or approximate GPU timing",
		Long: `Summarize GPU timing including per-function dispatch spans,
command buffer timings, and statistical analysis.

This command extracts timing data from GPU traces and provides:
  - Per-function dispatch-span statistics (min/max/avg)
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
	cmd.Flags().IntVar(&opts.minCalls, "min-calls", opts.minCalls, "Only table rows for functions dispatched at least N times (off by default; reports what it drops; JSON and CSV are never filtered)")
	addBenchfmtFlags(cmd, &opts.benchfmt, &opts.benchConfig)
	return cmd
}

func init() {
	rootCmd.AddCommand(timingCmd)
}

// tableMetrics applies --min-calls to a copy of metrics and returns the note
// naming what it dropped. The original is left alone: JSON and CSV exports
// carry every row regardless of the flag, because a filtered export is a
// partial file that reads as a complete one long after the flag is forgotten.
func tableMetrics(metrics *gputrace.TimingMetrics, minCalls int) (*gputrace.TimingMetrics, string) {
	kept, dropped := gputrace.FilterMinCalls(metrics.KernelTimings, minCalls)
	if dropped == 0 {
		return metrics, ""
	}
	filtered := *metrics
	filtered.KernelTimings = kept
	return &filtered, gputrace.MinCallsNote(minCalls, dropped, len(metrics.KernelTimings))
}

func runTiming(cmd *cobra.Command, args []string, opts *timingOptions) error {
	tracePath := args[0]
	if opts.minCalls < 0 {
		return fmt.Errorf("--min-calls must be >= 0")
	}
	if err := validateBenchfmtFlags(opts.benchfmt, opts.benchConfig); err != nil {
		return err
	}
	if opts.benchfmt && (opts.json != "" || opts.csv != "" || opts.compare != "") {
		return fmt.Errorf("--benchfmt cannot be combined with --json, --csv, or --compare")
	}
	if err := validateTimingOutputPaths(opts); err != nil {
		return err
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}
	if opts.benchfmt {
		_, stats, err := loadProfilerStats(tracePath)
		if err != nil {
			return fmt.Errorf("benchfmt requires measured profiler streamData: %w", err)
		}
		return writeProfilerBenchfmt(cmd.OutOrStdout(), tracePath, stats, nil, opts.benchConfig)
	}

	// A profiled bundle may also contain unsorted-capture. Prefer streamData:
	// its dispatch and command-buffer records are more specific than the
	// capture fallback. Keep comparison on the common extractor until it can
	// compare profiler records on both sides.
	if opts.compare == "" && findProfilerDir(tracePath) != "" {
		return runTimingFromProfiler(tracePath, opts)
	}

	// Try to open full trace first
	trace, err := gputrace.Open(tracePath)
	if err != nil || trace.ProfilerOnly {
		// Fall back to profiler-only mode when there is no capture stream.
		// Open now succeeds on such bundles, so the flag, not the error, is
		// what distinguishes them.
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
		shown, note := tableMetrics(metrics, opts.minCalls)
		fmt.Fprintln(timingReportWriter(opts), gputrace.FormatTimingMetrics(shown)+note)
	}
	if metrics.TimingSource == gputrace.TimingSourceUnavailable {
		fmt.Fprint(os.Stderr, profileReplayHint(tracePath))
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

	profilerDir := findProfilerDir(tracePath)

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
		shown, note := tableMetrics(metrics, opts.minCalls)
		fmt.Fprintln(timingReportWriter(opts), formatProfilerTimingMetrics(shown)+note)
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
		TotalDuration:        time.Duration(stats.TotalDispatchTimeUs) * time.Microsecond,
		TotalEncoders:        len(stats.EncoderTimings),
		TotalCommandBuffers:  cbCount,
		TimingSource:         "profiler",
		TimingApproximate:    false,
		KernelTimings:        make([]*gputrace.KernelTiming, 0),
		EncoderTimings:       make([]*gputrace.EncoderTiming, 0),
		CommandBufferTimings: make([]*gputrace.CommandBufferTiming, 0),
	}
	if metrics.TotalDuration == 0 {
		metrics.TotalDuration = time.Duration(stats.TotalEncoderTimeUs) * time.Microsecond
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
		if metrics.KernelTimings[i].TotalDuration != metrics.KernelTimings[j].TotalDuration {
			return metrics.KernelTimings[i].TotalDuration > metrics.KernelTimings[j].TotalDuration
		}
		return metrics.KernelTimings[i].Name < metrics.KernelTimings[j].Name
	})

	if stats.Timeline != nil {
		for _, cb := range stats.Timeline.CommandBufferTimestamps {
			metrics.CommandBufferTimings = append(metrics.CommandBufferTimings, &gputrace.CommandBufferTiming{
				Index:    cb.Index,
				Label:    "(profiler command buffer)",
				Duration: time.Duration(cb.DurationNs(stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom)),
			})
		}
	}

	return metrics
}

// formatProfilerTimingMetrics formats timing metrics from profiler data.
func formatProfilerTimingMetrics(metrics *gputrace.TimingMetrics) string {
	var out string

	// Summary line
	out += fmt.Sprintf("Trace: %s\n", metrics.TracePath)
	out += "Source: profiler streamData (measured cumulative dispatch offsets)\n"
	out += fmt.Sprintf("Dispatch span: %s\n", FormatDuration(int(metrics.TotalDuration.Microseconds())))
	out += fmt.Sprintf("%d %s, %d timed %s, %d command %s\n",
		metrics.TotalEncoders, Pluralize(metrics.TotalEncoders, "encoder", "encoders"),
		len(metrics.KernelTimings), Pluralize(len(metrics.KernelTimings), "function", "functions"),
		metrics.TotalCommandBuffers, Pluralize(metrics.TotalCommandBuffers, "buffer", "buffers"))
	out += "Note: per-dispatch spans come from cumulative offsets and may include boundary or gap time.\n\n"

	out += Colorize("Functions by Dispatch Span", ColorBold) + "\n"
	out += TableSeparator(80) + "\n"
	out += fmt.Sprintf("%-50s %8s %10s %10s %10s %8s\n",
		"Function", "Dispatches", "Span(us)", "Avg(us)", "Max(us)", "Span Share")
	out += TableSeparator(100) + "\n"

	for _, kt := range metrics.KernelTimings {
		name := kt.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		marker := ""
		if kt.IsLowSample() {
			marker = gputrace.LowSampleMarker
		}
		out += fmt.Sprintf("%-50s %8s %10s %10s %10s %7s%s\n",
			name,
			FormatCount(kt.InvocationCount),
			FormatCount(int(kt.TotalDuration.Microseconds())),
			FormatCount(int(kt.AvgDuration.Microseconds())),
			FormatCount(int(kt.MaxDuration.Microseconds())),
			FormatPercent(kt.PercentOfTotal),
			marker)
	}
	out += gputrace.LowSampleFootnote(metrics.KernelTimings)

	return out
}
