package export

import (
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

func TestApplyStreamTimingMetadata(t *testing.T) {
	stats := &counter.StreamDataStats{
		TotalEncoderTimeUs:    9876,
		TotalDispatchTimeUs:   10117,
		CommandBufferActiveNs: 2246081,
		CommandBufferWallNs:   356626625,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	prof := &profile.Profile{}

	applyStreamTimingMetadata(prof, stats)

	if prof.DefaultSampleType != "time" {
		t.Fatalf("DefaultSampleType = %q, want time", prof.DefaultSampleType)
	}
	if got, want := prof.DurationNanos, int64(10117000); got != want {
		t.Fatalf("DurationNanos = %d, want %d", got, want)
	}
	comments := strings.Join(prof.Comments, "\n")
	for _, want := range []string{
		"gputrace timing_source: APSTimelineData Command Buffer Timestamps active time",
		"gputrace display_duration_ns: 2246081",
		"gputrace display_duration_source: APSTimelineData command buffer active time",
		"gputrace command_buffer_active_time_ns: 2246081",
		"gputrace dispatch_span_ns: 10117000",
	} {
		if !strings.Contains(comments, want) {
			t.Fatalf("comments missing %q in:\n%s", want, comments)
		}
	}
}

func TestAddStreamTimingLabels(t *testing.T) {
	stats := &counter.StreamDataStats{
		CommandBufferActiveNs: 2246081,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	labels := map[string][]string{}

	addStreamTimingLabels(labels, stats)

	if got := labels["timing_source"]; len(got) != 1 || got[0] != stats.TimingSource {
		t.Fatalf("timing_source label = %#v", got)
	}
	if got := labels["display_duration_source"]; len(got) != 1 || got[0] != "APSTimelineData command buffer active time" {
		t.Fatalf("display_duration_source label = %#v", got)
	}
}

func TestDispatchSIMDGroups(t *testing.T) {
	dispatch := trace.DispatchThreads{
		ThreadsX:         1000,
		ThreadsY:         1,
		ThreadsZ:         1,
		ThreadsPerGroupX: 256,
		ThreadsPerGroupY: 1,
		ThreadsPerGroupZ: 1,
	}
	if got, want := dispatchSIMDGroups(dispatch), int64(32); got != want {
		t.Fatalf("dispatchSIMDGroups = %d, want %d", got, want)
	}

	dispatch.ThreadsPerGroupX = 0
	if got := dispatchSIMDGroups(dispatch); got != 0 {
		t.Fatalf("dispatchSIMDGroups with missing group size = %d, want 0", got)
	}
}

func TestDispatchExecutionCostValuesDistributesPipelineCost(t *testing.T) {
	stats := &counter.StreamDataStats{
		Dispatches: []counter.DispatchInfo{
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 8, ExecutionCostPct: 7.38},
		},
	}

	costs := &counter.ExecutionCostMetrics{
		TotalSamples:       271,
		SamplesPerPipeline: map[int]int{7: 180, 8: 20, 9: 71},
	}
	values := dispatchExecutionCostValues(stats, costs)
	if got, want := len(values), 4; got != want {
		t.Fatalf("len(values) = %d, want %d", got, want)
	}
	sum7 := values[0] + values[1] + values[2]
	if got, want := sum7, int64(6642); got != want {
		t.Fatalf("pipeline 7 total = %d, want %d", got, want)
	}
	if got, want := values[3], int64(738); got != want {
		t.Fatalf("pipeline 8 value = %d, want %d", got, want)
	}
}

func TestExecutionCostBasisPointsSumsTo10000(t *testing.T) {
	costs := &counter.ExecutionCostMetrics{
		TotalSamples:       271,
		SamplesPerPipeline: map[int]int{1: 180, 2: 41, 3: 20, 4: 15, 5: 10, 6: 3, 7: 2},
	}

	values := executionCostBasisPoints(costs)
	var sum int64
	for _, v := range values {
		sum += v
	}
	if got, want := sum, int64(10000); got != want {
		t.Fatalf("basis point sum = %d, want %d", got, want)
	}
}

func TestStreamDispatchNameUsesPipelineID(t *testing.T) {
	d := counter.DispatchInfo{PipelineIndex: 0, PipelineID: 2288}
	if got, want := streamDispatchName(d), "(pipeline_2288)"; got != want {
		t.Fatalf("streamDispatchName = %q, want %q", got, want)
	}

	d.FunctionName = "kernel0"
	if got, want := streamDispatchName(d), "kernel0"; got != want {
		t.Fatalf("streamDispatchName = %q, want %q", got, want)
	}
}
