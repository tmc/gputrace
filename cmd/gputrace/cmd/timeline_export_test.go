//go:build darwin

package cmd

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	tracepkg "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/xcodebindings"
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
		TraceEvents []TimelineEvent `json:"traceEvents"`
		OtherData   struct {
			GputraceTiming       map[string]interface{} `json:"gputrace_timing"`
			GputraceXcodeMetrics map[string]interface{} `json:"gputrace_xcode_metrics"`
		} `json:"otherData"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := uint64(doc.OtherData.GputraceTiming["display_duration_ns"].(float64)); got != effective {
		t.Fatalf("gputrace_timing display_duration_ns = %d, want %d", got, effective)
	}
	if got := doc.OtherData.GputraceXcodeMetrics["has_effective_gpu_time"]; got != true {
		t.Fatalf("has_effective_gpu_time = %v, want true", got)
	}
	bindings := doc.OtherData.GputraceXcodeMetrics["binding_candidates"].(map[string]interface{})
	if got, want := bindings["high_register"], "GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex"; got != want {
		t.Fatalf("binding candidate high_register = %v, want %q", got, want)
	}

	var foundSummary, foundCoverage bool
	for _, ev := range doc.TraceEvents {
		if ev.Name == "Xcode Timing Summary" && ev.Category == "xcode_timing" {
			foundSummary = true
			if got := ev.Args["timing_source"]; got != timeline.Timing.TimingSource {
				t.Fatalf("summary timing_source = %v, want %q", got, timeline.Timing.TimingSource)
			}
		}
		if ev.Name == "Xcode Metrics Coverage" && ev.Category == "xcode_metrics" {
			foundCoverage = true
			if got := ev.Args["has_effective_gpu_time"]; got != true {
				t.Fatalf("coverage has_effective_gpu_time = %v, want true", got)
			}
		}
	}
	if !foundSummary {
		t.Fatal("missing Xcode Timing Summary event")
	}
	if !foundCoverage {
		t.Fatal("missing Xcode Metrics Coverage event")
	}
}

func TestApplyXcodeGPUTime(t *testing.T) {
	timeline := &Timeline{Timing: &TimelineTiming{TimingSource: "APSTimelineData"}}
	applyXcodeGPUTime(timeline, 9_161_250)
	if timeline.Timing.EffectiveGPUTimeNs == nil || *timeline.Timing.EffectiveGPUTimeNs != 9_161_250 {
		t.Fatalf("effective GPU time = %#v, want 9161250", timeline.Timing.EffectiveGPUTimeNs)
	}
	if got, want := timeline.Timing.DisplayDurationNs, uint64(9_161_250); got != want {
		t.Fatalf("display duration = %d, want %d", got, want)
	}
	if !strings.Contains(timeline.Timing.DisplayDurationSource, "Xcode Overview GPU Time") {
		t.Fatalf("display duration source = %q", timeline.Timing.DisplayDurationSource)
	}
	if !strings.Contains(timeline.Timing.TimingSource, "Xcode Overview GPU Time") {
		t.Fatalf("timing source = %q", timeline.Timing.TimingSource)
	}
}

func TestExportChromeTracingDoesNotMutateTimelineEvents(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 10,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  3,
		}},
		CounterTracks: []CounterTrack{{
			Name: "ALU Utilization",
			Unit: "%",
			Samples: []CounterSample{
				{Timestamp: 20, Value: 42},
			},
		}},
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	if got, want := len(timeline.Events), 1; got != want {
		t.Fatalf("timeline events after export = %d, want %d", got, want)
	}
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("second exportChromeTracing: %v", err)
	}
	if got, want := len(timeline.Events), 1; got != want {
		t.Fatalf("timeline events after second export = %d, want %d", got, want)
	}
}

func TestExportChromeTracingKeepsContainedDispatchOnEncoderTrack(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{
			Name:      "encoder",
			Category:  "encoder",
			Phase:     "X",
			Timestamp: 10,
			Duration:  20,
			ProcessID: 1,
			ThreadID:  1,
		},
		{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 12,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"encoder_containment": "strict",
				"function_name":       "kernel",
				"pipeline_state":      "0x1234",
			},
		},
	}}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	for _, event := range doc.TraceEvents {
		if event.Name == "kernel" && event.Category == "kernel" {
			if got, want := event.ThreadID, 1; got != want {
				t.Fatalf("kernel tid = %d, want encoder tid %d", got, want)
			}
			return
		}
	}
	t.Fatal("missing kernel event")
}

func TestTimelineMetadataForActiveTracks(t *testing.T) {
	events := []TimelineEvent{
		{Name: "process_name", Category: "__metadata", Phase: "M", ProcessID: 1},
		{Name: "thread_name", Category: "__metadata", Phase: "M", ProcessID: 1, ThreadID: 1},
		{Name: "thread_name", Category: "__metadata", Phase: "M", ProcessID: 1, ThreadID: 2},
		{Name: "kernel", Category: "kernel", Phase: "X", ProcessID: 1, ThreadID: 1},
	}

	got := timelineMetadataForActiveTracks(events)
	if len(got) != 3 {
		t.Fatalf("events after filtering = %d, want 3", len(got))
	}
	for _, event := range got {
		if event.Name == "thread_name" && event.ThreadID == 2 {
			t.Fatal("metadata retained an unused track")
		}
	}
}

func TestTimelineClockProvenance(t *testing.T) {
	for _, test := range []struct {
		clock    timelineClock
		included []string
	}{
		{clock: timelineClockBusy, included: []string{"encoder", "kernel", "counter"}},
		{clock: timelineClockWall, included: []string{"command_buffer", "profiler_stream", "gprwcntr"}},
	} {
		t.Run(string(test.clock), func(t *testing.T) {
			args := timelineClockProvenance(test.clock)
			if got := args["clock_domain"]; got != string(test.clock) {
				t.Fatalf("clock_domain = %q, want %q", got, test.clock)
			}
			got := args["included_categories"].([]string)
			if strings.Join(got, ",") != strings.Join(test.included, ",") {
				t.Fatalf("included_categories = %#v, want %#v", got, test.included)
			}
			if args["clock_mapping"] == "" {
				t.Fatal("clock_mapping is empty")
			}
		})
	}
}

func TestExportChromeTracingCounterTrackMetadataIncludesXcodeProvenance(t *testing.T) {
	timeline := &Timeline{CounterTracks: []CounterTrack{{
		Name:             "ALU Utilization",
		Unit:             "Percentage of Peak ALU Performance",
		XcodeGroups:      []string{"ALU"},
		XcodeCatalogPath: "/Applications/Xcode-rc.app/GPUCounterGraph.plist",
		Samples:          []CounterSample{{Timestamp: 20, Value: 42}},
	}}}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	for _, event := range doc.TraceEvents {
		if event.Name != "thread_name" || event.Args["xcode_catalog_path"] == nil {
			continue
		}
		if got, want := event.Args["xcode_catalog_path"], timeline.CounterTracks[0].XcodeCatalogPath; got != want {
			t.Fatalf("catalog path = %q, want %q", got, want)
		}
		groups, ok := event.Args["xcode_groups"].([]interface{})
		if !ok || len(groups) != 1 || groups[0] != "ALU" {
			t.Fatalf("groups = %#v, want [ALU]", event.Args["xcode_groups"])
		}
		return
	}
	t.Fatal("missing counter metadata with Xcode provenance")
}

func TestExportChromeTracingStdoutWritesCleanJSON(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 10,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  3,
		}},
	}

	out, err := captureStdout(t, func() error {
		return exportChromeTracing(timeline, "/dev/stdout")
	})
	if err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}

	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, out)
	}
	if len(doc.TraceEvents) == 0 {
		t.Fatalf("stdout JSON contains no trace events:\n%s", out)
	}
}

func TestTimelineOutputPath(t *testing.T) {
	tests := []struct {
		name   string
		format string
		output string
		want   string
	}{
		{name: "text default", format: "text", want: ""},
		{name: "json default", format: "json", want: "timeline.json"},
		{name: "chrome default", format: "chrome", want: "timeline.json"},
		{name: "perfetto default", format: "perfetto", want: "timeline.json"},
		{name: "html default", format: "html", want: "timeline.html"},
		{name: "html explicit file", format: "html", output: "custom.htm", want: "custom.htm"},
		{name: "text explicit file", format: "text", output: "timeline.txt", want: "timeline.txt"},
		{name: "json stdout", format: "json", output: "/dev/stdout", want: "/dev/stdout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timelineOutputPath(tt.format, tt.output); got != tt.want {
				t.Fatalf("timelineOutputPath(%q, %q) = %q, want %q", tt.format, tt.output, got, tt.want)
			}
		})
	}
}

func TestTimelineDurationPhase(t *testing.T) {
	tests := []struct {
		name string
		dur  uint64
		want string
	}{
		{name: "zero", dur: 0, want: "i"},
		{name: "one microsecond", dur: 1, want: "X"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := timelineDurationPhase(test.dur); got != test.want {
				t.Fatalf("timelineDurationPhase(%d) = %q, want %q", test.dur, got, test.want)
			}
		})
	}
}

func TestValidateTimelineFormat(t *testing.T) {
	for _, format := range []string{"chrome", "perfetto", "html", "json", "text"} {
		t.Run(format, func(t *testing.T) {
			if err := validateTimelineFormat(format); err != nil {
				t.Fatalf("validateTimelineFormat(%q): %v", format, err)
			}
		})
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "empty",
			format: "",
			want:   `invalid timeline format "" (supported: chrome, perfetto, html, json, text)`,
		},
		{
			name:   "uppercase",
			format: "Chrome",
			want:   `invalid timeline format "Chrome" (supported: chrome, perfetto, html, json, text)`,
		},
		{
			name:   "unsupported",
			format: "svg",
			want:   `invalid timeline format "svg" (supported: chrome, perfetto, html, json, text)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTimelineFormat(tt.format)
			if err == nil {
				t.Fatalf("validateTimelineFormat(%q) = nil, want error", tt.format)
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("validateTimelineFormat(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestRunTimelineValidatesFormatBeforeTraceIO(t *testing.T) {
	err := runTimeline(nil, []string{filepath.Join(t.TempDir(), "missing.gputrace")}, &timelineOptions{
		format: "svg",
	})
	if err == nil {
		t.Fatal("runTimeline = nil, want error")
	}
	want := `invalid timeline format "svg" (supported: chrome, perfetto, html, json, text)`
	if got := err.Error(); got != want {
		t.Fatalf("runTimeline error = %q, want %q", got, want)
	}
}

func TestTimelineForClockKeepsOnlyComparableEvents(t *testing.T) {
	timeline := &Timeline{
		StartTime: 99,
		EndTime:   999_999_999,
		Duration:  999_999_900,
		Events: []TimelineEvent{
			{Category: "command_buffer", Timestamp: 300_000},
			{Category: "profiler_stream", Timestamp: 320_000},
			{Category: "gprwcntr", Timestamp: 340_000},
			{Category: "encoder", Timestamp: 0},
			{Category: "kernel", Timestamp: 100},
		},
		Encoders:      []EncoderInfo{{Index: 0}},
		Kernels:       []KernelInfo{{Name: "kernel", Encoder: 0}},
		CounterTracks: []CounterTrack{{Name: "GPU Cycles", Samples: []CounterSample{{Timestamp: 200_000, Value: 1}}}},
	}

	busy := timelineForClock(timeline, timelineClockBusy)
	if got, want := len(busy.Events), 2; got != want {
		t.Fatalf("busy events = %d, want %d", got, want)
	}
	if got, want := len(busy.CounterTracks), 1; got != want {
		t.Fatalf("busy counter tracks = %d, want %d", got, want)
	}
	if got, want := busy.Events[0].Category, "encoder"; got != want {
		t.Fatalf("first busy category = %q, want %q", got, want)
	}
	if got, want := busy.ClockDomain, string(timelineClockBusy); got != want {
		t.Fatalf("busy clock_domain = %q, want %q", got, want)
	}
	if got, want := busy.Duration, uint64(200_000); got != want {
		t.Fatalf("busy duration = %d, want %d", got, want)
	}

	wall := timelineForClock(timeline, timelineClockWall)
	if got, want := len(wall.Events), 3; got != want {
		t.Fatalf("wall events = %d, want %d", got, want)
	}
	if len(wall.Encoders) != 0 || len(wall.Kernels) != 0 || len(wall.CounterTracks) != 0 {
		t.Fatalf("wall timeline retained busy data: %#v", wall)
	}
	for _, event := range wall.Events {
		if event.Category == "encoder" || event.Category == "kernel" {
			t.Fatalf("wall timeline contains busy event: %#v", event)
		}
	}
	if got, want := wall.ClockDomain, string(timelineClockWall); got != want {
		t.Fatalf("wall clock_domain = %q, want %q", got, want)
	}
	if got, want := wall.Duration, uint64(340_000_000); got != want {
		t.Fatalf("wall duration = %d, want %d", got, want)
	}
	if got, want := len(timeline.Events), 5; got != want {
		t.Fatalf("source timeline events = %d, want %d", got, want)
	}
}

func TestTimelineForClockWithoutRawProfilerSamples(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Category: "command_buffer", Timestamp: 300_000},
		{Category: "profiler_stream", Timestamp: 320_000},
		{Category: "gprwcntr", Timestamp: 340_000},
	}}

	wall := timelineForClockWithRawSamples(timeline, timelineClockWall, false)
	if got, want := len(wall.Events), 1; got != want {
		t.Fatalf("wall events without raw samples = %d, want %d", got, want)
	}
	if wall.RawProfilerSamples {
		t.Fatal("wall timeline says raw profiler samples were included")
	}
	for _, event := range wall.Events {
		if event.Category == "gprwcntr" {
			t.Fatalf("wall timeline retained raw profiler record: %#v", event)
		}
	}

	withRaw := timelineForClockWithRawSamples(timeline, timelineClockWall, true)
	if got, want := len(withRaw.Events), 3; got != want {
		t.Fatalf("wall events with raw samples = %d, want %d", got, want)
	}
	if !withRaw.RawProfilerSamples {
		t.Fatal("wall timeline does not record raw profiler samples")
	}
}

func TestTimelineClockProvenanceWithoutRawProfilerSamples(t *testing.T) {
	args := timelineClockProvenanceWithRawSamples(timelineClockWall, false)
	if got := strings.Join(args["included_categories"].([]string), ","); got != "command_buffer" {
		t.Fatalf("included_categories = %q, want command_buffer", got)
	}
	if got := strings.Join(args["excluded_categories"].([]string), ","); !strings.Contains(got, "gprwcntr") {
		t.Fatalf("excluded_categories = %q, want gprwcntr", got)
	}
	if args["raw_profiler_samples"] == "" {
		t.Fatal("raw profiler sample provenance is missing")
	}
}

func TestTimelineClockValidation(t *testing.T) {
	if err := validateTimelineClock(timelineClockBusy); err != nil {
		t.Fatalf("validate busy: %v", err)
	}
	if err := validateTimelineClock(timelineClockWall); err != nil {
		t.Fatalf("validate wall: %v", err)
	}
	if err := validateTimelineClock(timelineClockBoth); err != nil {
		t.Fatalf("validate both: %v", err)
	}
	if err := validateTimelineClock("mixed"); err == nil {
		t.Fatal("validate mixed = nil, want error")
	}
}

func TestExportTimelineBothKeepsClockDomainsSeparate(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{
			{Category: "command_buffer", Timestamp: 300_000, Duration: 10},
			{Category: "encoder", Timestamp: 0, Duration: 10},
			{Category: "kernel", Timestamp: 10, Duration: 5},
		},
		Encoders: []EncoderInfo{{Index: 0, Duration: 10}},
		Kernels:  []KernelInfo{{Name: "kernel", Encoder: 0, Duration: 5}},
	}

	out := filepath.Join(t.TempDir(), "both.json")
	if err := exportTimelineBoth(timeline, "json", out); err != nil {
		t.Fatalf("exportTimelineBoth: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got timelineBothJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got.ClockDomain != string(timelineClockBoth) {
		t.Fatalf("clock_domain = %q, want %q", got.ClockDomain, timelineClockBoth)
	}
	if got.ClockMapping == "" {
		t.Fatal("clock_mapping is empty")
	}
	if got.Busy == nil || got.Wall == nil {
		t.Fatalf("missing clock view: busy=%v wall=%v", got.Busy != nil, got.Wall != nil)
	}
	if got.Busy.ClockDomain != string(timelineClockBusy) || got.Wall.ClockDomain != string(timelineClockWall) {
		t.Fatalf("clock domains = busy %q, wall %q", got.Busy.ClockDomain, got.Wall.ClockDomain)
	}
	if got, want := len(got.Busy.Events), 2; got != want {
		t.Fatalf("busy events = %d, want %d", got, want)
	}
	if got, want := len(got.Wall.Events), 1; got != want {
		t.Fatalf("wall events = %d, want %d", got, want)
	}
}

func TestExportTimelineBothHTMLHasSeparatePanels(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Category: "command_buffer", Timestamp: 300_000, Duration: 10},
		{Category: "encoder", Timestamp: 0, Duration: 10},
	}}
	out := filepath.Join(t.TempDir(), "both.html")
	if err := exportTimelineBoth(timeline, "html", out); err != nil {
		t.Fatalf("exportTimelineBoth: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, want := range []string{"total information view", "GPU busy time", "Wall-clock scheduling", "no measured mapping"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("HTML does not contain %q", want)
		}
	}
}

func TestExportTimelineBothRejectsOneAxisFormats(t *testing.T) {
	err := exportTimelineBoth(&Timeline{}, "perfetto", filepath.Join(t.TempDir(), "both.json"))
	if err == nil || !strings.Contains(err.Error(), "one global time axis") {
		t.Fatalf("exportTimelineBoth perfetto error = %v, want one-axis error", err)
	}
}

func TestExportTextTimelineWritesOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	if err := exportTextTimeline(&Timeline{}, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if got, want := string(data), "No timeline data available.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestExportTextTimelineSyntheticCommandBufferUsesMilliseconds(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	timeline := &Timeline{
		Duration: 10_837_000,
		Encoders: []EncoderInfo{{
			Index:    0,
			Label:    "encoder",
			Duration: 10_837_000,
		}},
	}
	if err := exportTextTimeline(timeline, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "CB#0 [0.0ms, duration=10.84ms]") {
		t.Fatalf("synthetic command buffer has wrong duration: %s", data)
	}
}

func TestExportTextTimelineSummarizesUnitsAndMissingDuration(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	timeline := &Timeline{
		TracePath: "/trace/example.gputrace",
		Events: []TimelineEvent{
			{Name: "CB#0", Category: "command_buffer", Duration: 250, Args: map[string]interface{}{"index": 0}},
			{Name: "CB#1", Category: "command_buffer", Timestamp: 250, Args: map[string]interface{}{"index": 1}},
		},
		Encoders: []EncoderInfo{{Index: 0}},
		Kernels:  []KernelInfo{{Name: "kernel", Encoder: 0}},
		Timing: &TimelineTiming{
			EncoderSpanNs:         1_000_000,
			DispatchSpanNs:        2_000_000,
			CommandBufferActiveNs: 500_000,
			CommandBufferWallNs:   3_000_000,
			EncoderTimingSource:   "profiler",
		},
	}
	if err := exportTextTimeline(timeline, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"Trace: /trace/example.gputrace",
		"Events: 2 command buffers, 1 encoder, 1 kernel dispatch",
		"Timing source: profiler (measured)",
		"Dispatch span: 2.00 ms",
		"Command-buffer active time: 500.00 us",
		"Xcode Effective GPU Time: unavailable",
		"Row units: start and duration are milliseconds",
		"CB#1 [0.2ms, duration unavailable: no end timestamp]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

// The timeline used to place encoder spans built from name-guessed durations
// on the same axis as measured ones, where a span's width reads as its cost.
// A trace with no measured timing now contributes no encoder spans at all.
func TestGenerateTimelineOmitsSpansWhenTimingUnavailable(t *testing.T) {
	tr := &gputrace.Trace{
		Path:        timelineTimingSourceTraceDir(t),
		KernelNames: []string{"block_softmax_float32"},
	}

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}

	if timeline.Timing == nil {
		t.Fatal("timeline timing metadata is nil")
	}
	if got, want := timeline.Timing.EncoderTimingSource, "unavailable"; got != want {
		t.Fatalf("EncoderTimingSource = %q, want %q", got, want)
	}
	if !timeline.Timing.EncoderTimingApproximate {
		t.Fatal("EncoderTimingApproximate = false, want true: an absent measurement is not an exact one")
	}

	if event := firstTimelineEventByCategory(timeline, "encoder"); event != nil {
		t.Fatalf("encoder span emitted for an unmeasured trace: %+v", event.Args)
	}
}

func TestGenerateTimelineAnnotatesExtractedTimingSource(t *testing.T) {
	const label = "encoder_from_capture"
	start := uint64(0x023456789abcdef1)
	end := start + 250_000

	tr := &gputrace.Trace{
		Path:          timelineTimingSourceTraceDir(t),
		CaptureData:   timelineCaptureWithExtractedTiming(label, start, end),
		EncoderLabels: []string{label},
	}

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}

	if timeline.Timing == nil {
		t.Fatal("timeline timing metadata is nil")
	}
	if got, want := timeline.Timing.EncoderTimingSource, "extracted"; got != want {
		t.Fatalf("EncoderTimingSource = %q, want %q", got, want)
	}
	if !timeline.Timing.EncoderTimingApproximate {
		t.Fatal("EncoderTimingApproximate = false, want true")
	}

	event := firstTimelineEventByCategory(timeline, "encoder")
	if event == nil {
		t.Fatal("missing encoder event")
	}
	if got, want := event.Args["timing_source"], "extracted"; got != want {
		t.Fatalf("event timing_source = %v, want %q", got, want)
	}
	if got, want := event.Args["timing_approximate"], true; got != want {
		t.Fatalf("event timing_approximate = %v, want %v", got, want)
	}
	if got, want := event.Args["real_timing"], false; got != want {
		t.Fatalf("event real_timing = %v, want %v", got, want)
	}
}

func TestTimelineTimingSourceHelpersMarkProfilerMeasured(t *testing.T) {
	metrics := &gputrace.TimingMetrics{
		TimingSource:      "profiler",
		TimingApproximate: false,
	}

	args := map[string]interface{}{}
	addTimingMetricsEventArgs(args, metrics)
	if got, want := args["timing_source"], "profiler"; got != want {
		t.Fatalf("timing_source = %v, want %q", got, want)
	}
	if got, want := args["timing_approximate"], false; got != want {
		t.Fatalf("timing_approximate = %v, want %v", got, want)
	}
	if got, want := args["real_timing"], true; got != want {
		t.Fatalf("real_timing = %v, want %v", got, want)
	}

	timeline := &Timeline{}
	annotateTimelineWithTimingMetrics(timeline, metrics)
	timingArgs := timelineTimingArgs(timeline.Timing)
	if got, want := timingArgs["encoder_timing_source"], "profiler"; got != want {
		t.Fatalf("encoder_timing_source = %v, want %q", got, want)
	}
	if got, want := timingArgs["encoder_timing_approximate"], false; got != want {
		t.Fatalf("encoder_timing_approximate = %v, want %v", got, want)
	}
}

func TestAddDispatchKernelEventsIncludesXcodeShaderArgs(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "encoder0",
			Category:  "encoder",
			Phase:     "X",
			ProcessID: 1,
			ThreadID:  2,
			Args:      map[string]interface{}{"index": 0},
		}},
		Encoders: []EncoderInfo{{
			Index:     0,
			Label:     "encoder0",
			Type:      "compute",
			StartTime: 1000,
			EndTime:   21000,
			Duration:  20000,
		}},
	}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{
			PipelineID:             42,
			PipelineAddress:        0xabc,
			FunctionName:           "kernel0",
			TemporaryRegisterCount: 13,
			UniformRegisterCount:   4,
			SpilledBytes:           8,
			InstructionCount:       99,
			ALUInstructionCount:    77,
			FP16InstructionCount:   55,
		}},
		Dispatches: []counter.DispatchInfo{{
			Index:                   2,
			PipelineIndex:           0,
			PipelineID:              42,
			FunctionName:            "kernel0",
			EncoderIndex:            0,
			CumulativeUs:            7,
			DurationUs:              7,
			ProfilingSampleSharePct: 85.25,
			SampleCount:             3,
			SamplingDensity:         0.42,
			StartTicks:              10,
			EndTicks:                20,
		}},
	}
	perfStats := &gputrace.PerfCounterStats{
		ShaderMetrics: []gputrace.ShaderHardwareMetrics{{
			ShaderName:     "kernel0",
			PipelineState:  0xabc,
			SIMDGroups:     128,
			AllocatedRegs:  17,
			HighRegister:   19,
			SpilledBytes:   16,
			ALUUtilization: 71.25,
		}},
	}
	shaderReport := &gputrace.ShaderMetricsReport{
		Shaders: []*gputrace.ShaderMetrics{{
			Name:              "kernel0",
			PercentOfTotal:    88.5,
			TotalThreadgroups: 4096,
			TotalDurationNs:   7000,
		}},
	}
	simd := timelineDispatchSIMDStats{
		byName: map[string]uint64{"kernel0": 4096},
		total:  4096,
	}

	if !addDispatchKernelEvents(timeline, stats, simd, shaderReport, perfStats, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got := len(timeline.Kernels); got != 1 {
		t.Fatalf("kernels = %d, want 1", got)
	}
	if got := len(timeline.Events); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	ev := timeline.Events[1]
	if ev.Name != "kernel0" || ev.Category != "kernel" {
		t.Fatalf("event = %s/%s, want kernel0/kernel", ev.Name, ev.Category)
	}
	if got, want := ev.ThreadID, 2; got != want {
		t.Fatalf("dispatch tid = %d, want encoder tid %d", got, want)
	}
	if got, want := ev.Timestamp, uint64(1); got != want {
		t.Fatalf("timestamp = %d, want %d", got, want)
	}
	if got, want := ev.Duration, uint64(7); got != want {
		t.Fatalf("duration = %d, want %d", got, want)
	}
	checkArg := func(key string, want interface{}) {
		t.Helper()
		if got := ev.Args[key]; got != want {
			t.Fatalf("arg %s = %#v, want %#v", key, got, want)
		}
	}
	checkArg("simd_group_share_pct", 100.0)
	checkArg("profiling_sample_share_estimate_pct", 85.25)
	checkArg("pipeline_state", "0xabc")
	checkArg("simd_groups", uint64(4096))
	checkArg("allocated_registers", 17)
	checkArg("high_register", 19)
	checkArg("spilled_bytes", 16)
	checkArg("instruction_count", 99)
	checkArg("shader_duration_ns", uint64(7000))
	checkArg("gprwcntr_sample_count", 3)
	checkArg("xcode_view", "Shaders")
	checkArg("encoder_containment", "strict")
}

func TestAddDispatchKernelEventsUsesEncoderCounterFallback(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{
			Index:     0,
			Label:     "encoder0",
			Type:      "compute",
			StartTime: 1000,
			EndTime:   21000,
			Duration:  20000,
		}},
	}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{
			PipelineID:             42,
			TemporaryRegisterCount: 13,
		}},
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			PipelineIndex: 0,
			PipelineID:    42,
			EncoderIndex:  0,
			DurationUs:    5,
		}},
	}
	encoderMetrics := []counter.EncoderCounterMetrics{{
		EncoderIndex:       0,
		Attribution:        counter.CounterAttributionEncoder,
		ALUUtilization:     71.25,
		ComputeUtilization: 80,
	}}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, encoderMetrics, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Events[0].Args
	if got, want := args["alu_utilization_pct"], 71.25; got != want {
		t.Fatalf("alu_utilization_pct = %#v, want %#v", got, want)
	}
	if _, ok := args["occupancy_pct"]; ok {
		t.Fatal("occupancy_pct must not be emitted; gputrace cannot measure occupancy")
	}
	if got, want := args["alu_utilization_source"], "encoder counter fallback"; got != want {
		t.Fatalf("alu_utilization_source = %#v, want %#v", got, want)
	}
}

func TestPerfettoRendersUnknownCounterMetricsAsUnattributed(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{
		Index:     0,
		Label:     "encoder0",
		Type:      "compute",
		StartTime: 1000,
		EndTime:   21000,
		Duration:  20000,
	}}}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{PipelineID: 42}},
		Dispatches: []counter.DispatchInfo{{
			Index:        0,
			PipelineID:   42,
			EncoderIndex: 0,
			DurationUs:   5,
		}},
	}
	metrics := []counter.EncoderCounterMetrics{{
		EncoderIndex:   0, // A stale/default index must not override attribution.
		EncoderLabel:   "pipeline0",
		Attribution:    counter.CounterAttributionUnknown,
		ALUUtilization: 71.25,
	}}

	recordUnattributedCounterMetrics(timeline, metrics)
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, metrics, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got, ok := timeline.Events[0].Args["alu_utilization_pct"]; ok {
		t.Fatalf("dispatch received unknown counter value %#v", got)
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, event := range doc.TraceEvents {
		if event.Category != "counter_attribution" {
			continue
		}
		if got, want := event.Args["attribution"], "unknown"; got != want {
			t.Fatalf("attribution = %#v, want %#v", got, want)
		}
		if got, want := event.Args["pipeline_label"], "pipeline0"; got != want {
			t.Fatalf("pipeline_label = %#v, want %#v", got, want)
		}
		if got, want := event.Args["alu_utilization_pct"], 71.25; got != want {
			t.Fatalf("alu_utilization_pct = %#v, want %#v", got, want)
		}
		if _, ok := event.Args["encoder_index"]; ok {
			t.Fatal("unattributed event contains encoder_index")
		}
		return
	}
	t.Fatal("missing counter_attribution event")
}

func TestAddDispatchKernelEventsMarksBoundaryDispatch(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{
		Index:     0,
		Label:     "encoder0",
		Type:      "compute",
		StartTime: 0,
		EndTime:   1000,
		Duration:  1000,
	}}}
	stats := &counter.StreamDataStats{Dispatches: []counter.DispatchInfo{{
		Index:        0,
		PipelineID:   1,
		EncoderIndex: 0,
		DurationUs:   2,
	}}}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got, want := timeline.Events[0].Args["encoder_containment"], "not_strictly_contained"; got != want {
		t.Fatalf("encoder_containment = %v, want %q", got, want)
	}
}

func TestAddDispatchKernelEventsAnnotatesSource(t *testing.T) {
	dir := t.TempDir()
	source := `#include <metal_stdlib>
using namespace metal;

kernel void source_backed_kernel(device float *out [[buffer(0)]],
                                 uint tid [[thread_position_in_grid]]) {
	out[tid] = 1;
}
`
	sourcePath := filepath.Join(dir, "kernels.metal")
	if err := os.WriteFile(sourcePath, []byte(source), 0666); err != nil {
		t.Fatal(err)
	}

	mapper := gputrace.NewShaderSourceMapper(dir)
	if err := mapper.IndexShaderSources(); err != nil {
		t.Fatal(err)
	}
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, StartTime: 1000}},
	}
	stats := &counter.StreamDataStats{
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			FunctionName:  "source_backed_kernel",
			EncoderIndex:  0,
			PipelineIndex: 0,
			DurationUs:    7,
		}},
	}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, nil, mapper) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Events[0].Args
	if got := args["source_available"]; got != true {
		t.Fatalf("source_available = %#v, want true", got)
	}
	if got := args["source_file"]; got != sourcePath {
		t.Fatalf("source_file = %#v, want %q", got, sourcePath)
	}
	if got := args["source_line"]; got != 4 {
		t.Fatalf("source_line = %#v, want 4", got)
	}
}

func TestBuildXcodeParityReport(t *testing.T) {
	timeline := &Timeline{
		Timing: &TimelineTiming{
			TimingSource:          "command buffer active time",
			DisplayDurationSource: "command buffer active time",
		},
		Events: []TimelineEvent{{
			Category: "kernel",
			Args: map[string]interface{}{
				"alu_utilization_pct": 0.0,
				"allocated_registers": 13,
			},
		}},
	}
	report := buildXcodeParityReport("trace.gputrace", timeline, xcodebindingsReportForTest())
	if report.KernelEvents != 1 {
		t.Fatalf("KernelEvents = %d, want 1", report.KernelEvents)
	}
	if len(report.RemainingGaps) == 0 {
		t.Fatal("missing remaining gaps")
	}
	// The fixture's alu_utilization_pct is 0.0, which is what the fallback
	// stamped when nothing had been read. It must land in the gap list.
	if !stringSliceContains(report.AbsentFields, "alu_utilization_pct") {
		t.Fatalf("alu_utilization_pct = 0 must not count as present: %+v", report.AbsentFields)
	}
	for _, example := range report.ClosedExamples {
		if strings.Contains(example, "alu_utilization_pct") {
			t.Fatalf("alu_utilization_pct reported closed on a zero value: %q", example)
		}
	}
}

func TestXcodeParityStreamDataEvidenceReportsSafeNextSteps(t *testing.T) {
	report := xcodeParityReport{
		Timing: map[string]interface{}{},
		RemainingGaps: []xcodeParityGap{
			{Metric: "high_register", Next: "old"},
			{Metric: "alu_utilization_pct", Next: "old"},
			{Metric: "effective_gpu_time", Next: "old"},
		},
		StreamData: &xcodebindings.StreamDataSummary{
			SelectedValues: []xcodebindings.ValueSummary{
				{Key: "Binaries", Count: 734},
				{Key: "Derived Counter Sample Data", Count: 16},
				{Key: "Derived Counters Info Data"},
				{Key: "ReplayerGPUTime", Count: 1},
			},
		},
	}

	report.applyStreamDataEvidence()

	gaps := make(map[string]xcodeParityGap)
	for _, gap := range report.RemainingGaps {
		gaps[gap.Metric] = gap
	}
	if got := gaps["high_register"].Next; !strings.Contains(got, "GTShaderProfilerStreamData") && !strings.Contains(got, "selector") {
		t.Fatalf("high_register next = %q, want runtime selector compatibility warning", got)
	}
	if got := gaps["alu_utilization_pct"].Next; !strings.Contains(got, "counter info dictionary is empty") {
		t.Fatalf("alu_utilization_pct next = %q, want empty counter info warning", got)
	}
	if got := gaps["effective_gpu_time"].Status; got != "archived as zero in Xcode streamData" {
		t.Fatalf("effective_gpu_time status = %q, want archived zero", got)
	}
}

func xcodebindingsReportForTest() xcodebindings.Report {
	return xcodebindings.Report{
		Summary: map[string]int{
			"classes_present":   4,
			"classes_missing":   0,
			"selectors_present": 42,
			"selectors_missing": 0,
		},
		Gaps: []xcodebindings.Gap{
			{
				Metric:  "high_register",
				Binding: "GTMioShaderBinaryData.liveRegisterForInstructionAtIndex:",
				Status:  "binding present; adapter missing",
				Next:    "map shader binary data",
			},
			{
				Metric:  "alu_utilization_pct",
				Binding: "XRGPUAPSDataProcessor derived counters",
				Status:  "binding present; adapter missing",
				Next:    "resolve ALU counter",
			},
		},
	}
}

func TestTimelineDispatchSIMDGroup(t *testing.T) {
	dispatch := tracepkg.DispatchThreads{
		ThreadsX:         1000,
		ThreadsY:         1,
		ThreadsZ:         1,
		ThreadsPerGroupX: 256,
		ThreadsPerGroupY: 1,
		ThreadsPerGroupZ: 1,
	}
	if got, want := dispatch.SIMDGroups(), uint64(32); got != want {
		t.Fatalf("timelineDispatchSIMDGroup = %d, want %d", got, want)
	}
}

func TestGenerateTimelineWithoutPerfDataIncludesDispatchSIMDGroups(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	tr, err := tracepkg.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer tr.Close()

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}
	for _, event := range timeline.Events {
		if event.Category != "kernel" || event.Args == nil {
			continue
		}
		if got, ok := event.Args["simd_groups"].(uint64); ok && got == 32 {
			if source := fmt.Sprint(event.Args["source"]); !strings.Contains(source, "dispatch geometry") {
				t.Fatalf("source = %q, want dispatch geometry", source)
			}
			return
		}
	}
	t.Fatalf("no kernel event with simd_groups=32 in %#v", timeline.Events)
}

func TestGenerateInteractiveHTMLIncludesShaderTooltipFields(t *testing.T) {
	html := generateInteractiveHTML(`{"events":[]}`)
	for _, want := range []string{
		"Profiling Sample Share (estimate)",
		"Pipeline ID",
		"Instructions",
		"ALU Instructions",
		"FP32 Instructions",
		"FP16 Instructions",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("generated HTML missing %q", want)
		}
	}
}

func TestGenerateInteractiveHTMLIncludesEstimatedTimingWarning(t *testing.T) {
	html := generateInteractiveHTML(`{"events":[]}`)
	for _, want := range []string{
		"warning-banner",
		"Precise hardware timing data is unavailable",
		"Estimated Timing",
		"Timing Mode",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("generated HTML missing estimated timing element %q", want)
		}
	}
}

func TestGenerateCounterTracksFromPerfDataUsesEncoderCounters(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{
			Index:     1,
			Label:     "kernel0",
			Type:      "compute",
			StartTime: 100,
			EndTime:   200,
			Duration:  100,
		}},
	}
	encoderMetrics := []counter.EncoderCounterMetrics{{
		EncoderIndex:               1,
		EncoderLabel:               "kernel0",
		Attribution:                counter.CounterAttributionEncoder,
		ALUUtilization:             3.25,
		DeviceMemoryBandwidthGBps:  12.5,
		BytesReadFromDeviceMemory:  500,
		GPUWriteBandwidthGBps:      4.5,
		InstructionThroughputUtil:  2.5,
		ComputeUtilization:         3.25,
		ComputeShaderLaunchLimiter: 0.17,
		L1CacheLimiter:             0.25,
		TextureReadLimiter:         0.5,
		BufferL1MissRate:           1.25,
	}}

	streamStats := &gputrace.StreamDataStats{
		FunctionNames: []string{"kernel0"},
		Pipelines: []gputrace.PipelineStats{{
			FunctionName:           "kernel0",
			TemporaryRegisterCount: 46,
			UniformRegisterCount:   8,
			SpilledBytes:           16,
			ThreadgroupMemory:      1024,
		}},
	}

	tracks := generateCounterTracksFromPerfData(streamStats, encoderMetrics, timeline)
	alu := findCounterTrackForTest(t, tracks, "ALU Utilization")
	if len(alu.Samples) != 2 || alu.Samples[0].Value != 3.25 {
		t.Fatalf("ALU samples = %+v, want two samples at 3.25", alu.Samples)
	}
	bandwidth := findCounterTrackForTest(t, tracks, "Bandwidth")
	if len(bandwidth.Samples) != 2 || bandwidth.Samples[0].Value != 12.5 {
		t.Fatalf("bandwidth samples = %+v, want two samples at 12.5", bandwidth.Samples)
	}
	readBW := findCounterTrackForTest(t, tracks, "Memory Read BW")
	if len(readBW.Samples) != 2 || readBW.Samples[0].Value != 5.0 {
		t.Fatalf("memory read samples = %+v, want two samples at 5.0", readBW.Samples)
	}
	writeBW := findCounterTrackForTest(t, tracks, "Memory Write BW")
	if len(writeBW.Samples) != 2 || writeBW.Samples[0].Value != 4.5 {
		t.Fatalf("memory write samples = %+v, want two samples at 4.5", writeBW.Samples)
	}
	l1Miss := findCounterTrackForTest(t, tracks, "L1 Cache Miss Rate")
	if len(l1Miss.Samples) != 2 || l1Miss.Samples[0].Value != 1.25 {
		t.Fatalf("L1 miss samples = %+v, want two samples at 1.25", l1Miss.Samples)
	}
	computeLimiter := findCounterTrackForTest(t, tracks, "Limiter: Compute")
	if len(computeLimiter.Samples) != 2 || computeLimiter.Samples[0].Value != 0.17 {
		t.Fatalf("compute limiter samples = %+v, want two samples at 0.17", computeLimiter.Samples)
	}
	memoryLimiter := findCounterTrackForTest(t, tracks, "Limiter: Memory")
	if len(memoryLimiter.Samples) != 2 || memoryLimiter.Samples[0].Value != 0.75 {
		t.Fatalf("memory limiter samples = %+v, want two samples at 0.75", memoryLimiter.Samples)
	}

	allocated := findCounterTrackForTest(t, tracks, "Allocated Registers")
	if len(allocated.Samples) != 2 || allocated.Samples[0].Value != 46 {
		t.Fatalf("allocated register samples = %+v, want two samples at 46", allocated.Samples)
	}
	uniform := findCounterTrackForTest(t, tracks, "Uniform Registers")
	if len(uniform.Samples) != 2 || uniform.Samples[0].Value != 8 {
		t.Fatalf("uniform register samples = %+v, want two samples at 8", uniform.Samples)
	}
	spills := findCounterTrackForTest(t, tracks, "Spilled Bytes")
	if len(spills.Samples) != 2 || spills.Samples[0].Value != 16 {
		t.Fatalf("spilled byte samples = %+v, want two samples at 16", spills.Samples)
	}
	tgmem := findCounterTrackForTest(t, tracks, "Threadgroup Memory")
	if len(tgmem.Samples) != 2 || tgmem.Samples[0].Value != 1024 {
		t.Fatalf("threadgroup memory samples = %+v, want two samples at 1024", tgmem.Samples)
	}
}

func TestGenerateCounterTracksFromCounterArchive(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{
		{Index: 0, StartTime: 100, EndTime: 200},
		{Index: 1, StartTime: 300, EndTime: 400},
	}}
	archive := &counter.CounterArchive{Encoders: []counter.EncoderSamples{
		{Ordinal: 0, GPUCycles: 100, EndSamples: 16},
		{Ordinal: 1, GPUCycles: 300, EndSamples: 16},
	}}
	tracks := generateCounterTracksFromCounterArchive(archive, timeline)
	if got, want := len(tracks), 2; got != want {
		t.Fatalf("tracks = %d, want %d", got, want)
	}
	if got, want := tracks[0].Name, "GPU Cycles"; got != want {
		t.Fatalf("cycles track = %q, want %q", got, want)
	}
	if got, want := tracks[1].Name, "Execution Cost"; got != want {
		t.Fatalf("cost track = %q, want %q", got, want)
	}
	if got, want := tracks[1].Samples[0].Value, 25.0; got != want {
		t.Fatalf("first cost = %v, want %v", got, want)
	}
	if got, want := tracks[1].Description, "Derived per encoder from APSCounterData GRC_GPU_CYCLES; not Xcode's exact Execution Cost column."; got != want {
		t.Fatalf("cost description = %q, want %q", got, want)
	}
}

func TestGenerateCounterTracksFromCounterArchiveMarksSparseValues(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{Index: 0, StartTime: 100, EndTime: 200}}}
	archive := &counter.CounterArchive{Encoders: []counter.EncoderSamples{{
		Ordinal:    0,
		GPUCycles:  100,
		EndSamples: 15,
	}}}
	tracks := generateCounterTracksFromCounterArchive(archive, timeline)
	if got, want := len(tracks), 2; got != want {
		t.Fatalf("tracks = %d, want %d", got, want)
	}
	if !strings.Contains(tracks[0].Description, "1 encoder value(s) have fewer than 16 end-counter reads, the minimum for the archive's 16 replay groups") {
		t.Fatalf("cycles description = %q, want sparse-read caveat", tracks[0].Description)
	}
}

func TestGenerateCounterTracksDoesNotEstimateShaderLaunchLimiter(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{
		Index:     0,
		Label:     "kernel0",
		StartTime: 100,
		EndTime:   200,
		Duration:  100,
	}}}
	tracks := generateCounterTracksFromPerfData(nil, nil, timeline)
	limiter := findCounterTrackForTest(t, tracks, "Shader Launch Limiter")
	if counterTrackHasSignal(limiter) {
		t.Fatalf("shader launch limiter = %+v, want no signal without a measured limiter", limiter.Samples)
	}
}

func TestGenerateCounterTracksFromPerfDataKeepsSourceBackedZeroValues(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{
			Index:     0,
			Label:     "kernel0",
			Type:      "compute",
			StartTime: 10,
			EndTime:   20,
			Duration:  10,
		}},
	}
	encoderMetrics := []counter.EncoderCounterMetrics{{
		EncoderIndex: 0,
		EncoderLabel: "kernel0",
		Attribution:  counter.CounterAttributionEncoder,
	}}

	tracks := generateCounterTracksFromPerfData(nil, encoderMetrics, timeline)
	alu := findCounterTrackForTest(t, tracks, "ALU Utilization")
	if len(alu.Samples) != 2 {
		t.Fatalf("ALU samples = %d, want 2", len(alu.Samples))
	}
	if got := alu.Samples[0].Value; got != 0 {
		t.Fatalf("ALU value = %v, want 0", got)
	}
}

// TestDispatchKernelArgsOmitsUnreadEncoderCounters guards against reporting an
// unread counter as a measured zero. An empty EncoderCounterMetrics means the
// counters were not read; Xcode reports ALU Utilization of 1.59% to 3.35% for
// the encoders of qwen25-05b-staticmask-warm-tokens2-4-rep1 where gputrace used
// to emit 0.00 with the label "encoder counter fallback".
func TestDispatchKernelArgsOmitsUnreadEncoderCounters(t *testing.T) {
	args := dispatchKernelArgs(counter.DispatchInfo{}, nil, 0, 0, nil, nil, &counter.EncoderCounterMetrics{}, nil)
	for _, key := range []string{"alu_utilization_pct", "alu_utilization_source"} {
		if got, ok := args[key]; ok {
			t.Fatalf("%s = %#v, want absent: nothing was read into it", key, got)
		}
	}
}

func timelineTimingSourceTraceDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture"), nil, 0o644); err != nil {
		t.Fatalf("write empty capture: %v", err)
	}
	return dir
}

func timelineCaptureWithExtractedTiming(label string, start, end uint64) []byte {
	const labelOffset = 96

	data := make([]byte, 160)
	binary.LittleEndian.PutUint64(data[labelOffset-40:], start)
	copy(data[labelOffset:], label)
	binary.LittleEndian.PutUint64(data[labelOffset+len(label)+8:], end)
	return data
}

func firstTimelineEventByCategory(timeline *Timeline, category string) *TimelineEvent {
	if timeline == nil {
		return nil
	}
	for i := range timeline.Events {
		if timeline.Events[i].Category == category {
			return &timeline.Events[i]
		}
	}
	return nil
}

func findCounterTrackForTest(t *testing.T, tracks []CounterTrack, name string) CounterTrack {
	t.Helper()
	for _, track := range tracks {
		if track.Name == name {
			return track
		}
	}
	t.Fatalf("missing counter track %q", name)
	return CounterTrack{}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAddDispatchKernelEventsJoinsPipelinesByID guards the join between
// gpuCommandInfoData dispatches and pipelinePerformanceStatistics. The latter
// is an NSDictionary, so its slice order does not follow the pipeline index
// carried by the dispatch records; joining positionally attributes one
// kernel's register and instruction counts to another.
func TestAddDispatchKernelEventsJoinsPipelinesByID(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, Label: "encoder0", Type: "compute", StartTime: 1000, EndTime: 21000, Duration: 20000}},
	}
	stats := &counter.StreamDataStats{
		// Dictionary order is the reverse of the pipeline index order.
		Pipelines: []counter.PipelineStats{
			{PipelineID: 458, FunctionName: "other_kernel", InstructionCount: 999},
			{PipelineID: 446, FunctionName: "kernel0", InstructionCount: 12},
		},
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			PipelineIndex: 0,
			PipelineID:    446,
			FunctionName:  "kernel0",
			EncoderIndex:  0,
			DurationUs:    7,
		}},
	}
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Kernels[0].Args
	if got, want := args["function_name"], "kernel0"; got != want {
		t.Fatalf("function_name = %#v, want %#v", got, want)
	}
	if got, want := args["instruction_count"], 12; got != want {
		t.Fatalf("instruction_count = %#v, want %#v", got, want)
	}
}

// TestAddDispatchKernelEventsNamesAgreeWithPipelineID checks the invariant the
// positional join broke, over every emitted event rather than a single
// dispatch: an event's name comes from pipelineStateInfoData by pipeline index
// and its function_name argument comes from pipelinePerformanceStatistics by
// pipeline ID, so the two must name the same kernel. Under the positional join
// this reported, for example, pipeline 458's name next to pipeline_id 446.
func TestAddDispatchKernelEventsNamesAgreeWithPipelineID(t *testing.T) {
	const n = 8
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, Label: "encoder0", Type: "compute", StartTime: 1000, EndTime: 21000, Duration: 20000}},
	}
	stats := &counter.StreamDataStats{}
	for i := 0; i < n; i++ {
		// Dictionary order is rotated relative to the pipeline index order, so
		// every pipeline lands at a different slice position than its index.
		j := (i + 3) % n
		stats.Pipelines = append(stats.Pipelines, counter.PipelineStats{
			PipelineID:       440 + j,
			FunctionName:     fmt.Sprintf("kernel%d", j),
			InstructionCount: 100 + j,
		})
		stats.Dispatches = append(stats.Dispatches, counter.DispatchInfo{
			Index:         i,
			PipelineIndex: i,
			PipelineID:    440 + i,
			FunctionName:  fmt.Sprintf("kernel%d", i),
			EncoderIndex:  0,
			DurationUs:    1,
		})
	}
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if len(timeline.Kernels) != n {
		t.Fatalf("got %d kernels, want %d", len(timeline.Kernels), n)
	}
	check := func(kind, name string, args map[string]interface{}) {
		t.Helper()
		fn, ok := args["function_name"].(string)
		if !ok {
			t.Errorf("%s %q: no function_name argument", kind, name)
			return
		}
		if fn != name {
			t.Errorf("%s pipeline_id=%v: name %q but function_name %q", kind, args["pipeline_id"], name, fn)
		}
		want := 100 + args["pipeline_id"].(int) - 440
		if got := args["instruction_count"]; got != want {
			t.Errorf("%s pipeline_id=%v: instruction_count = %v, want %v", kind, args["pipeline_id"], got, want)
		}
	}
	for _, k := range timeline.Kernels {
		check("kernel", k.Name, k.Args)
	}
	for _, e := range timeline.Events {
		if e.Category == "kernel" {
			check("event", e.Name, e.Args)
		}
	}
}

func TestUnprofiledRawTraceEmitsPhaseIInstantEvents(t *testing.T) {
	rawTrace := "/Users/tmc/tmp/mlx-go-fast/verify/verify-dbg.gputrace"
	if _, err := os.Stat(rawTrace); os.IsNotExist(err) {
		t.Skipf("raw trace fixture absent: %s", rawTrace)
	}

	trace, err := gputrace.Open(rawTrace)
	if err != nil {
		t.Fatalf("open raw trace: %v", err)
	}

	timeline, err := generateTimeline(trace)
	if err != nil {
		t.Fatalf("build timeline: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "unprofiled.json")
	if err := exportChromeTracing(timeline, outPath); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read exported timeline: %v", err)
	}

	var traceDoc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &traceDoc); err != nil {
		t.Fatalf("unmarshal exported timeline: %v", err)
	}

	var phaseXCount, phaseICount int
	for _, event := range traceDoc.TraceEvents {
		if event.Category == "encoder" || event.Category == "kernel" {
			if event.Phase == "X" {
				phaseXCount++
				t.Errorf("unprofiled event %q category=%q has finite Phase X duration %d", event.Name, event.Category, event.Duration)
			} else if event.Phase == "i" {
				phaseICount++
				if event.Duration != 0 {
					t.Errorf("unprofiled instant event %q category=%q has non-zero duration %d", event.Name, event.Category, event.Duration)
				}
			}
		}
	}

	if phaseICount == 0 {
		t.Fatalf("expected Phase 'i' instant events for unprofiled trace, found 0")
	}
	if phaseXCount > 0 {
		t.Fatalf("found %d finite synthetic Phase 'X' events in unprofiled trace export, want 0", phaseXCount)
	}
}
