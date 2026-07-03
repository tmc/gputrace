package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var replayCountersCmd = newReplayCountersCommand(&replayCountersOptions{
	encoderBoundaries:  true,
	dispatchBoundaries: true,
	useBarriers:        true,
})

type replayCountersOptions struct {
	counterSets        []string
	encoderBoundaries  bool
	dispatchBoundaries bool
	useBarriers        bool
	simulate           bool
	output             string
}

func newReplayCountersCommand(opts *replayCountersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "replay-counters <trace.gputrace>",
		Short:  "Plan MTLCounterSampleBuffer sampling; real collection is disabled",
		Hidden: true,
		Long: `Plan Metal performance counter sampling for trace replay.

IMPORTANT: This command is fail-closed for real replay counter collection.

Current Behavior:
  - --simulate builds a sampling plan only
  - --simulate does not replay GPU work
  - Running without --simulate fails closed before trace replay or GPU work
  - No replay-time MTLCounterSampleBuffer collection is attempted

Use this command to inspect:
  - Where counter samples would be taken (encoder/dispatch boundaries)
  - Sampling overhead estimates (barrier synchronization cost)
  - Memory requirements for counter buffers
  - Counter aggregation and reporting structure

Use profiler when you need existing profiler data:
   - Reads existing .gpuprofiler_raw files from Instruments
   - No GPU execution required
   - Binary format undocumented (reverse engineering needed)

Current Status:
Replay-time counter collection currently fails closed until Metal API bindings
are connected and the replay path can collect counters safely. The planned
counter sets are:
  - Timestamp counters (GPU cycles)
  - Stage utilization (vertex/fragment/compute)
  - Statistics (draw/dispatch counts)
  - Apple GPU hardware counters (ALU, cache, bandwidth)

Output modes:
  - simulate: Show overhead and memory analysis without replaying GPU work
  - json: Export simulation results as JSON when -o ends in .json

Counter Sets (--counter-sets):
  - timestamp: GPU timestamp in cycles
  - stage_utilization: Vertex/Fragment/Compute utilization
  - statistics: Draw and dispatch counts
  - All sets are enabled by default

Sampling Points (--encoder-boundaries, --dispatch-boundaries):
  - Encoder boundaries: Sample at start/end of each encoder (recommended)
  - Dispatch boundaries: Sample before/after each compute dispatch (detailed)
  - Both enabled by default for complete coverage

Examples:
  # Show sampling overhead analysis
  gputrace replay-counters trace.gputrace --simulate

  # Sample only at encoder boundaries (lower overhead)
  gputrace replay-counters trace.gputrace --simulate --encoder-boundaries --dispatch-boundaries=false

  # Enable specific counter sets
  gputrace replay-counters trace.gputrace --simulate --counter-sets timestamp,stage_utilization

  # Export simulation as JSON
  gputrace replay-counters trace.gputrace --simulate -o counters.json

Implementation Status:
  This command provides only a planning/simulation path today. Actual replay-time
  GPU counter collection is intentionally unavailable and fails closed before
  trace replay or GPU work.

Related Commands:
  - gputrace profiler: Extract profiler timing data from .gpuprofiler_raw/streamData
  - gputrace xcode-profile xcode-export-counters: Export counters through Xcode`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReplayCounters(cmd, args, opts)
		},
	}

	cmd.Flags().StringSliceVar(&opts.counterSets, "counter-sets", opts.counterSets,
		"Counter sets to enable (default: all)")
	cmd.Flags().BoolVar(&opts.encoderBoundaries, "encoder-boundaries", opts.encoderBoundaries,
		"Sample at encoder boundaries (start/end)")
	cmd.Flags().BoolVar(&opts.dispatchBoundaries, "dispatch-boundaries", opts.dispatchBoundaries,
		"Sample at dispatch boundaries (before/after)")
	cmd.Flags().BoolVar(&opts.useBarriers, "use-barriers", opts.useBarriers,
		"Insert barriers for accurate sampling")
	cmd.Flags().BoolVar(&opts.simulate, "simulate", opts.simulate,
		"Show simulation/overhead analysis only")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output,
		"Output file (default: stdout)")
	return cmd
}

func init() {
	rootCmd.AddCommand(replayCountersCmd)
}

func runReplayCounters(cmd *cobra.Command, args []string, opts *replayCountersOptions) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	if !opts.simulate {
		return fmt.Errorf("real replay counter collection is unavailable without replay-time Metal bindings; rerun with --simulate to inspect the sampling plan")
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Create replay engine
	engine := gputrace.NewReplayEngine(trace)

	// Configure counter sampling
	config := &gputrace.CounterSamplingConfig{
		EnabledCounterSets:         opts.counterSets,
		SampleAtEncoderBoundaries:  opts.encoderBoundaries,
		SampleAtDispatchBoundaries: opts.dispatchBoundaries,
		UseBarriers:                opts.useBarriers,
		GPUFrequency:               0, // Auto-detect
	}

	// Use defaults if no counter sets specified
	if len(config.EnabledCounterSets) == 0 {
		config.EnabledCounterSets = []string{"timestamp", "stage_utilization", "statistics"}
	}

	// Enable counter sampling
	if err := engine.EnableCounterSampling(config); err != nil {
		return fmt.Errorf("failed to enable counter sampling: %w", err)
	}

	var output string
	var data interface{}

	if opts.simulate {
		// Show simulation/overhead analysis
		simulation, err := engine.SimulateCounterSampling()
		if err != nil {
			return fmt.Errorf("failed to simulate counter sampling: %w", err)
		}

		if opts.output != "" && isJSONOutput(opts.output) {
			data = simulation
		} else {
			output = gputrace.FormatCounterSamplingSimulation(simulation)
		}
	} else {
		// Perform full analysis with counter sampling
		plan, result, err := engine.AnalyzeReplayWithCounters()
		if err != nil {
			return fmt.Errorf("failed to analyze replay with counters: %w", err)
		}

		if opts.output != "" && isJSONOutput(opts.output) {
			// Export combined result
			data = map[string]interface{}{
				"plan":   plan,
				"result": result,
			}
		} else {
			// Generate text report
			output = "=== Replay with Counter Sampling ===\n\n"
			output += fmt.Sprintf("Trace: %s\n\n", plan.TraceePath)
			output += fmt.Sprintf("Replay Plan:\n")
			output += fmt.Sprintf("  Encoders: %d\n", len(plan.Encoders))
			output += fmt.Sprintf("  Commands: %d\n", len(plan.Commands))
			output += fmt.Sprintf("  Compute Dispatches: %d\n\n", plan.ComputeDispatches)

			output += gputrace.FormatCounterSamplingResult(result)
		}
	}

	// Write output
	return writeOutput(opts.output, output, data)
}

func writeOutput(filename, textOutput string, jsonData interface{}) error {
	writer, closeOutput, err := createCommandOutput(filename)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	if textOutput != "" {
		if _, err := io.WriteString(writer, textOutput); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	} else if jsonData != nil {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(jsonData); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
	}

	if filename != "" {
		fmt.Fprintf(os.Stderr, "✓ Written to: %s\n", filename)
	}

	return nil
}

func isJSONOutput(filename string) bool {
	return len(filename) > 5 && filename[len(filename)-5:] == ".json"
}
