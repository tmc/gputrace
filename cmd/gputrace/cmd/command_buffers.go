package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	cmdBuffersVerbose bool
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

	// Basic output
	fmt.Printf("=== Command Buffers ===\n")
	fmt.Printf("Total: %d\n\n", len(commandBuffers))

	// List command buffers
	for _, cb := range commandBuffers {
		fmt.Printf("Command Buffer #%d\n", cb.Index)
		fmt.Printf("  UUID:      %s\n", cb.UUID)
		fmt.Printf("  Timestamp: %d (0x%x)\n", cb.Timestamp, cb.Timestamp)
		fmt.Printf("  Offset:    0x%08x\n", cb.Offset)

		if cmdBuffersVerbose || cmdBuffersDetailed {
			// Get detailed information
			dcb, err := trace.ParseDetailedCommandBuffer(cb.Index)
			if err != nil {
				fmt.Printf("  Error parsing details: %v\n", err)
			} else {
				fmt.Printf("  Encoders:  %d\n", len(dcb.Encoders))
				fmt.Printf("  API Calls: %d\n", len(dcb.Calls))
			}
		}

		fmt.Println()
	}

	// Show detailed analysis if requested
	if cmdBuffersDetailed {
		fmt.Printf("\n=== Detailed Analysis ===\n\n")
		for _, cb := range commandBuffers {
			if err := trace.DumpCommandBuffer(cmd.OutOrStdout(), cb.Index); err != nil {
				fmt.Printf("Error dumping command buffer #%d: %v\n", cb.Index, err)
			}
		}
	}

	// Summary statistics
	if cmdBuffersVerbose || cmdBuffersDetailed {
		totalEncoders := 0
		totalAPICalls := 0

		for _, cb := range commandBuffers {
			dcb, err := trace.ParseDetailedCommandBuffer(cb.Index)
			if err == nil {
				totalEncoders += len(dcb.Encoders)
				totalAPICalls += len(dcb.Calls)
			}
		}

		fmt.Printf("\n=== Summary ===\n")
		fmt.Printf("Total Command Buffers: %d\n", len(commandBuffers))
		fmt.Printf("Total Encoders:        %d\n", totalEncoders)
		fmt.Printf("Total API Calls:       %d\n", totalAPICalls)
		if len(commandBuffers) > 0 {
			fmt.Printf("Avg Encoders/Buffer:   %.1f\n", float64(totalEncoders)/float64(len(commandBuffers)))
			fmt.Printf("Avg API Calls/Buffer:  %.1f\n", float64(totalAPICalls)/float64(len(commandBuffers)))
		}
	}

	return nil
}
