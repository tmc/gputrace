//go:build darwin && metal

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

type replayMetalOptions struct {
	json   bool
	output string
}

var replayMetalCmd = newReplayMetalCommand(&replayMetalOptions{})

func newReplayMetalCommand(opts *replayMetalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay-metal <trace.gputrace>",
		Short: "Execute a trace through the public Metal replay engine",
		Long: `Execute supported trace commands through the public Metal replay engine.

This command is available only in builds made with the metal build tag. A
successful result means command submission completed for the supported replay
plan; it does not validate replayed buffer contents against the capture. Any
unsupported command fails closed and may leave a partial execution count.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReplayMetal(cmd, args[0], opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "write the result as JSON")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "", "write the result to a file")
	return cmd
}

func init() {
	rootCmd.AddCommand(replayMetalCmd)
}

func runReplayMetal(_ *cobra.Command, path string, opts *replayMetalOptions) error {
	if err := checkTraceFile(path); err != nil {
		return err
	}
	trace, err := gputrace.Open(path)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer trace.Close()

	engine, err := gputrace.NewMetalReplayEngine(trace)
	if err != nil {
		return fmt.Errorf("create Metal replay engine: %w", err)
	}
	defer engine.Close()

	plan, err := engine.AnalyzeReplay()
	if err != nil {
		return fmt.Errorf("analyze replay: %w", err)
	}
	result, replayErr := engine.ExecuteReplayPlan(plan)
	if result == nil {
		if replayErr != nil {
			return fmt.Errorf("execute replay: %w", replayErr)
		}
		return fmt.Errorf("execute replay: nil result")
	}

	var output string
	var data interface{}
	if opts.json {
		data = result
	} else {
		output = gputrace.FormatMetalReplayResult(result)
	}
	if err := writeOutput(opts.output, output, data); err != nil {
		return err
	}
	if replayErr != nil {
		return fmt.Errorf("execute replay: %w", replayErr)
	}
	return nil
}
