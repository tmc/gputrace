package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	replayFormat   string
	replayOutput   string
	replayValidate bool
)

var replayCmd = &cobra.Command{
	Use:   "replay <trace.gputrace>",
	Short: "Analyze GPU trace replay structure and requirements",
	Long: `Analyze a GPU trace and show what would be replayed.

This command parses the trace capture file and extracts the command buffer
structure, showing:
  - Command sequence (dispatches, buffer bindings, pipeline changes)
  - Encoder organization
  - Resource requirements (buffers, functions, pipelines)
  - Validation of replay readiness

This provides the foundation for actual GPU replay with Metal API bindings.
The replay engine can be extended with MTLCounterSampleBuffer support to
collect performance counters during replay (see gputrace-54).

Output formats:
  - plan: Show detailed replay execution plan (default)
  - validate: Check if trace can be replayed
  - json: Export replay plan as JSON
  - state: Show resource restoration analysis

Examples:
  # Show replay execution plan
  gputrace replay trace.gputrace

  # Validate replay readiness
  gputrace replay trace.gputrace --format validate

  # Show resource restoration requirements
  gputrace replay trace.gputrace --format state

  # Export replay plan to JSON
  gputrace replay trace.gputrace --format json -o replay-plan.json

Note: This command performs analysis only. Actual GPU replay requires
Metal API bindings (CGo/Swift) which can be added in a future phase.`,
	Args: cobra.ExactArgs(1),
	RunE: runReplay,
}

func init() {
	rootCmd.AddCommand(replayCmd)

	replayCmd.Flags().StringVarP(&replayFormat, "format", "f", "plan",
		"Output format: plan, validate, state, json")
	replayCmd.Flags().StringVarP(&replayOutput, "output", "o", "",
		"Output file (default: stdout)")
	replayCmd.Flags().BoolVarP(&replayValidate, "validate", "v", false,
		"Validate replay readiness (shortcut for --format validate)")
}

func runReplay(cmd *cobra.Command, args []string) error {
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

	// Create replay engine
	engine := gputrace.NewReplayEngine(trace)

	// Handle --validate flag as shortcut
	if replayValidate {
		replayFormat = "validate"
	}

	// Generate output based on format
	var output string
	var data interface{}

	switch replayFormat {
	case "plan":
		plan, err := engine.AnalyzeReplay()
		if err != nil {
			return fmt.Errorf("failed to analyze replay: %w", err)
		}
		output = gputrace.FormatReplayPlan(plan)

	case "validate":
		validation, err := engine.ValidateReplay()
		if err != nil {
			return fmt.Errorf("failed to validate replay: %w", err)
		}
		output = gputrace.FormatReplayValidation(validation)

		// Exit with error code if validation failed
		if !validation.CanReplay {
			// Write output first
			if replayOutput != "" {
				if err := os.WriteFile(replayOutput, []byte(output), 0644); err != nil {
					return fmt.Errorf("failed to write output: %w", err)
				}
				fmt.Fprintf(os.Stderr, "✓ Written to: %s\n", replayOutput)
			} else {
				fmt.Print(output)
			}
			return fmt.Errorf("trace validation failed")
		}

	case "state":
		plan, err := engine.AnalyzeReplay()
		if err != nil {
			return fmt.Errorf("failed to analyze replay: %w", err)
		}
		if plan.StateAnalysis != nil {
			output = gputrace.FormatReplayAnalysis(plan.StateAnalysis)
		} else {
			output = "No state analysis available\n"
		}

	case "json":
		plan, err := engine.AnalyzeReplay()
		if err != nil {
			return fmt.Errorf("failed to analyze replay: %w", err)
		}
		data = plan

	default:
		return fmt.Errorf("unknown format: %s (valid: plan, validate, state, json)", replayFormat)
	}

	// Write output
	if replayOutput != "" {
		f, err := os.Create(replayOutput)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer f.Close()

		if output != "" {
			if _, err := f.WriteString(output); err != nil {
				return fmt.Errorf("failed to write output: %w", err)
			}
		} else {
			encoder := json.NewEncoder(f)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(data); err != nil {
				return fmt.Errorf("failed to write JSON: %w", err)
			}
		}

		fmt.Fprintf(os.Stderr, "✓ Written to: %s\n", replayOutput)
	} else {
		if output != "" {
			fmt.Print(output)
		} else {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(data); err != nil {
				return fmt.Errorf("failed to write JSON: %w", err)
			}
		}
	}

	return nil
}
