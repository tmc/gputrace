package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var buffersDiffCmd = &cobra.Command{
	Use:   "diff <trace1.gputrace> <trace2.gputrace>",
	Short: "Compare buffers between two GPU traces",
	Long: `Compare Metal buffer usage between two GPU traces.

This command shows:
  - Buffers added in trace2
  - Buffers removed from trace1
  - Buffers with size changes
  - Total memory delta

This is useful for:
  - Tracking memory optimization changes
  - Detecting memory regressions
  - Understanding buffer lifecycle changes
  - Comparing different execution paths

Examples:
  # Compare two traces
  gputrace buffers diff baseline.gputrace optimized.gputrace

  # Compare before/after optimization
  gputrace buffers diff before.gputrace after.gputrace`,
	Args: cobra.ExactArgs(2),
	RunE: runBuffersDiff,
}

func init() {
	buffersCmd.AddCommand(buffersDiffCmd)
}

func runBuffersDiff(cmd *cobra.Command, args []string) error {
	trace1Path := args[0]
	trace2Path := args[1]

	// Verify both trace files exist
	if err := checkTraceFile(trace1Path); err != nil {
		return fmt.Errorf("trace1: %w", err)
	}
	if err := checkTraceFile(trace2Path); err != nil {
		return fmt.Errorf("trace2: %w", err)
	}

	// Open both traces
	trace1, err := gputrace.Open(trace1Path)
	if err != nil {
		return fmt.Errorf("failed to open trace1: %w", err)
	}

	trace2, err := gputrace.Open(trace2Path)
	if err != nil {
		return fmt.Errorf("failed to open trace2: %w", err)
	}

	// Extract buffer sizes
	buffers1, err := gputrace.ExtractBufferSizes(trace1)
	if err != nil {
		return fmt.Errorf("failed to extract buffers from trace1: %w", err)
	}

	buffers2, err := gputrace.ExtractBufferSizes(trace2)
	if err != nil {
		return fmt.Errorf("failed to extract buffers from trace2: %w", err)
	}

	// Compare buffers
	diff := gputrace.CompareBuffers(buffers1, buffers2)

	// Format and print diff
	output := gputrace.FormatBufferDiff(diff, trace1Path, trace2Path)
	fmt.Print(output)

	return nil
}
