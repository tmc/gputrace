package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/livetiming"
)

func TestProjectLiveTimingBindsCaptureLabelsAndNanoseconds(t *testing.T) {
	sidecar := livetiming.Sidecar{
		RunID: "run-1", ContentDigest: testDigest("a"),
		ClockSamples: []livetiming.ClockSample{{CPUTimeNS: 1, GPUTimeNS: 1}, {CPUTimeNS: 2, GPUTimeNS: 2}, {CPUTimeNS: 3, GPUTimeNS: 3}},
		CommandBuffers: []livetiming.CommandBuffer{{
			ID: 1, CaptureLabel: "gputrace.live.cb.1", FinalLabel: "decode",
			GPUStartNS: 1_250, GPUEndNS: 1_751, Status: 4,
		}},
	}
	timeline := &Timeline{}
	if err := projectLiveTiming(timeline, map[string]struct{}{"gputrace.live.cb.1": {}}, sidecar); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(timeline.Events))
	}
	event := timeline.Events[0]
	if event.TimestampNS != 1_250 || event.DurationNS != 501 {
		t.Fatalf("exact timing = %d+%d, want 1250+501", event.TimestampNS, event.DurationNS)
	}
	if !timelineHasMeasuredClock(timeline, timelineClockLive) {
		t.Fatal("live clock not admitted")
	}
	if got := perfettoEventArgs(timeline, event, timelineClockLive)["timing_quality"]; got != "measured" {
		t.Fatalf("timing quality = %v, want measured", got)
	}
}

func TestExportLiveTimingRetainsReceiptAndEventEvidence(t *testing.T) {
	timeline := &Timeline{
		LiveTiming: &liveTimingProjection{
			RunID: "run-1", ContentDigest: testDigest("a"),
			ClockSamples: 3, CommandBuffers: 2, Projected: 1, Unmatched: 1,
		},
		Events: []TimelineEvent{{
			Name: "decode", Category: "live_command_buffer", Phase: "X",
			TimestampNS: 1_250, DurationNS: 501, ProcessID: 2,
			Args: map[string]interface{}{
				"command_buffer_id": 1,
				"capture_label":     "gputrace.live.cb.1",
				"final_label":       "decode",
				"timing_source":     "MTLCommandBuffer.GPUStartTime/GPUEndTime",
				"clock_domain":      "live",
				"run_id":            "run-1",
				"sidecar_digest":    testDigest("a"),
			},
		}},
	}
	out := filepath.Join(t.TempDir(), "live.pftrace")
	if err := exportPerfettoForClock(timeline, out, timelineClockLive); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"live_timing_projected_command_buffers", "live_timing_unmatched_command_buffers",
		"live_timing_digest", testDigest("a"),
		"gputrace.live.cb.1", "MTLCommandBuffer.GPUStartTime/GPUEndTime",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("native live trace missing %q", want)
		}
	}
}

func TestProjectLiveTimingRefusesIncompleteJoins(t *testing.T) {
	sidecar := livetiming.Sidecar{
		RunID: "run-1", ContentDigest: testDigest("a"),
		ClockSamples:   []livetiming.ClockSample{{}, {}, {}},
		CommandBuffers: []livetiming.CommandBuffer{{ID: 1, CaptureLabel: "gputrace.live.cb.1", FinalLabel: "decode"}},
	}
	tests := []struct {
		name     string
		captured map[string]struct{}
		want     string
	}{
		{"no intersection", map[string]struct{}{}, "no sidecar command buffer belongs"},
		{"captured label incomplete", map[string]struct{}{"gputrace.live.cb.1": {}, "gputrace.live.cb.2": {}}, "has no completed sidecar record"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := projectLiveTiming(&Timeline{}, test.captured, sidecar)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProjectLiveTimingRetainsUnmatchedSidecarCount(t *testing.T) {
	sidecar := livetiming.Sidecar{
		RunID: "run-1", ContentDigest: testDigest("a"),
		ClockSamples: []livetiming.ClockSample{{}, {}, {}},
		CommandBuffers: []livetiming.CommandBuffer{
			{ID: 1, CaptureLabel: "gputrace.live.cb.1", FinalLabel: "decode"},
			{ID: 2, CaptureLabel: "gputrace.live.cb.2", FinalLabel: "cleanup"},
		},
	}
	timeline := &Timeline{}
	if err := projectLiveTiming(timeline, map[string]struct{}{"gputrace.live.cb.1": {}}, sidecar); err != nil {
		t.Fatal(err)
	}
	if timeline.LiveTiming.Projected != 1 || timeline.LiveTiming.Unmatched != 1 {
		t.Fatalf("projection = %+v", timeline.LiveTiming)
	}
}
