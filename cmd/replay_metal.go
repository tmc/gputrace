// +build metal

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var replayMetalCmd = &cobra.Command{
	Use:   "replay-metal <trace>",
	Short: "Replay GPU trace with actual Metal execution",
	Long: `Replay GPU trace with actual Metal execution on GPU hardware.

This command uses the Metal Bridge to execute the trace on real GPU hardware:
1. Restores buffers from trace files to Metal buffers
2. Compiles shaders and creates Metal pipeline states
3. Encodes and executes commands on GPU
4. Validates output against original trace

Requires macOS with Metal support.`,
	Args: cobra.ExactArgs(1),
	RunE: runReplayMetal,
}

var (
	replayMetalValidate   bool
	replayMetalVerbose    bool
)

func init() {
	rootCmd.AddCommand(replayMetalCmd)

	replayMetalCmd.Flags().BoolVar(&replayMetalValidate, "validate", false,
		"Validate replay output against original trace")
	replayMetalCmd.Flags().BoolVar(&replayMetalVerbose, "verbose", false,
		"Show detailed execution information")
}

func runReplayMetal(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	// Create Metal replay engine
	engine, err := gputrace.NewMetalReplayEngine(trace)
	if err != nil {
		return fmt.Errorf("create Metal replay engine: %w", err)
	}
	defer engine.Close()

	// Analyze replay plan
	plan, err := engine.AnalyzeReplay()
	if err != nil {
		return fmt.Errorf("analyze replay: %w", err)
	}

	if replayMetalVerbose {
		fmt.Println(gputrace.FormatReplayPlan(plan))
	}

	// Execute replay on GPU
	fmt.Printf("Executing replay on Metal GPU...\n")
	result, err := engine.ExecuteReplayPlan(plan)
	if err != nil {
		return fmt.Errorf("execute replay: %w", err)
	}

	// Show results
	fmt.Println(gputrace.FormatMetalReplayResult(result))

	// Validate if requested
	if replayMetalValidate {
		fmt.Println("Validating replay output...")
		validation, err := engine.ValidateExecution(plan)
		if err != nil {
			return fmt.Errorf("validate execution: %w", err)
		}

		fmt.Println(gputrace.FormatMetalValidationResult(validation))

		// Exit with error if validation failed
		if validation.BuffersMismatched > 0 {
			os.Exit(1)
		}
	}

	return nil
}
