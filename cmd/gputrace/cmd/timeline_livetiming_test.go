package cmd

import (
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
		{"sidecar label absent", map[string]struct{}{}, "absent from trace"},
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
