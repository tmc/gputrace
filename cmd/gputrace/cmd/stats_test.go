package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

func TestFormatPayloadCompletenessGatesStructuralClaims(t *testing.T) {
	got := formatPayloadCompleteness(tracebundle.Payload{Class: tracebundle.PayloadProfilerOnly, HasProfilerStream: true})
	for _, want := range []string{"profiler-only", "aggregate timing available", "structural/threadgroup data unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatPayloadCompleteness() = %q, want %q", got, want)
		}
	}
}

func TestRunStatsJSONUsesCommandOutput(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	opts := &statsOptions{json: true}
	if err := runStats(command, []string{tracePath}, opts); err != nil {
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
	if got.Statistics.DiscoveredFunctions < got.Statistics.UniqueKernels {
		t.Fatalf("discovered functions = %d, dispatched kernels = %d", got.Statistics.DiscoveredFunctions, got.Statistics.UniqueKernels)
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

func TestOutputStatsJSONReportsEncoderCountAvailability(t *testing.T) {
	stats := &gputrace.TraceStatistics{
		CommandBuffers:           10,
		DispatchCalls:            435,
		ComputeEncodersSource:    "unavailable: raw capture lacks command-buffer-scoped encoder lifecycle evidence",
		ComputeEncodersAvailable: false,
	}

	var out bytes.Buffer
	if err := outputStatsJSON(&out, stats, &gputrace.Trace{}, false); err != nil {
		t.Fatalf("outputStatsJSON: %v", err)
	}

	var got StatsJSONOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode stats JSON: %v", err)
	}
	if got.Statistics.CommandBuffers != 10 || got.Statistics.DispatchCalls != 435 {
		t.Fatalf("structural totals changed: %+v", got.Statistics)
	}
	if got.Statistics.ComputeEncodersAvailable {
		t.Fatalf("compute_encoders_available = true, want false")
	}
	if got.Statistics.ComputeEncoders != nil {
		t.Fatalf("compute_encoders = %v, want null", got.Statistics.ComputeEncoders)
	}
	if !strings.Contains(got.Statistics.ComputeEncodersSource, "command-buffer-scoped") {
		t.Fatalf("compute_encoders_source = %q", got.Statistics.ComputeEncodersSource)
	}
}
