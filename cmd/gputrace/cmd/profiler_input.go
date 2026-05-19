package cmd

import (
	"fmt"

	"github.com/tmc/gputrace/internal/counter"
)

func loadProfilerStats(tracePath string) (string, *counter.StreamDataStats, error) {
	profilerDir := findProfilerDir(tracePath)
	if profilerDir == "" {
		return "", nil, fmt.Errorf("no .gpuprofiler_raw directory found in %s", tracePath)
	}

	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return profilerDir, nil, fmt.Errorf("parse streamData: %w", err)
	}
	counter.CorrelateDispatchSamples(stats)
	return profilerDir, stats, nil
}

func aggregateExecutionCost(profilerDir string, stats *counter.StreamDataStats) []counter.ExecutionCostByFunction {
	if stats == nil || len(stats.Pipelines) == 0 {
		return nil
	}
	pipelineIDs := make([]int, 0, len(stats.Pipelines))
	for _, p := range stats.Pipelines {
		pipelineIDs = append(pipelineIDs, p.PipelineID)
	}
	costMetrics, err := counter.ParseExecutionCost(profilerDir, pipelineIDs)
	if err != nil {
		return nil
	}
	return counter.AggregateExecutionCostByFunction(costMetrics, stats.Pipelines)
}
