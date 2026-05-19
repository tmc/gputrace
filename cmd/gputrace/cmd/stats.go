package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

var (
	statsVerbose bool
	statsJSON    bool
)

var statsCmd = &cobra.Command{
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
	RunE: runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)

	statsCmd.Flags().BoolVarP(&statsVerbose, "verbose", "v", false, "Show verbose statistics including detailed analysis")
	statsCmd.Flags().BoolVar(&statsJSON, "json", false, "Output statistics in JSON format")
}

func runStats(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		if findProfilerDir(tracePath) != "" {
			return runStatsFromProfiler(tracePath)
		}
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Extract and display statistics
	statistics, err := gputrace.ExtractStatistics(trace)
	if err != nil {
		return fmt.Errorf("failed to extract statistics: %w", err)
	}

	// Handle JSON output
	if statsJSON {
		return outputStatsJSON(statistics, trace, statsVerbose)
	}

	// Quick one-liner summary
	parts := []string{
		fmt.Sprintf("%d %s", statistics.ComputeEncoders, Pluralize(statistics.ComputeEncoders, "encoder", "encoders")),
		fmt.Sprintf("%d %s", statistics.DispatchCalls, Pluralize(statistics.DispatchCalls, "dispatch", "dispatches")),
		fmt.Sprintf("%d %s", statistics.UniqueKernels, Pluralize(statistics.UniqueKernels, "kernel", "kernels")),
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
	fmt.Printf("  Compute Encoders: %s\n", FormatCount(statistics.ComputeEncoders))
	fmt.Printf("  Dispatch Calls:   %s\n", FormatCount(statistics.DispatchCalls))
	fmt.Printf("  Unique Kernels:   %s\n", FormatCount(statistics.UniqueKernels))
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
		if streamStats, err := counter.ParseStreamData(profilerDir); err == nil {
			gpuTimeUs = streamStats.TotalTimeUs
		}
	}

	// Timing Section
	fmt.Println(Colorize("Timing", ColorBold))
	fmt.Println(TableSeparator(40))
	if gpuTimeUs > 0 {
		fmt.Printf("  GPU Time:         %s\n", FormatDuration(gpuTimeUs))
	} else {
		fmt.Printf("  GPU Time:         (no profiler data)\n")
	}
	fmt.Printf("  Profiler Data:    %s\n", formatBool(hasProfilerData))
	fmt.Println()

	// Top Kernels Section (if we have data)
	if hasProfilerData && profilerDir != "" {
		if streamStats, err := counter.ParseStreamData(profilerDir); err == nil && len(streamStats.Dispatches) > 0 {
			fmt.Println(Colorize("Top Kernels (by time)", ColorBold))
			fmt.Println(TableSeparator(40))

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
				return sorted[i].time > sorted[j].time
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
				fmt.Printf("  ...and %d more kernels\n", len(sorted)-5)
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
	if statsVerbose {
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
			for i, label := range trace.EncoderLabels {
				fmt.Printf("  [%d] %s\n", i, label)
			}
			fmt.Println()
		}

		// Show all kernel names
		if len(trace.KernelNames) > 0 {
			fmt.Printf("%s (%d):\n", Colorize("All Kernel Names", ColorGreen), len(trace.KernelNames))
			for i, name := range trace.KernelNames {
				fmt.Printf("  [%d] %s\n", i, name)
			}
			fmt.Println()
		}

		// Show buffer labels
		if len(trace.BufferLabels) > 0 {
			fmt.Printf("%s (%d):\n", Colorize("All Buffer Labels", ColorGreen), len(trace.BufferLabels))
			for i, label := range trace.BufferLabels {
				fmt.Printf("  [%d] %s\n", i, label)
			}
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

type profilerStatsJSONOutput struct {
	ProfilerOnly bool              `json:"profiler_only"`
	ProfilerDir  string            `json:"profiler_dir"`
	Statistics   profilerStatsJSON `json:"statistics"`
}

type profilerStatsJSON struct {
	CommandBuffers      int    `json:"command_buffers"`
	ComputeEncoders     int    `json:"compute_encoders"`
	DispatchCalls       int    `json:"dispatch_calls"`
	UniquePipelines     int    `json:"unique_pipelines"`
	TotalGPUTimeUs      int    `json:"total_gpu_time_us"` // Backward-compatible alias for TotalEncoderTimeUs.
	TotalEncoderTimeUs  int    `json:"total_encoder_time_us"`
	TotalDispatchTimeUs int    `json:"total_dispatch_time_us"`
	TimingSource        string `json:"timing_source"`
}

func runStatsFromProfiler(tracePath string) error {
	profilerDir, streamStats, err := loadProfilerStats(tracePath)
	if err != nil {
		return err
	}

	commandBuffers := 0
	if streamStats.Timeline != nil {
		commandBuffers = len(streamStats.Timeline.CommandBufferTimestamps)
	}
	stats := profilerStatsJSON{
		CommandBuffers:      commandBuffers,
		ComputeEncoders:     streamStats.NumEncoders,
		DispatchCalls:       streamStats.NumGPUCommands,
		UniquePipelines:     streamStats.NumPipelines,
		TotalGPUTimeUs:      streamStats.TotalEncoderTimeUs,
		TotalEncoderTimeUs:  streamStats.TotalEncoderTimeUs,
		TotalDispatchTimeUs: streamStats.TotalDispatchTimeUs,
		TimingSource:        streamStats.TimingSource,
	}

	if statsJSON {
		output := profilerStatsJSONOutput{
			ProfilerOnly: true,
			ProfilerDir:  profilerDir,
			Statistics:   stats,
		}
		jsonData, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil
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
	fmt.Printf("  Note:          no MTSP capture data; use profiler for kernel timing details\n")
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
		fmt.Printf("  Effective GPU:    (not parsed from Xcode)\n")
	} else {
		fmt.Printf("  Timing:           (not available)\n")
	}
	fmt.Println()
	return nil
}

// StatsJSONOutput represents the JSON output structure for stats command.
type StatsJSONOutput struct {
	Statistics *StatsJSON    `json:"statistics"`
	Metadata   *MetadataJSON `json:"metadata,omitempty"`
	Verbose    *VerboseJSON  `json:"verbose,omitempty"`
}

// StatsJSON represents statistics in JSON format.
type StatsJSON struct {
	BufferUsageBytes uint64         `json:"buffer_usage_bytes"`
	BufferUsageGB    float64        `json:"buffer_usage_gb"`
	BufferSizeSum    uint64         `json:"buffer_size_sum"`
	UniqueBuffers    int            `json:"unique_buffers"`
	HeapUsageBytes   uint64         `json:"heap_usage_bytes"`
	HeapUsageMB      float64        `json:"heap_usage_mb"`
	UniqueHeaps      int            `json:"unique_heaps"`
	UnusedBuffers    int            `json:"unused_buffers,omitempty"`
	UnusedTextures   int            `json:"unused_textures,omitempty"`
	UnusedFunctions  int            `json:"unused_functions,omitempty"`
	UniqueKernels    int            `json:"unique_kernels"`
	CommandBuffers   int            `json:"command_buffers"`
	ComputeEncoders  int            `json:"compute_encoders"`
	DispatchCalls    int            `json:"dispatch_calls"`
	TotalRecords     int            `json:"total_records"`
	RecordTypes      map[string]int `json:"record_types"`
	MTLBLibraries    int            `json:"mtlb_libraries"`
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
func outputStatsJSON(stats *gputrace.TraceStatistics, trace *gputrace.Trace, verbose bool) error {
	s := &StatsJSON{
		BufferUsageBytes: stats.BufferUsageBytes,
		BufferUsageGB:    stats.BufferUsageGB,
		BufferSizeSum:    stats.BufferSizeSum,
		UniqueBuffers:    stats.UniqueBuffers,
		HeapUsageBytes:   stats.HeapUsageBytes,
		HeapUsageMB:      stats.HeapUsageMB,
		UniqueHeaps:      stats.UniqueHeaps,
		UnusedBuffers:    stats.UnusedBuffers,
		UnusedTextures:   stats.UnusedTextures,
		UnusedFunctions:  stats.UnusedFunctions,
		UniqueKernels:    stats.UniqueKernels,
		CommandBuffers:   stats.CommandBuffers,
		ComputeEncoders:  stats.ComputeEncoders,
		DispatchCalls:    stats.DispatchCalls,
		TotalRecords:     stats.TotalRecords,
		RecordTypes:      stats.RecordTypes,
		MTLBLibraries:    stats.MTLBLibraries,
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

	// Marshal to JSON with indentation
	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(jsonData))
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
