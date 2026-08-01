package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/tracebundle"
)

var statsCmd = newStatsCommand(new(statsOptions))

type statsOptions struct {
	verbose     bool
	json        bool
	limit       int
	all         bool
	benchfmt    bool
	benchConfig benchfmtConfigFlags
}

func newStatsCommand(opts *statsOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats <trace.gputrace>",
		Short: "Display GPU trace statistics",
		Long: `Display comprehensive statistics about a GPU trace file.

This command extracts and displays information including:
  - Trace metadata (UUID, version, API, device)
  - Encoder labels and kernel names
  - Buffer information
  - Command queue details
  - Timing data (if available)

Examples:
  gputrace stats trace.gputrace
  gputrace stats trace.gputrace -v
  gputrace stats trace.gputrace --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Show verbose statistics including detailed analysis")
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output statistics in JSON format")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Maximum rows in each verbose list")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Show every row in verbose lists")
	addBenchfmtFlags(cmd, &opts.benchfmt, &opts.benchConfig)
	return cmd
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func runStats(cmd *cobra.Command, args []string, opts *statsOptions) error {
	tracePath := args[0]
	if err := validateBenchfmtFlags(opts.benchfmt, opts.benchConfig); err != nil {
		return err
	}
	if opts.benchfmt && opts.json {
		return fmt.Errorf("--benchfmt and --json are mutually exclusive")
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}
	if opts.benchfmt {
		if findProfilerDir(tracePath) != "" {
			_, streamStats, err := loadProfilerStats(tracePath)
			if err != nil {
				return fmt.Errorf("parse streamData: %w", err)
			}
			return writeProfilerBenchfmt(cmd.OutOrStdout(), tracePath, streamStats, nil, opts.benchConfig)
		}
	}
	payload, payloadErr := tracebundle.InspectPayload(tracePath)

	// Open trace
	// Open now succeeds on a bundle with no capture stream, so ProfilerOnly,
	// not the error, is what routes to the profiler-backed report.
	trace, err := gputrace.Open(tracePath)
	if err != nil || trace.ProfilerOnly {
		if findProfilerDir(tracePath) != "" {
			return runStatsFromProfiler(cmd.OutOrStdout(), tracePath, opts)
		}
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Extract and display statistics
	statistics, err := gputrace.ExtractStatistics(trace)
	if err != nil {
		return fmt.Errorf("failed to extract statistics: %w", err)
	}
	if opts.benchfmt {
		config, err := mergeBenchfmtConfig(benchfmtDefaults(tracePath, ""), opts.benchConfig)
		if err != nil {
			return err
		}
		return writeBenchfmt(cmd.OutOrStdout(), benchfmtRecord{
			Config: config,
			Values: benchfmtStructuralValues(statistics),
		})
	}

	// Handle JSON output
	if opts.json {
		return outputStatsJSON(cmd.OutOrStdout(), statistics, trace, opts.verbose)
	}

	// Quick one-liner summary
	parts := []string{
		fmt.Sprintf("%d %s", statistics.DispatchCalls, Pluralize(statistics.DispatchCalls, "dispatch", "dispatches")),
		fmt.Sprintf("%d observed kernel %s", statistics.ObservedKernelLabels, Pluralize(statistics.ObservedKernelLabels, "label", "labels")),
	}
	if statistics.ComputeEncodersAvailable {
		parts = append([]string{
			fmt.Sprintf("%d %s", statistics.ComputeEncoders, Pluralize(statistics.ComputeEncoders, "encoder", "encoders")),
		}, parts...)
	}
	if statistics.BufferUsageGB >= 0.001 {
		parts = append(parts, FormatBytes(statistics.BufferUsageBytes))
	}
	fmt.Println(strings.Join(parts, ", "))
	fmt.Println()

	// Trace Info Section
	fmt.Println(Colorize("Trace Info", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Path: %s\n", tracePath)
	if payloadErr == nil {
		fmt.Printf("  Raw Payload: %s\n", formatPayloadCompleteness(payload))
	}
	if trace.Metadata != nil {
		fmt.Printf("  UUID: %s\n", trace.Metadata.UUID)
		apiName := "Metal"
		if trace.Metadata.GraphicsAPI == 1 {
			apiName = "Metal (Compute)"
		}
		fmt.Printf("  API:  %s\n", apiName)
	}
	fmt.Println()

	// Workload Section
	fmt.Println(Colorize("Workload", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Command Buffers:  %s\n", FormatCount(statistics.CommandBuffers))
	if statistics.ComputeEncodersAvailable {
		fmt.Printf("  Compute Encoders: %s\n", FormatCount(statistics.ComputeEncoders))
	} else {
		fmt.Printf("  Compute Encoders: (unavailable)\n")
	}
	fmt.Printf("  Encoder Count Source: %s\n", statistics.ComputeEncodersSource)
	fmt.Printf("  Dispatch Calls:   %s\n", FormatCount(statistics.DispatchCalls))
	fmt.Printf("  Observed Kernel Labels: %s\n", FormatCount(statistics.ObservedKernelLabels))
	fmt.Printf("  Discovered Functions:  %s\n", FormatCount(statistics.DiscoveredFunctions))
	fmt.Println()

	// Memory Section
	fmt.Println(Colorize("Memory", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Buffer Usage:     %s (%s)\n", FormatBytes(statistics.BufferUsageBytes), FormatSummaryLine(statistics.UniqueBuffers, "buffer", "buffers", ""))
	if statistics.HeapUsageBytes > 0 {
		fmt.Printf("  Heap Usage:       %s (%s)\n", FormatBytes(statistics.HeapUsageBytes), FormatSummaryLine(statistics.UniqueHeaps, "heap", "heaps", ""))
	}
	if statistics.UnusedMemoryBytes > 0 {
		fmt.Printf("  Unused Memory:    %s\n", FormatBytes(statistics.UnusedMemoryBytes))
	}
	fmt.Println()

	// Try to get timing information
	gpuTimeUs := 0
	hasProfilerData := false

	// Check for profiler data
	profilerDir := findProfilerDir(tracePath)
	if profilerDir != "" {
		hasProfilerData = true
		if streamStats, err := counter.ParseStreamData(profilerDir, nil); err == nil {
			gpuTimeUs = streamStats.TotalTimeUs
		}
	}

	// Timing Section
	fmt.Println(Colorize("Timing", ColorBold))
	fmt.Println(TableSeparator(40))
	if gpuTimeUs > 0 {
		fmt.Printf("  Encoder Span:     %s\n", FormatDuration(gpuTimeUs))
	} else {
		fmt.Printf("  Encoder Span:     (no profiler data)\n")
	}
	if hasProfilerData && profilerDir != "" {
		if streamStats, err := counter.ParseStreamData(profilerDir, nil); err == nil {
			if streamStats.EffectiveGPUTimeNs != nil {
				fmt.Printf("  Effective GPU:    %s\n", FormatDurationNs(*streamStats.EffectiveGPUTimeNs))
			} else {
				fmt.Printf("  Effective GPU:    (not present in streamData)\n")
			}
			if streamStats.CommandBufferActiveNs > 0 {
				fmt.Printf("  CB Active Time:   %s\n", FormatDurationNs(streamStats.CommandBufferActiveNs))
			}
			if streamStats.CommandBufferWallNs > 0 {
				fmt.Printf("  CB Wall Time:     %s\n", FormatDurationNs(streamStats.CommandBufferWallNs))
			}
			if streamStats.TimingSource != "" {
				fmt.Printf("  Timing Source:    %s\n", streamStats.TimingSource)
			}
		}
	}
	fmt.Printf("  Profiler Data:    %s\n", formatBool(hasProfilerData))
	fmt.Println()

	// Top Kernels Section (if we have data)
	if hasProfilerData && profilerDir != "" {
		if streamStats, err := counter.ParseStreamData(profilerDir, nil); err == nil && len(streamStats.Dispatches) > 0 {
			fmt.Println(Colorize("Functions by Dispatch Span", ColorBold))
			fmt.Println(TableSeparator(40))
			fmt.Println("  Cumulative offsets may include boundary or gap time.")

			// Aggregate by function name
			funcTotals := make(map[string]int)
			funcCounts := make(map[string]int)
			for _, d := range streamStats.Dispatches {
				name := d.FunctionName
				if name == "" {
					name = fmt.Sprintf("pipeline_%d", d.PipelineIndex)
				}
				funcTotals[name] += d.DurationUs
				funcCounts[name]++
			}

			// Sort by time
			type funcStat struct {
				name  string
				time  int
				count int
			}
			var sorted []funcStat
			for name, time := range funcTotals {
				sorted = append(sorted, funcStat{name, time, funcCounts[name]})
			}
			sort.Slice(sorted, func(i, j int) bool {
				if sorted[i].time != sorted[j].time {
					return sorted[i].time > sorted[j].time
				}
				return sorted[i].name < sorted[j].name
			})

			// Show top 5
			// Use sum of dispatch times for percentage (more accurate than encoder total)
			var totalDispatchTime int
			for _, fs := range sorted {
				totalDispatchTime += fs.time
			}
			if totalDispatchTime == 0 {
				totalDispatchTime = gpuTimeUs
			}

			for i, fs := range sorted {
				if i >= 5 {
					break
				}
				pct := 0.0
				if totalDispatchTime > 0 {
					pct = float64(fs.time) / float64(totalDispatchTime) * 100
				}
				name := fs.name
				if len(name) > 35 {
					name = name[:32] + "..."
				}
				fmt.Printf("  %5.1f%%  %-35s  (%dx)\n", pct, name, fs.count)
			}
			if len(sorted) > 5 {
				fmt.Printf("  ...and %d more functions\n", len(sorted)-5)
			}
			fmt.Println()
		}
	}

	// Record Types (condensed)
	fmt.Println(Colorize("MTSP Records", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Total Records:    %s\n", FormatCount(statistics.TotalRecords))

	// Sort record types by count
	type recordStat struct {
		name  string
		count int
	}
	var recordStats []recordStat
	for k, v := range statistics.RecordTypes {
		recordStats = append(recordStats, recordStat{k, v})
	}
	sort.Slice(recordStats, func(i, j int) bool {
		return recordStats[i].count > recordStats[j].count
	})

	// Show all record types with descriptions
	fmt.Printf("  Types:\n")
	for _, rs := range recordStats {
		desc := mtspRecordDescription(rs.name)
		fmt.Printf("    %-12s %5d  %s\n", rs.name, rs.count, desc)
	}
	fmt.Println()

	// If verbose, show additional analysis
	if opts.verbose {
		fmt.Println()
		fmt.Println(Colorize("Detailed Analysis", ColorBold))
		fmt.Println(TableSeparator(40))

		// Show metadata details
		if trace.Metadata != nil {
			fmt.Println(Colorize("Metadata Details:", ColorGreen))
			fmt.Printf("  UUID: %s\n", trace.Metadata.UUID)
			fmt.Printf("  Capture Version: %d\n", trace.Metadata.CaptureVersion)
			fmt.Printf("  Graphics API: %d\n", trace.Metadata.GraphicsAPI)
			fmt.Printf("  Device ID: %d\n", trace.Metadata.DeviceID)
			fmt.Println()
		}

		// Show all encoder labels
		if len(trace.EncoderLabels) > 0 {
			fmt.Printf("%s (%d):\n", Colorize("All Encoder Labels", ColorGreen), len(trace.EncoderLabels))
			for i, label := range limitedStrings(trace.EncoderLabels, opts.limit, opts.all) {
				fmt.Printf("  [%d] %s\n", i, label)
			}
			printOmittedRows(len(trace.EncoderLabels), opts.limit, opts.all)
			fmt.Println()
		}

		// Show all kernel names
		if len(trace.KernelNames) > 0 {
			fmt.Printf("%s (%d discovered; not necessarily dispatched):\n", Colorize("Library Functions", ColorGreen), len(trace.KernelNames))
			for i, name := range limitedStrings(trace.KernelNames, opts.limit, opts.all) {
				fmt.Printf("  [%d] %s\n", i, name)
			}
			printOmittedRows(len(trace.KernelNames), opts.limit, opts.all)
			fmt.Println()
		}

		// Show buffer labels
		if len(trace.BufferLabels) > 0 {
			fmt.Printf("%s (%d):\n", Colorize("All Buffer Labels", ColorGreen), len(trace.BufferLabels))
			for i, label := range limitedStrings(trace.BufferLabels, opts.limit, opts.all) {
				fmt.Printf("  [%d] %s\n", i, label)
			}
			printOmittedRows(len(trace.BufferLabels), opts.limit, opts.all)
			fmt.Println()
		}

		// Show command queue label
		if trace.CommandQueueLabel != "" {
			fmt.Printf("%s: %s\n\n", Colorize("Command Queue Label", ColorGreen), trace.CommandQueueLabel)
		}

		// Try to extract timing data
		timings, err := gputrace.ExtractTimingData(trace)
		if err == nil && len(timings) > 0 {
			fmt.Printf("%s (%d samples):\n", Colorize("Timing Data", ColorGreen), len(timings))
			for _, timing := range timings {
				fmt.Printf("  %s:\n", Colorize(timing.Label, ColorYellow))
				fmt.Printf("    Start: %d (0x%x)\n", timing.StartTimestamp, timing.StartTimestamp)
				fmt.Printf("    End:   %d (0x%x)\n", timing.EndTimestamp, timing.EndTimestamp)
				fmt.Printf("    Duration: %.2f ms\n", timing.DurationMs)
			}
			fmt.Println()
		}
	}

	return nil
}

func limitedStrings(values []string, limit int, all bool) []string {
	if all || limit < 0 || len(values) <= limit {
		return values
	}
	if limit == 0 {
		return nil
	}
	return values[:limit]
}

func printOmittedRows(total, limit int, all bool) {
	if all || limit < 0 || total <= limit {
		return
	}
	shown := limit
	if shown < 0 {
		shown = 0
	}
	fmt.Printf("  ... %d more; use --all to show every row\n", total-shown)
}

type profilerStatsJSONOutput struct {
	ProfilerOnly bool              `json:"profiler_only"`
	ProfilerDir  string            `json:"profiler_dir"`
	Statistics   profilerStatsJSON `json:"statistics"`
}

type profilerStatsJSON struct {
	CommandBuffers        int     `json:"command_buffers"`
	ComputeEncoders       int     `json:"compute_encoders"`
	DispatchCalls         int     `json:"dispatch_calls"`
	UniquePipelines       int     `json:"unique_pipelines"`
	TotalGPUTimeUs        int     `json:"total_gpu_time_us"` // Backward-compatible alias for TotalEncoderTimeUs.
	TotalEncoderTimeUs    int     `json:"total_encoder_time_us"`
	TotalDispatchTimeUs   int     `json:"total_dispatch_time_us"`
	EffectiveGPUTimeNs    *uint64 `json:"effective_gpu_time_ns,omitempty"`
	CommandBufferActiveNs uint64  `json:"command_buffer_active_time_ns,omitempty"`
	CommandBufferWallNs   uint64  `json:"command_buffer_wall_time_ns,omitempty"`
	TimingSource          string  `json:"timing_source"`
}

func runStatsFromProfiler(w io.Writer, tracePath string, opts *statsOptions) error {
	profilerDir, streamStats, err := loadProfilerStats(tracePath)
	if err != nil {
		return err
	}
	if opts.benchfmt {
		return writeProfilerBenchfmt(w, tracePath, streamStats, nil, opts.benchConfig)
	}

	commandBuffers := 0
	if streamStats.Timeline != nil {
		commandBuffers = len(streamStats.Timeline.CommandBufferTimestamps)
	}
	stats := profilerStatsJSON{
		CommandBuffers:        commandBuffers,
		ComputeEncoders:       streamStats.NumEncoders,
		DispatchCalls:         streamStats.NumGPUCommands,
		UniquePipelines:       streamStats.NumPipelines,
		TotalGPUTimeUs:        streamStats.TotalEncoderTimeUs,
		TotalEncoderTimeUs:    streamStats.TotalEncoderTimeUs,
		TotalDispatchTimeUs:   streamStats.TotalDispatchTimeUs,
		EffectiveGPUTimeNs:    streamStats.EffectiveGPUTimeNs,
		CommandBufferActiveNs: streamStats.CommandBufferActiveNs,
		CommandBufferWallNs:   streamStats.CommandBufferWallNs,
		TimingSource:          streamStats.TimingSource,
	}

	if opts.json {
		output := profilerStatsJSONOutput{
			ProfilerOnly: true,
			ProfilerDir:  profilerDir,
			Statistics:   stats,
		}
		return writeStatsJSON(w, output)
	}

	parts := []string{
		fmt.Sprintf("%d %s", stats.ComputeEncoders, Pluralize(stats.ComputeEncoders, "encoder", "encoders")),
		fmt.Sprintf("%d %s", stats.DispatchCalls, Pluralize(stats.DispatchCalls, "dispatch", "dispatches")),
		fmt.Sprintf("%d %s", stats.UniquePipelines, Pluralize(stats.UniquePipelines, "pipeline", "pipelines")),
	}
	fmt.Println(strings.Join(parts, ", "))
	fmt.Println()

	fmt.Println(Colorize("Profiler-Only Trace", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Path:          %s\n", tracePath)
	fmt.Printf("  Profiler Data: %s\n", profilerDir)
	if payload, err := tracebundle.InspectPayload(tracePath); err == nil {
		fmt.Printf("  Raw Payload:   %s\n", formatPayloadCompleteness(payload))
	}
	fmt.Printf("  Note:          aggregate profiler timing is available; structural and threadgroup analysis requires a full raw payload\n")
	fmt.Println()

	fmt.Println(Colorize("Workload", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Command Buffers:  %s\n", FormatCount(stats.CommandBuffers))
	fmt.Printf("  Compute Encoders: %s\n", FormatCount(stats.ComputeEncoders))
	fmt.Printf("  Dispatch Calls:   %s\n", FormatCount(stats.DispatchCalls))
	fmt.Printf("  Unique Pipelines: %s\n", FormatCount(stats.UniquePipelines))
	fmt.Println()

	fmt.Println(Colorize("Timing", ColorBold))
	fmt.Println(TableSeparator(40))
	if stats.TotalEncoderTimeUs > 0 {
		fmt.Printf("  Encoder Span:     %s\n", FormatDuration(stats.TotalEncoderTimeUs))
		fmt.Printf("  Dispatch Span:    %s\n", FormatDuration(stats.TotalDispatchTimeUs))
		if stats.EffectiveGPUTimeNs != nil {
			fmt.Printf("  Effective GPU:    %s\n", FormatDurationNs(*stats.EffectiveGPUTimeNs))
		} else {
			fmt.Printf("  Effective GPU:    (not present in streamData)\n")
		}
		if stats.CommandBufferActiveNs > 0 {
			fmt.Printf("  CB Active Time:   %s\n", FormatDurationNs(stats.CommandBufferActiveNs))
		}
		if stats.CommandBufferWallNs > 0 {
			fmt.Printf("  CB Wall Time:     %s\n", FormatDurationNs(stats.CommandBufferWallNs))
		}
		if stats.TimingSource != "" {
			fmt.Printf("  Timing Source:    %s\n", stats.TimingSource)
		}
	} else {
		fmt.Printf("  Timing:           (not available)\n")
	}
	fmt.Println()
	return nil
}

func formatPayloadCompleteness(payload tracebundle.Payload) string {
	switch payload.Class {
	case tracebundle.PayloadFull:
		return "full (capture and raw resources present)"
	case tracebundle.PayloadProfilerOnly:
		return "profiler-only (aggregate timing available; structural/threadgroup data unavailable)"
	default:
		return "incomplete (structural/threadgroup data unavailable)"
	}
}

// StatsJSONOutput represents the JSON output structure for stats command.
type StatsJSONOutput struct {
	Statistics *StatsJSON    `json:"statistics"`
	Metadata   *MetadataJSON `json:"metadata,omitempty"`
	Verbose    *VerboseJSON  `json:"verbose,omitempty"`
}

// StatsJSON represents statistics in JSON format.
type StatsJSON struct {
	BufferUsageBytes         uint64         `json:"buffer_usage_bytes"`
	BufferUsageGB            float64        `json:"buffer_usage_gb"`
	BufferSizeSum            uint64         `json:"buffer_size_sum"`
	UniqueBuffers            int            `json:"unique_buffers"`
	HeapUsageBytes           uint64         `json:"heap_usage_bytes"`
	HeapUsageMB              float64        `json:"heap_usage_mb"`
	UniqueHeaps              int            `json:"unique_heaps"`
	UnusedBuffers            int            `json:"unused_buffers,omitempty"`
	UnusedTextures           int            `json:"unused_textures,omitempty"`
	UnusedFunctions          int            `json:"unused_functions,omitempty"`
	UniqueKernels            int            `json:"unique_kernels"`
	ObservedKernelLabels     int            `json:"observed_kernel_labels"`
	DiscoveredFunctions      int            `json:"discovered_functions"`
	CommandBuffers           int            `json:"command_buffers"`
	ComputeEncoders          *int           `json:"compute_encoders"`
	ComputeEncodersAvailable bool           `json:"compute_encoders_available"`
	ComputeEncodersSource    string         `json:"compute_encoders_source"`
	DispatchCalls            int            `json:"dispatch_calls"`
	TotalRecords             int            `json:"total_records"`
	RecordTypes              map[string]int `json:"record_types"`
	MTLBLibraries            int            `json:"mtlb_libraries"`
}

// MetadataJSON represents trace metadata in JSON format.
type MetadataJSON struct {
	UUID           string `json:"uuid"`
	CaptureVersion int    `json:"capture_version"`
	GraphicsAPI    int    `json:"graphics_api"`
	DeviceID       int    `json:"device_id"`
}

// VerboseJSON represents verbose output in JSON format.
type VerboseJSON struct {
	EncoderLabels     []string     `json:"encoder_labels,omitempty"`
	KernelNames       []string     `json:"kernel_names,omitempty"`
	BufferLabels      []string     `json:"buffer_labels,omitempty"`
	CommandQueueLabel string       `json:"command_queue_label,omitempty"`
	TimingData        []TimingJSON `json:"timing_data,omitempty"`
}

// TimingJSON represents timing data in JSON format.
type TimingJSON struct {
	Label          string  `json:"label"`
	StartTimestamp uint64  `json:"start_timestamp"`
	EndTimestamp   uint64  `json:"end_timestamp"`
	DurationMs     float64 `json:"duration_ms"`
}

// outputStatsJSON outputs statistics in JSON format.
func outputStatsJSON(w io.Writer, stats *gputrace.TraceStatistics, trace *gputrace.Trace, verbose bool) error {
	s := &StatsJSON{
		BufferUsageBytes:         stats.BufferUsageBytes,
		BufferUsageGB:            stats.BufferUsageGB,
		BufferSizeSum:            stats.BufferSizeSum,
		UniqueBuffers:            stats.UniqueBuffers,
		HeapUsageBytes:           stats.HeapUsageBytes,
		HeapUsageMB:              stats.HeapUsageMB,
		UniqueHeaps:              stats.UniqueHeaps,
		UnusedBuffers:            stats.UnusedBuffers,
		UnusedTextures:           stats.UnusedTextures,
		UnusedFunctions:          stats.UnusedFunctions,
		UniqueKernels:            stats.UniqueKernels,
		ObservedKernelLabels:     stats.ObservedKernelLabels,
		DiscoveredFunctions:      stats.DiscoveredFunctions,
		CommandBuffers:           stats.CommandBuffers,
		ComputeEncodersAvailable: stats.ComputeEncodersAvailable,
		ComputeEncodersSource:    stats.ComputeEncodersSource,
		DispatchCalls:            stats.DispatchCalls,
		TotalRecords:             stats.TotalRecords,
		RecordTypes:              stats.RecordTypes,
		MTLBLibraries:            stats.MTLBLibraries,
	}
	if stats.ComputeEncodersAvailable {
		count := stats.ComputeEncoders
		s.ComputeEncoders = &count
	}

	output := &StatsJSONOutput{
		Statistics: s,
	}

	// Add metadata if available
	if trace.Metadata != nil {
		output.Metadata = &MetadataJSON{
			UUID:           trace.Metadata.UUID,
			CaptureVersion: trace.Metadata.CaptureVersion,
			GraphicsAPI:    trace.Metadata.GraphicsAPI,
			DeviceID:       trace.Metadata.DeviceID,
		}
	}

	// Add verbose information if requested
	if verbose {
		verboseData := &VerboseJSON{}

		if len(trace.EncoderLabels) > 0 {
			verboseData.EncoderLabels = trace.EncoderLabels
		}

		if len(trace.KernelNames) > 0 {
			verboseData.KernelNames = trace.KernelNames
		}

		if len(trace.BufferLabels) > 0 {
			verboseData.BufferLabels = trace.BufferLabels
		}

		if trace.CommandQueueLabel != "" {
			verboseData.CommandQueueLabel = trace.CommandQueueLabel
		}

		// Try to extract timing data
		timings, err := gputrace.ExtractTimingData(trace)
		if err == nil && len(timings) > 0 {
			verboseData.TimingData = make([]TimingJSON, len(timings))
			for i, timing := range timings {
				verboseData.TimingData[i] = TimingJSON{
					Label:          timing.Label,
					StartTimestamp: timing.StartTimestamp,
					EndTimestamp:   timing.EndTimestamp,
					DurationMs:     timing.DurationMs,
				}
			}
		}

		output.Verbose = verboseData
	}

	return writeStatsJSON(w, output)
}

func writeStatsJSON(w io.Writer, output any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return nil
}

// formatBool returns a human-readable yes/no string.
func formatBool(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// mtspRecordDescription returns a human-readable description for MTSP record types.
func mtspRecordDescription(recordType string) string {
	descriptions := map[string]string{
		"CS":        "Kernel submission (contains function name)",
		"Ct":        "Pipeline state + buffer bindings",
		"Ctt":       "Pipeline state (extended format)",
		"Ctulul":    "Pipeline state + buffer array",
		"Culul":     "Command buffer marker",
		"Ciulul":    "Indirect command buffer ref",
		"Cul":       "Resource binding",
		"Cuw":       "Buffer write/update",
		"Ci":        "Indirect dispatch reference",
		"C":         "Generic command (end encoder, pop debug)",
		"C@3ul@3ul": "Dispatch threads (grid + threadgroup size)",
		"CtU":       "Buffer definition (name + address)",
		"CU":        "Command identifier",
		"Cut":       "Command type (extended)",
		"CSuwuw":    "Kernel submission (extended)",
		"CiulSl":    "Function address reference",
		"Unknown":   "Unrecognized record format",
	}
	if desc, ok := descriptions[recordType]; ok {
		return desc
	}
	return ""
}
