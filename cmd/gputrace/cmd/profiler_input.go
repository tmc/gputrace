package cmd

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilereplay"
)

func loadProfilerStats(tracePath string) (string, *counter.StreamDataStats, error) {
	profilerDir := findProfilerDir(tracePath)
	if profilerDir == "" {
		fmt.Fprint(os.Stderr, profileReplayHint(tracePath))
		return "", nil, fmt.Errorf("no .gpuprofiler_raw directory found in %s", tracePath)
	}

	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		return profilerDir, nil, fmt.Errorf("parse streamData: %w", err)
	}
	counter.CorrelateDispatchSamples(stats)
	return profilerDir, stats, nil
}

// profileReplayHint returns advice for a trace that holds a capture but no
// performance data, or "" when there is no advice worth giving: a profiler-only
// export has nothing left to replay, so naming a command that would refuse the
// trace is worse than saying nothing.
//
// The Xcode route is the fallback rather than the recommendation. It drives the
// UI and takes minutes with the machine; the replay is headless and takes
// seconds. It is still what remains when MTLReplayer is not installed.
func profileReplayHint(tracePath string) string {
	if profilereplay.Replayable(tracePath) != nil {
		return ""
	}
	add := "gputrace profile-replay " + tracePath
	if profilereplay.Available() != nil {
		add = "gputrace xcode-profile run " + tracePath
	}
	return fmt.Sprintf("Note: %s holds a capture but no performance data.\n"+
		"      Add it with: %s\n\n", tracePath, add)
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
