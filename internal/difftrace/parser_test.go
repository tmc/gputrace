package difftrace

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/tracebundle"
)

func TestLoadRawTraceDataReportsStructuralDispatches(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1.gputrace")
	got, err := LoadTraceData(path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.StructuralDispatches == nil || *got.StructuralDispatches <= 0 {
		t.Fatalf("structural dispatches = %v, want positive count", got.StructuralDispatches)
	}
	if got.TimingAvailable {
		t.Fatal("raw trace reported profiler timing")
	}
}

func TestLoadRawTraceDataRejectsFilters(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1.gputrace")
	_, err := LoadTraceData(path, -1, regexp.MustCompile("kernel"))
	if err == nil || !strings.Contains(err.Error(), "cannot filter") {
		t.Fatalf("error = %v, want filtered raw trace error", err)
	}
}

func TestPayloadLimitationWarning(t *testing.T) {
	got := payloadLimitationWarning("profile.gputrace", tracebundle.Payload{Class: tracebundle.PayloadProfilerOnly, HasProfilerStream: true})
	for _, want := range []string{"profiler-only", "aggregate profiler timing is available", "structural and threadgroup comparisons are unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning %q does not contain %q", got, want)
		}
	}
}

func TestHasDispatchEncoderAttributionRejectsDegenerateIndex(t *testing.T) {
	stats := &counter.StreamDataStats{
		Dispatches: []counter.DispatchInfo{
			{EncoderIndex: 2},
			{EncoderIndex: 2},
		},
		EncoderTimings: []counter.EncoderTimingInfo{
			{Index: 0},
			{Index: 1},
		},
	}
	if hasDispatchEncoderAttribution(stats) {
		t.Fatal("degenerate dispatch encoder index reported as attributed")
	}

	stats.Dispatches[1].EncoderIndex = 3
	if !hasDispatchEncoderAttribution(stats) {
		t.Fatal("distinct dispatch encoder indices reported as unavailable")
	}
}
