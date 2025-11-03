package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var shadersVerbose bool

var shadersCmd = &cobra.Command{
	Use:   "shaders <trace.gputrace>",
	Short: "Show shader performance statistics (Xcode Instruments format)",
	Long: `Display shader/kernel performance statistics in Xcode Instruments format.

Shows:
  - Cost % (percentage of total GPU time)
  - Shader name
  - Type (Compute)
  - Pipeline State address
  - # SIMD Groups (threadgroups dispatched)
  - # Allocated Registers (estimated)
  - High Register (peak register usage)
  - Spilled Bytes (register spills to memory)

Output matches Xcode Instruments GPU counters format.

Examples:
  gputrace shaders trace.gputrace
  gputrace shaders trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runShaders,
}

func init() {
	rootCmd.AddCommand(shadersCmd)

	shadersCmd.Flags().BoolVarP(&shadersVerbose, "verbose", "v", false, "Show verbose output")
}

func runShaders(cmd *cobra.Command, args []string) error {
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
	defer trace.Close()

	// Extract shader metrics
	report, err := trace.ExtractShaderMetrics()
	if err != nil {
		return fmt.Errorf("failed to extract shader metrics: %w", err)
	}

	// Format as Xcode Instruments style output
	gputrace.FormatShadersXcodeStyle(os.Stdout, report)

	return nil
}
