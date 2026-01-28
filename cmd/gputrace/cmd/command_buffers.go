package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	cmdBuffersVerbose  bool
	cmdBuffersDetailed bool
)

var commandBuffersCmd = &cobra.Command{
	Use:   "command-buffers <trace.gputrace>",
	Short: "List and analyze command buffers in a GPU trace",
	Long: `List all Metal command buffers found in a GPU trace.

This command parses CUUU markers to identify command buffer submissions
and can provide detailed analysis of each command buffer including:
  - Number and types of encoders
  - API calls within each buffer
  - Dispatch calls and thread configurations

Examples:
  gputrace command-buffers trace.gputrace
  gputrace command-buffers trace.gputrace -v
  gputrace command-buffers trace.gputrace -d`,
	Args: cobra.ExactArgs(1),
	RunE: runCommandBuffers,
}

func init() {
	rootCmd.AddCommand(commandBuffersCmd)

	commandBuffersCmd.Flags().BoolVarP(&cmdBuffersVerbose, "verbose", "v", false, "Show verbose output with encoder and API call counts")
	commandBuffersCmd.Flags().BoolVarP(&cmdBuffersDetailed, "detailed", "d", false, "Show detailed analysis of each command buffer")
}

func runCommandBuffers(cmd *cobra.Command, args []string) error {
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

	// Parse command buffers
	commandBuffers, err := trace.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("failed to parse command buffers: %w", err)
	}

	// Compact one-line-per-buffer output
	fmt.Printf("%d command buffers:\n", len(commandBuffers))
	for _, cb := range commandBuffers {
		if cmdBuffersVerbose || cmdBuffersDetailed {
			dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
			if err != nil {
				fmt.Printf("  %3d: offset=0x%08x (error: %v)\n", cb.Index, cb.Offset, err)
			} else {
				fmt.Printf("  %3d: %d encoders, %d calls\n", cb.Index, len(dcb.Encoders), len(dcb.Calls))
			}
		} else {
			fmt.Printf("  %3d: offset=0x%08x\n", cb.Index, cb.Offset)
		}
	}

	// Show detailed analysis if requested
	if cmdBuffersDetailed {
		fmt.Printf("\n=== Detailed Analysis ===\n\n")
		for _, cb := range commandBuffers {
			if err := gputrace.DumpCommandBuffer(trace, cmd.OutOrStdout(), cb.Index); err != nil {
				fmt.Printf("Error dumping command buffer #%d: %v\n", cb.Index, err)
			}
		}
	}

	// Summary statistics (verbose only)
	if cmdBuffersVerbose && !cmdBuffersDetailed {
		totalEncoders := 0
		totalAPICalls := 0
		for _, cb := range commandBuffers {
			dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
			if err == nil {
				totalEncoders += len(dcb.Encoders)
				totalAPICalls += len(dcb.Calls)
			}
		}
		if len(commandBuffers) > 0 {
			fmt.Printf("\nTotal: %d encoders, %d calls (%.1f enc/buf, %.1f calls/buf)\n",
				totalEncoders, totalAPICalls,
				float64(totalEncoders)/float64(len(commandBuffers)),
				float64(totalAPICalls)/float64(len(commandBuffers)))
		}
	}

	return nil
}
