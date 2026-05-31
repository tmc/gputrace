package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunStatsJSONUsesCommandOutput(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	oldJSON := statsJSON
	oldVerbose := statsVerbose
	defer func() {
		statsJSON = oldJSON
		statsVerbose = oldVerbose
	}()
	statsJSON = true
	statsVerbose = false

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	if err := runStats(command, []string{tracePath}); err != nil {
		t.Fatalf("runStats: %v", err)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("stats JSON output missing trailing newline: %q", out.String())
	}

	var got StatsJSONOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stats JSON output did not decode: %v\n%s", err, out.String())
	}
	if got.Statistics == nil {
		t.Fatalf("stats JSON output missing statistics: %s", out.String())
	}
}

func TestWriteStatsJSONProfilerOutput(t *testing.T) {
	output := profilerStatsJSONOutput{
		ProfilerOnly: true,
		ProfilerDir:  "/tmp/profile.gpuprofiler_raw",
		Statistics: profilerStatsJSON{
			CommandBuffers:  2,
			ComputeEncoders: 3,
			DispatchCalls:   4,
			UniquePipelines: 5,
			TimingSource:    "streamData",
		},
	}

	var out bytes.Buffer
	if err := writeStatsJSON(&out, output); err != nil {
		t.Fatalf("writeStatsJSON: %v", err)
	}

	var got profilerStatsJSONOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("profiler stats JSON did not decode: %v\n%s", err, out.String())
	}
	if !got.ProfilerOnly || got.Statistics.DispatchCalls != 4 {
		t.Fatalf("profiler stats JSON = %+v", got)
	}
}
