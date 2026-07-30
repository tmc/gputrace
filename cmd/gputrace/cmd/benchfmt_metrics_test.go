package cmd

import (
	"bytes"
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
