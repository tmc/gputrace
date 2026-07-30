package cmd

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"golang.org/x/perf/benchfmt"
)

func TestInferBenchfmtProvenance(t *testing.T) {
	tests := []struct {
		path         string
		runtime      string
		model        string
		captureRange string
		cacheMode    string
	}{
		{
			path:         "/traces/go/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata.gputrace",
			runtime:      "go",
			model:        "qwen25-05b",
			captureRange: "2:4",
			cacheMode:    "warm",
		},
		{
			path:         "/traces/python/qwen25-05b-warm_tokens_2_to_4.gputrace",
			runtime:      "python",
			model:        "qwen25-05b",
			captureRange: "2:4",
			cacheMode:    "warm",
		},
		{
			path:         "/traces/swift/laguna-xs21_tokens_1_to_2_layer2.gputrace",
			runtime:      "swift",
			model:        "laguna-xs21",
			captureRange: "1:2",
			cacheMode:    "unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.runtime, func(t *testing.T) {
			if got := inferBenchfmtRuntime(test.path); got != test.runtime {
				t.Fatalf("runtime = %q, want %q", got, test.runtime)
			}
			if got := inferBenchfmtModel(test.path); got != test.model {
				t.Fatalf("model = %q, want %q", got, test.model)
			}
			if got := inferBenchfmtCaptureRange(test.path); got != test.captureRange {
				t.Fatalf("capture range = %q, want %q", got, test.captureRange)
			}
			if got := inferBenchfmtCacheMode(test.path); got != test.cacheMode {
				t.Fatalf("cache mode = %q, want %q", got, test.cacheMode)
			}
		})
	}
}

func TestWriteProfilerBenchfmtOmitsUnavailableTiming(t *testing.T) {
	stats := &counter.StreamDataStats{
		NumEncoders:         3,
		NumGPUCommands:      7,
		TotalDispatchTimeUs: 42,
		TimingSource:        "gpuCommandInfoData cumulative offsets",
		Dispatches: []counter.DispatchInfo{
			{SampleCount: 2},
			{SampleCount: 3},
		},
	}
	var out bytes.Buffer
	if err := writeProfilerBenchfmt(&out, "/traces/go/model_tokens2-4.gputrace", stats, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), benchfmtCBActiveUnit) ||
		strings.Contains(out.String(), benchfmtCBWallUnit) ||
		strings.Contains(out.String(), benchfmtEffectiveGPUUnit) {
		t.Fatalf("output contains unavailable timing:\n%s", out.String())
	}

	reader := benchfmt.NewReader(strings.NewReader(out.String()), "test.bench")
	if !reader.Scan() {
		t.Fatalf("Scan: %v", reader.Err())
	}
	result := reader.Result().(*benchfmt.Result)
	for unit, want := range map[string]float64{
		benchfmtDispatchSpanUnit:    42000,
		benchfmtGPRWCNTRSamplesUnit: 5,
		benchfmtDispatchesUnit:      7,
		benchfmtEncodersUnit:        3,
	} {
		if got, ok := result.Value(unit); !ok || got != want {
			t.Fatalf("%s = %v, %v, want %v, true", unit, got, ok, want)
		}
	}
}

func TestWriteProfilerBenchfmtExecutionCost(t *testing.T) {
	stats := &counter.StreamDataStats{
		NumEncoders:    3,
		NumGPUCommands: 7,
		TimingSource:   "streamData",
	}
	costs := []counter.ExecutionCostByFunction{
		{
			FunctionName: "steel/gemm",
			CostPercent:  20.25,
			SampleCount:  4,
		},
		{
			FunctionName: "steel gemm",
			CostPercent:  7.5,
			SampleCount:  2,
		},
	}
	flags := benchfmtConfigFlags{{Key: "experiment", Value: "cost-test"}}

	var out bytes.Buffer
	if err := writeProfilerBenchfmt(&out, "/traces/go/model_tokens2-4.gputrace", stats, costs, flags); err != nil {
		t.Fatal(err)
	}

	type resultSnapshot struct {
		name         string
		experiment   string
		timingSource string
		values       map[string]float64
	}
	reader := benchfmt.NewReader(strings.NewReader(out.String()), "test.bench")
	var results []resultSnapshot
	for reader.Scan() {
		result, ok := reader.Result().(*benchfmt.Result)
		if !ok {
			t.Fatalf("record type = %T, want *benchfmt.Result", reader.Result())
		}
		values := make(map[string]float64)
		for _, value := range result.Values {
			values[value.Unit] = value.Value
		}
		results = append(results, resultSnapshot{
			name:         string(result.Name),
			experiment:   result.GetConfig("experiment"),
			timingSource: result.GetConfig("timing-source"),
			values:       values,
		})
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if got, want := len(results), 3; got != want {
		t.Fatalf("results = %d, want %d\n%s", got, want, out.String())
	}

	if got := results[0].experiment; got != "cost-test" {
		t.Fatalf("main experiment config = %q, want %q", got, "cost-test")
	}
	if got, ok := results[0].values[benchfmtProfilerCostSamplesUnit]; !ok || got != 6 {
		t.Fatalf("main cost samples = %v, %v, want 6, true", got, ok)
	}

	names := make(map[string]bool)
	for i, result := range results[1:] {
		if got := result.experiment; got != "cost-test" {
			t.Fatalf("cost %d experiment config = %q, want %q", i, got, "cost-test")
		}
		if got := result.timingSource; got != "streamData" {
			t.Fatalf("cost %d timing-source config = %q, want %q", i, got, "streamData")
		}
		got, ok := result.values[benchfmtProfilerSampleCostUnit]
		if !ok || got != costs[i].CostPercent {
			t.Fatalf("cost %d value = %v, %v, want %v, true", i, got, ok, costs[i].CostPercent)
		}
		name := result.name
		if names[name] {
			t.Fatalf("duplicate benchmark name %q for colliding sanitized function names", name)
		}
		names[name] = true
		if !strings.Contains(name, "ProfilerSampleCost_steel_gemm_") {
			t.Fatalf("cost %d name = %q, want sanitized function name and hash", i, name)
		}
	}
}

func TestWriteProfilerBenchfmtInvalidExecutionCostIsAtomic(t *testing.T) {
	stats := &counter.StreamDataStats{
		NumEncoders:    1,
		NumGPUCommands: 1,
		TimingSource:   "streamData",
	}
	costs := []counter.ExecutionCostByFunction{{
		FunctionName: "invalid",
		CostPercent:  math.NaN(),
		SampleCount:  1,
	}}

	var out bytes.Buffer
	err := writeProfilerBenchfmt(&out, "/traces/go/model_tokens2-4.gputrace", stats, costs, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid benchfmt value") {
		t.Fatalf("error = %v, want invalid benchfmt value", err)
	}
	if out.Len() != 0 {
		t.Fatalf("partial output on error:\n%s", out.String())
	}
}
