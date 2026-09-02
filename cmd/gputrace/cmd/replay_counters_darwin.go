//go:build darwin && metal

package cmd

import (
	"fmt"

	"github.com/tmc/gputrace"
)

func replayCountersRealAvailable() bool { return true }

func runReplayCountersReal(tracePath string, opts *replayCountersOptions) error {
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer trace.Close()

	engine, err := gputrace.NewMetalReplayEngine(trace)
	if err != nil {
		return fmt.Errorf("create Metal replay engine: %w", err)
	}
	defer engine.Close()

	config := replayCounterConfig(opts)
	if len(opts.counterSets) == 0 {
		// The generic planning names are not guaranteed to be device counter-set
		// names. Timestamp is the only set verified on this host.
		config.EnabledCounterSets = []string{"timestamp"}
	}
	sampler, err := engine.NewCounterSampler(config)
	if err != nil {
		return fmt.Errorf("create counter sampler: %w", err)
	}
	plan, err := engine.AnalyzeReplay()
	if err != nil {
		return fmt.Errorf("analyze replay: %w", err)
	}
	result, err := engine.ExecuteReplayPlanWithCounters(plan, sampler)
	if err != nil {
		return fmt.Errorf("execute replay with counters: %w", err)
	}

	data := map[string]interface{}{
		"plan":         plan,
		"result":       result,
		"sample_count": len(sampler.Samples),
		"samples":      sampler.Samples,
		"raw_data":     sampler.RawData,
	}
	if opts.output != "" && isJSONOutput(opts.output) {
		return writeOutput(opts.output, "", data)
	}
	output := gputrace.FormatMetalReplayResult(result)
	output += fmt.Sprintf("Counter samples: %d\n", len(sampler.Samples))
	for name, raw := range sampler.RawData {
		output += fmt.Sprintf("  %s: %d raw bytes\n", name, len(raw))
	}
	return writeOutput(opts.output, output, nil)
}
