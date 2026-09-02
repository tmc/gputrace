package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/counter"
)

func TestWriteProfilerJSONUsesCommandOutput(t *testing.T) {
	effectiveNs := uint64(42000)
	output := ProfilerOutputStats{
		StreamDataStats: &counter.StreamDataStats{
			Pipelines: []counter.PipelineStats{{
				PipelineID:       7,
				FunctionName:     "kernel_add",
				InstructionCount: 11,
			}},
			Dispatches: []counter.DispatchInfo{{
				Index:         0,
				PipelineIndex: 0,
				PipelineID:    7,
				FunctionName:  "kernel_add",
				EncoderIndex:  1,
				CumulativeUs:  42,
				DurationUs:    42,
			}},
			FunctionNames:       []string{"kernel_add"},
			NumEncoders:         1,
			NumGPUCommands:      1,
			NumPipelines:        1,
			TotalTimeUs:         42,
			TotalEncoderTimeUs:  42,
			TotalDispatchTimeUs: 42,
			EffectiveGPUTimeNs:  &effectiveNs,
			TimingSource:        "synthetic",
		},
		ExecutionCost: []counter.ExecutionCostByFunction{{
			FunctionName: "kernel_add",
			CostPercent:  100,
			PipelineIDs:  []int{7},
			SampleCount:  3,
		}},
	}

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	if err := writeProfilerJSON(command.OutOrStdout(), output); err != nil {
		t.Fatalf("writeProfilerJSON: %v", err)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("profiler JSON output missing trailing newline: %q", out.String())
	}

	var got ProfilerOutputStats
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("profiler JSON output did not decode: %v\n%s", err, out.String())
	}
	if got.StreamDataStats == nil || len(got.Dispatches) != 1 {
		t.Fatalf("profiler JSON stream stats = %+v", got.StreamDataStats)
	}
	if got.NumGPUCommands != 1 || got.Dispatches[0].FunctionName != "kernel_add" {
		t.Fatalf("profiler JSON stream stats = %+v", got.StreamDataStats)
	}
	if len(got.ExecutionCost) != 1 || got.ExecutionCost[0].SampleCount != 3 {
		t.Fatalf("profiler JSON execution cost = %+v", got.ExecutionCost)
	}
	if !strings.Contains(out.String(), "\"execution_cost\": [") {
		t.Fatalf("profiler JSON output changed execution_cost shape:\n%s", out.String())
	}
}

func TestSelectLimiterRowsSuppressesZerosAndHonorsLimit(t *testing.T) {
	all := []limiterMetrics{
		{EncoderIndex: 1},
		{EncoderIndex: 2, F32Limiter: 0.04},
		{EncoderIndex: 3, L1Cache: 10},
		{EncoderIndex: 4, IntegerComplex: 20},
		{EncoderIndex: 5, InstructionThroughput: 5},
	}

	rows, nonzero, zero := selectLimiterRows(all, 2)
	if nonzero != 3 || zero != 2 {
		t.Fatalf("nonzero=%d zero=%d, want 3 and 2", nonzero, zero)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	if rows[0].EncoderIndex != 4 || rows[1].EncoderIndex != 3 {
		t.Fatalf("rows=%+v, want descending peak limiter", rows)
	}
}

func TestProfilerLimitFlagDefaultsToTwenty(t *testing.T) {
	opts := &profilerOptions{limit: 20}
	cmd := newProfilerCommand(opts)
	if got := cmd.Flag("limit").DefValue; got != "20" {
		t.Fatalf("--limit default=%q, want 20", got)
	}
}

func TestDispatchedFunctionNames(t *testing.T) {
	dispatches := []counter.DispatchInfo{
		{PipelineID: 7, FunctionName: "used"},
		{PipelineID: 7, FunctionName: "used"},
		{PipelineID: 9},
	}
	got := dispatchedFunctionNames(dispatches)
	want := []string{"used", "(pipeline_9)"}
	if len(got) != len(want) {
		t.Fatalf("dispatchedFunctionNames() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dispatchedFunctionNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDispatchedPipelines(t *testing.T) {
	pipelines := []counter.PipelineStats{
		{PipelineID: 1, FunctionName: "unused"},
		{PipelineID: 2, FunctionName: "used_by_id"},
		{PipelineID: 3, FunctionName: "used_by_name"},
	}
	dispatches := []counter.DispatchInfo{
		{PipelineID: 2},
		{FunctionName: "used_by_name"},
	}
	got := dispatchedPipelines(pipelines, dispatches)
	if len(got) != 2 || got[0].PipelineID != 2 || got[1].PipelineID != 3 {
		t.Fatalf("dispatchedPipelines() = %+v, want pipeline IDs 2 and 3", got)
	}
}
