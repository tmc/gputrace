package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	bufferTimelineFormat string
	bufferTimelineWidth  int
	bufferTimelineOutput string
)

var bufferTimelineCmd = &cobra.Command{
	Use:   "buffer-timeline <trace.gputrace>",
	Short: "Visualize buffer allocation and usage timeline",
	Long: `Analyze and visualize buffer lifecycle events across the trace.

This command extracts buffer allocation, usage, and deallocation patterns
and presents them in various formats:

  - ASCII: Terminal-based bar chart showing buffer lifetimes
  - summary: Text summary with statistics and top buffers
  - chrome: Chrome tracing format for ui.perfetto.dev
  - json: Raw JSON data

The timeline shows:
  - Buffer allocation/deallocation times
  - Memory usage over time
  - Peak memory usage
  - Buffer sizes and usage patterns

Examples:
  # Show ASCII timeline
  gputrace buffer-timeline trace.gputrace

  # Export to Chrome tracing format
  gputrace buffer-timeline trace.gputrace --format chrome -o buffers.json

  # Show summary statistics
  gputrace buffer-timeline trace.gputrace --format summary

  # Wider ASCII display
  gputrace buffer-timeline trace.gputrace --width 120`,
	Args: cobra.ExactArgs(1),
	RunE: runBufferTimeline,
}

func init() {
	rootCmd.AddCommand(bufferTimelineCmd)

	bufferTimelineCmd.Flags().StringVarP(&bufferTimelineFormat, "format", "f", "ascii",
		"Output format: ascii, summary, chrome, json")
	bufferTimelineCmd.Flags().IntVarP(&bufferTimelineWidth, "width", "w", 100,
		"Width for ASCII visualization")
	bufferTimelineCmd.Flags().StringVarP(&bufferTimelineOutput, "output", "o", "",
		"Output file (default: stdout)")
}

func runBufferTimeline(cmd *cobra.Command, args []string) error {
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

	// Extract buffer timeline
	timeline, err := gputrace.ExtractBufferTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to extract buffer timeline: %w", err)
	}

	// Generate output based on format
	var output string
	switch bufferTimelineFormat {
	case "ascii":
		output = gputrace.FormatBufferTimelineASCII(timeline, bufferTimelineWidth)
	case "summary":
		output = gputrace.FormatBufferTimelineSummary(timeline)
	case "chrome":
		return fmt.Errorf("chrome trace format not yet implemented")
	case "json":
		return fmt.Errorf("json export not yet implemented")
	default:
		return fmt.Errorf("unknown format: %s (valid: ascii, summary, chrome, json)", bufferTimelineFormat)
	}

	// Output to file or stdout
	if bufferTimelineOutput != "" {
		return os.WriteFile(bufferTimelineOutput, []byte(output), 0644)
	}

	fmt.Print(output)
	return nil
}
