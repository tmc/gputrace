package export

import (
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/counter"
)

func TestApplyStreamTimingMetadata(t *testing.T) {
	stats := &counter.StreamDataStats{
		TotalEncoderTimeUs:    9876,
		TotalDispatchTimeUs:   10117,
		CommandBufferActiveNs: 2246081,
		CommandBufferWallNs:   356626625,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	prof := &profile.Profile{}

	applyStreamTimingMetadata(prof, stats)

	if prof.DefaultSampleType != "time" {
		t.Fatalf("DefaultSampleType = %q, want time", prof.DefaultSampleType)
	}
	if got, want := prof.DurationNanos, int64(10117000); got != want {
		t.Fatalf("DurationNanos = %d, want %d", got, want)
	}
	comments := strings.Join(prof.Comments, "\n")
	for _, want := range []string{
		"gputrace timing_source: APSTimelineData Command Buffer Timestamps active time",
		"gputrace display_duration_ns: 2246081",
		"gputrace display_duration_source: APSTimelineData command buffer active time",
		"gputrace command_buffer_active_time_ns: 2246081",
		"gputrace dispatch_span_ns: 10117000",
	} {
		if !strings.Contains(comments, want) {
			t.Fatalf("comments missing %q in:\n%s", want, comments)
		}
	}
}

func TestAddStreamTimingLabels(t *testing.T) {
	stats := &counter.StreamDataStats{
		CommandBufferActiveNs: 2246081,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	labels := map[string][]string{}

	addStreamTimingLabels(labels, stats)

	if got := labels["timing_source"]; len(got) != 1 || got[0] != stats.TimingSource {
		t.Fatalf("timing_source label = %#v", got)
	}
	if got := labels["display_duration_source"]; len(got) != 1 || got[0] != "APSTimelineData command buffer active time" {
		t.Fatalf("display_duration_source label = %#v", got)
	}
}
