package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
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

	// Compact output: one line per encoder
	fmt.Printf("%d encoders:\n", len(encoders))
	for _, encoder := range encoders {
		if encoder.Label != "" {
			fmt.Printf("  %3d: %s\n", encoder.Index, encoder.Label)
		} else {
			fmt.Printf("  %3d: (unlabeled) 0x%x\n", encoder.Index, encoder.Address)
		}
	}

	// Show per-command-buffer breakdown if verbose
	if encodersVerbose {
		commandBuffers, err := trace.ParseCommandBuffers()
		if err == nil && len(commandBuffers) > 0 {
			fmt.Printf("\n%d command buffers (%.1f encoders/buffer avg)\n",
				len(commandBuffers), float64(len(encoders))/float64(len(commandBuffers)))
			for _, cb := range commandBuffers {
				dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
				if err != nil {
					continue
				}
				fmt.Printf("  CB %d: %d encoders\n", cb.Index, len(dcb.Encoders))
			}
		}
	}

	return nil
}
