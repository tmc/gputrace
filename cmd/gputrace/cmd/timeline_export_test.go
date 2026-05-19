package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExportChromeTracingIncludesTimingMetadata(t *testing.T) {
	effective := uint64(1650625)
	timeline := &Timeline{
		Timing: &TimelineTiming{
			EncoderSpanNs:         9876000,
			DispatchSpanNs:        10117000,
			EffectiveGPUTimeNs:    &effective,
			CommandBufferActiveNs: 2246081,
			CommandBufferWallNs:   356626625,
			DisplayDurationNs:     effective,
			DisplayDurationSource: "APSTimelineData ReplayerGPUTime",
			TimingSource:          "APSTimelineData ReplayerGPUTime (Xcode Effective GPU Time)",
		},
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var doc struct {
		TraceEvents    []TimelineEvent        `json:"traceEvents"`
		GputraceTiming map[string]interface{} `json:"gputrace_timing"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := uint64(doc.GputraceTiming["display_duration_ns"].(float64)); got != effective {
		t.Fatalf("gputrace_timing display_duration_ns = %d, want %d", got, effective)
	}

	var foundSummary, foundDuration bool
	for _, ev := range doc.TraceEvents {
		if ev.Name == "Xcode Timing Summary" && ev.Category == "xcode_timing" {
			foundSummary = true
			if got := ev.Args["timing_source"]; got != timeline.Timing.TimingSource {
				t.Fatalf("summary timing_source = %v, want %q", got, timeline.Timing.TimingSource)
			}
		}
		if ev.Name == "Xcode Display Duration" && ev.Category == "xcode_timing" {
			foundDuration = true
			if got, want := ev.Duration, effective/1000; got != want {
				t.Fatalf("display duration event = %d, want %d", got, want)
			}
		}
	}
	if !foundSummary {
		t.Fatal("missing Xcode Timing Summary event")
	}
	if !foundDuration {
		t.Fatal("missing Xcode Display Duration event")
	}
}
