package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/hostcorrelation"
	"github.com/tmc/gputrace/internal/mlxsemantic"
	"github.com/tmc/gputrace/internal/perfetto"
)

func TestAttachHostCorrelation(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "run.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "metadata"), []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := mlxsemantic.Digest(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	receipt := hostcorrelation.Receipt{
		Schema: hostcorrelation.Schema,
		Host: hostcorrelation.Artifact{
			Kind: "host-signpost", RunID: "run-1", ContentDigest: testDigest("1"), ClockDomain: "busy",
		},
		GPU: hostcorrelation.Artifact{
			Kind: "gpu-trace", RunID: "run-1", ContentDigest: digest, ClockDomain: "busy",
		},
		Events: []hostcorrelation.Event{{ID: "event-1", Kind: "interval", Name: "decode", TimestampNS: 1250, DurationNS: 500}},
	}
	receiptPath := writeHostCorrelationReceipt(t, receipt)
	timeline := measuredBusyTimeline()
	if err := attachHostCorrelation(timeline, tracePath, timelineClockBusy, receiptPath); err != nil {
		t.Fatal(err)
	}
	if got := timeline.HostCorrelation.Events[0].TimestampNS; got != 1250 {
		t.Fatalf("timestamp = %d, want 1250", got)
	}

	trace := &perfetto.Trace{Metadata: make(map[string]any)}
	appendHostCorrelationEvents(trace, timeline)
	if len(trace.Events) != 1 || trace.Events[0].StartNS != 1250 || trace.Events[0].DurationNS != 500 {
		t.Fatalf("events = %+v, want exact nanosecond interval", trace.Events)
	}
}

func TestAttachHostCorrelationRefusesUnprovenJoins(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "run.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "metadata"), []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := mlxsemantic.Digest(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	base := hostcorrelation.Receipt{
		Schema: hostcorrelation.Schema,
		Host: hostcorrelation.Artifact{
			Kind: "host-signpost", RunID: "run-1", ContentDigest: testDigest("1"), ClockDomain: "busy",
		},
		GPU: hostcorrelation.Artifact{
			Kind: "gpu-trace", RunID: "run-1", ContentDigest: digest, ClockDomain: "busy",
		},
		Events: []hostcorrelation.Event{{ID: "event-1", Kind: "instant", Name: "decode", TimestampNS: 1250}},
	}
	tests := []struct {
		name     string
		timeline *Timeline
		clock    timelineClock
		mutate   func(*hostcorrelation.Receipt)
		want     string
	}{
		{"approximate clock", &Timeline{Timing: &TimelineTiming{EncoderTimingSource: "synthetic", EncoderTimingApproximate: true}}, timelineClockBusy, nil, "not measured"},
		{"both clocks", measuredBusyTimeline(), timelineClockBoth, nil, "no shared timeline axis"},
		{"wrong clock", measuredBusyTimeline(), timelineClockBusy, func(r *hostcorrelation.Receipt) { r.Host.ClockDomain, r.GPU.ClockDomain = "wall", "wall" }, "differs from selected clock"},
		{"wrong trace", measuredBusyTimeline(), timelineClockBusy, func(r *hostcorrelation.Receipt) { r.GPU.ContentDigest = testDigest("2") }, "does not identify input trace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := base
			if test.mutate != nil {
				test.mutate(&receipt)
			}
			err := attachHostCorrelation(test.timeline, tracePath, test.clock, writeHostCorrelationReceipt(t, receipt))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func measuredBusyTimeline() *Timeline {
	return &Timeline{Timing: &TimelineTiming{EncoderTimingSource: "streamData", TimingSource: "streamData"}}
}

func writeHostCorrelationReceipt(t *testing.T, receipt hostcorrelation.Receipt) string {
	t.Helper()
	data, err := receipt.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "correlation.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testDigest(digit string) string {
	return "sha256:" + strings.Repeat(digit, 64)
}
