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
