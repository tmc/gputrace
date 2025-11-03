package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var statsVerbose bool

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
  gputrace stats trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)

	statsCmd.Flags().BoolVarP(&statsVerbose, "verbose", "v", false, "Show verbose statistics including detailed analysis")
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
	statistics, err := trace.ExtractStatistics()
	if err != nil {
		return fmt.Errorf("failed to extract statistics: %w", err)
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
		timings, err := trace.ExtractTimingData()
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
