package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/mlx-go/experiments/gputrace"
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

	fmt.Print(statistics.FormatStatistics())

	// If verbose, show additional analysis
	if statsVerbose {
		fmt.Println("\n=== Detailed Analysis ===\n")

		// Show metadata details
		if trace.Metadata != nil {
			fmt.Println("Metadata Details:")
			fmt.Printf("  UUID: %s\n", trace.Metadata.UUID)
			fmt.Printf("  Capture Version: %d\n", trace.Metadata.CaptureVersion)
			fmt.Printf("  Graphics API: %d\n", trace.Metadata.GraphicsAPI)
			fmt.Printf("  Device ID: %d\n", trace.Metadata.DeviceID)
			fmt.Println()
		}

		// Show all encoder labels
		if len(trace.EncoderLabels) > 0 {
			fmt.Printf("All Encoder Labels (%d):\n", len(trace.EncoderLabels))
			for i, label := range trace.EncoderLabels {
				fmt.Printf("  [%d] %s\n", i, label)
			}
			fmt.Println()
		}

		// Show all kernel names
		if len(trace.KernelNames) > 0 {
			fmt.Printf("All Kernel Names (%d):\n", len(trace.KernelNames))
			for i, name := range trace.KernelNames {
				fmt.Printf("  [%d] %s\n", i, name)
			}
			fmt.Println()
		}

		// Show buffer labels
		if len(trace.BufferLabels) > 0 {
			fmt.Printf("All Buffer Labels (%d):\n", len(trace.BufferLabels))
			for i, label := range trace.BufferLabels {
				fmt.Printf("  [%d] %s\n", i, label)
			}
			fmt.Println()
		}

		// Show command queue label
		if trace.CommandQueueLabel != "" {
			fmt.Printf("Command Queue Label: %s\n\n", trace.CommandQueueLabel)
		}

		// Try to extract timing data
		timings, err := gputrace.ExtractTimingData(trace)
		if err == nil && len(timings) > 0 {
			fmt.Printf("Timing Data (%d samples):\n", len(timings))
			for _, timing := range timings {
				fmt.Printf("  %s:\n", timing.Label)
				fmt.Printf("    Start: %d (0x%x)\n", timing.StartTimestamp, timing.StartTimestamp)
				fmt.Printf("    End:   %d (0x%x)\n", timing.EndTimestamp, timing.EndTimestamp)
				fmt.Printf("    Duration: %.2f ms\n", timing.DurationMs)
			}
			fmt.Println()
		}
	}

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
	UnusedBuffers    int            `json:"unused_buffers,omitempty"`
	UnusedTextures   int            `json:"unused_textures,omitempty"`
	UnusedFunctions  int            `json:"unused_functions,omitempty"`
	UniqueKernels    int            `json:"unique_kernels"`
	CommandBuffers   int            `json:"command_buffers"`
	ComputeEncoders  int            `json:"compute_encoders"`
	DispatchCalls    int            `json:"dispatch_calls"`
	TotalRecords     int            `json:"total_records"`
	RecordTypes      map[string]int `json:"record_types"`
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
		UnusedBuffers:    stats.UnusedBuffers,
		UnusedTextures:   stats.UnusedTextures,
		UnusedFunctions:  stats.UnusedFunctions,
		UniqueKernels:    stats.UniqueKernels,
		CommandBuffers:   stats.CommandBuffers,
		ComputeEncoders:  stats.ComputeEncoders,
		DispatchCalls:    stats.DispatchCalls,
		TotalRecords:     stats.TotalRecords,
		RecordTypes:      stats.RecordTypes,
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
