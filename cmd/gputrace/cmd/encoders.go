package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var encodersVerbose bool

var encodersCmd = &cobra.Command{
	Use:   "encoders <trace.gputrace>",
	Short: "List compute command encoders in a GPU trace",
	Long: `List all Metal compute command encoders found in a GPU trace.

This command parses Cul records to identify compute command encoder
creation and usage. Compute encoders are used to encode compute
commands (kernel dispatches) into command buffers.

Examples:
  gputrace encoders trace.gputrace
  gputrace encoders trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runEncoders,
}

func init() {
	rootCmd.AddCommand(encodersCmd)

	encodersCmd.Flags().BoolVarP(&encodersVerbose, "verbose", "v", false, "Show verbose output with encoder details")
}

func runEncoders(cmd *cobra.Command, args []string) error {
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

	// Parse compute encoders
	encoders, err := trace.ParseComputeEncoders()
	if err != nil {
		return fmt.Errorf("failed to parse compute encoders: %w", err)
	}

	// Basic output
	fmt.Printf("=== Compute Encoders ===\n")
	fmt.Printf("Total: %d\n\n", len(encoders))

	// List encoders
	for _, encoder := range encoders {
		fmt.Printf("Encoder #%d\n", encoder.Index)
		if encoder.Label != "" {
			fmt.Printf("  Label:   %s\n", encoder.Label)
		}
		fmt.Printf("  Address: 0x%x\n", encoder.Address)
		if encodersVerbose {
			fmt.Printf("  Offset:  0x%08x\n", encoder.Offset)
		}
		fmt.Println()
	}

	// Show per-command-buffer breakdown if verbose
	if encodersVerbose {
		commandBuffers, err := trace.ParseCommandBuffers()
		if err == nil && len(commandBuffers) > 0 {
			fmt.Printf("\n=== Per-Command-Buffer Breakdown ===\n\n")

			for _, cb := range commandBuffers {
				dcb, err := trace.ParseDetailedCommandBuffer(cb.Index)
				if err != nil {
					fmt.Printf("Command Buffer #%d: Error - %v\n", cb.Index, err)
					continue
				}

				fmt.Printf("Command Buffer #%d: %d encoder(s)\n", cb.Index, len(dcb.Encoders))
				for _, encoder := range dcb.Encoders {
					fmt.Printf("  Encoder 0x%x\n", encoder.Address)
				}
			}
		}

		// Show summary statistics
		if len(commandBuffers) > 0 {
			fmt.Printf("\n=== Summary ===\n")
			fmt.Printf("Total Encoders:        %d\n", len(encoders))
			fmt.Printf("Total Command Buffers: %d\n", len(commandBuffers))
			fmt.Printf("Avg Encoders/Buffer:   %.2f\n", float64(len(encoders))/float64(len(commandBuffers)))
		}
	}

	return nil
}
