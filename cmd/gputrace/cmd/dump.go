package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	dumpFilter       string
	dumpNoIndent     bool
	dumpNoNumbers    bool
	dumpBuffersOnly  bool
	dumpDispatchOnly bool
	dumpEncodersOnly bool
	dumpJSON         bool
	dumpFull         bool
	dumpCommandBufferIndex int
)

var dumpCmd = &cobra.Command{
	Use:   "dump [trace-path]",
	Short: "Dump all API calls from a GPU trace",
	Long: `Dumps all Metal API calls from a GPU trace in a format similar to Xcode Instruments.

The output shows:
- Initialization calls (buffer/library/pipeline creation)
- Command buffer execution with all encoder calls
- Buffer bindings and dispatch calls

Filtering options:
  --filter <pattern>    Only show API calls matching pattern
  --buffers-only        Show only buffer creation calls
  --dispatch-only       Show only dispatch calls
  --encoders-only       Show only encoder-related calls
  --command-buffer <n>  Show only calls from command buffer N

Formatting options:
  --no-indent          Disable indentation for nested calls
  --no-numbers         Don't number the API calls
  --json               Output in JSON format
  --full               Show expanded tree view with all call levels

Examples:
  gputrace dump trace.gputrace
  gputrace dump trace.gputrace --full
  gputrace dump trace.gputrace --filter "Buffer"
  gputrace dump trace.gputrace --dispatch-only
  gputrace dump trace.gputrace --command-buffer 0
`,
	Args: cobra.ExactArgs(1),
	RunE: runDump,
}

func init() {
	rootCmd.AddCommand(dumpCmd)

	dumpCmd.Flags().StringVar(&dumpFilter, "filter", "", "Filter API calls by pattern")
	dumpCmd.Flags().BoolVar(&dumpNoIndent, "no-indent", false, "Disable indentation")
	dumpCmd.Flags().BoolVar(&dumpNoNumbers, "no-numbers", false, "Don't number API calls")
	dumpCmd.Flags().BoolVar(&dumpBuffersOnly, "buffers-only", false, "Show only buffer creation calls")
	dumpCmd.Flags().BoolVar(&dumpDispatchOnly, "dispatch-only", false, "Show only dispatch calls")
	dumpCmd.Flags().BoolVar(&dumpEncodersOnly, "encoders-only", false, "Show only encoder-related calls")
	dumpCmd.Flags().BoolVar(&dumpJSON, "json", false, "Output in JSON format")
	dumpCmd.Flags().BoolVar(&dumpFull, "full", false, "Show expanded tree view with all call levels")
	dumpCmd.Flags().IntVar(&dumpCommandBufferIndex, "command-buffer", -1, "Show only calls from specific command buffer")
}

func runDump(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	if dumpFull {
		if err := trace.FormatAPICallListFull(os.Stdout); err != nil {
			return fmt.Errorf("format API calls: %w", err)
		}
	} else {
		if err := trace.FormatAPICallList(os.Stdout); err != nil {
			return fmt.Errorf("format API calls: %w", err)
		}
	}

	return nil
}

func matchesFilter(callString, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(callString, filter)
}
