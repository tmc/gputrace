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
		TraceEvents          []TimelineEvent        `json:"traceEvents"`
		GputraceTiming       map[string]interface{} `json:"gputrace_timing"`
		GputraceXcodeMetrics map[string]interface{} `json:"gputrace_xcode_metrics"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := uint64(doc.GputraceTiming["display_duration_ns"].(float64)); got != effective {
		t.Fatalf("gputrace_timing display_duration_ns = %d, want %d", got, effective)
	}
	if got := doc.GputraceXcodeMetrics["has_effective_gpu_time"]; got != true {
		t.Fatalf("has_effective_gpu_time = %v, want true", got)
	}
	bindings := doc.GputraceXcodeMetrics["binding_candidates"].(map[string]interface{})
	if got, want := bindings["high_register"], "GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex"; got != want {
		t.Fatalf("binding candidate high_register = %v, want %q", got, want)
	}

	var foundSummary, foundDuration, foundCoverage bool
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
	if !foundDuration {
		t.Fatal("missing Xcode Display Duration event")
	}
	if !foundCoverage {
		t.Fatal("missing Xcode Metrics Coverage event")
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

func TestExportTextTimelineWritesOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	if err := exportTextTimeline(&Timeline{}, out); err != nil {
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

func TestGenerateTimelineAnnotatesSyntheticTimingSource(t *testing.T) {
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
	if got, want := timeline.Timing.EncoderTimingSource, "synthetic"; got != want {
		t.Fatalf("EncoderTimingSource = %q, want %q", got, want)
	}
	if !timeline.Timing.EncoderTimingApproximate {
		t.Fatal("EncoderTimingApproximate = false, want true")
	}

	event := firstTimelineEventByCategory(timeline, "encoder")
	if event == nil {
		t.Fatal("missing encoder event")
	}
	if got, want := event.Args["timing_source"], "synthetic"; got != want {
		t.Fatalf("event timing_source = %v, want %q", got, want)
	}
	if got, want := event.Args["timing_approximate"], true; got != want {
		t.Fatalf("event timing_approximate = %v, want %v", got, want)
	}
	if got, want := event.Args["real_timing"], false; got != want {
		t.Fatalf("event real_timing = %v, want %v", got, want)
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
			Index:            2,
			PipelineIndex:    0,
			PipelineID:       42,
			FunctionName:     "kernel0",
			EncoderIndex:     0,
			CumulativeUs:     7,
			DurationUs:       7,
			ExecutionCostPct: 85.25,
			SampleCount:      3,
			SamplingDensity:  0.42,
			StartTicks:       10,
			EndTicks:         20,
		}},
	}
	perfStats := &gputrace.PerfCounterStats{
		ShaderMetrics: []gputrace.ShaderHardwareMetrics{{
			ShaderName:      "kernel0",
			PipelineState:   0xabc,
			SIMDGroups:      128,
			AllocatedRegs:   17,
			HighRegister:    19,
			SpilledBytes:    16,
			KernelOccupancy: 62.5,
			ALUUtilization:  71.25,
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
	if got := len(timeline.Events); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	ev := timeline.Events[0]
	if ev.Name != "kernel0" || ev.Category != "kernel" {
		t.Fatalf("event = %s/%s, want kernel0/kernel", ev.Name, ev.Category)
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
	checkArg("xcode_cost_pct", 100.0)
	checkArg("profiling_cost_pct", 85.25)
	checkArg("pipeline_state", "0xabc")
	checkArg("simd_groups", uint64(4096))
	checkArg("allocated_registers", 17)
	checkArg("high_register", 19)
	checkArg("spilled_bytes", 16)
	checkArg("instruction_count", 99)
	checkArg("shader_duration_ns", uint64(7000))
	checkArg("gprwcntr_sample_count", 3)
	checkArg("xcode_view", "Shaders")
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
		KernelOccupancy:    62.5,
		ALUUtilization:     71.25,
		ComputeUtilization: 80,
	}}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, encoderMetrics, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Events[0].Args
	if got, want := args["occupancy_pct"], 62.5; got != want {
		t.Fatalf("occupancy_pct = %#v, want %#v", got, want)
	}
	if got, want := args["alu_utilization_pct"], 71.25; got != want {
		t.Fatalf("alu_utilization_pct = %#v, want %#v", got, want)
	}
	if got, want := args["occupancy_source"], "encoder counter fallback"; got != want {
		t.Fatalf("occupancy_source = %#v, want %#v", got, want)
	}
	if got, want := args["alu_utilization_source"], "encoder counter fallback"; got != want {
		t.Fatalf("alu_utilization_source = %#v, want %#v", got, want)
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
				"occupancy_pct":       62.5,
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
	for _, gap := range report.RemainingGaps {
		if gap.Metric == "occupancy_pct" {
			t.Fatalf("occupancy_pct should be closed: %+v", report.RemainingGaps)
		}
		if gap.Metric == "alu_utilization_pct" {
			t.Fatalf("alu_utilization_pct should be closed: %+v", report.RemainingGaps)
		}
	}
	if !stringSliceContains(report.ClosedExamples, "alu_utilization_pct present on kernel events") {
		t.Fatalf("missing closed alu example: %+v", report.ClosedExamples)
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
	if got := gaps["high_register"].Next; !strings.Contains(got, "nil-parent constructor path is unsafe") {
		t.Fatalf("high_register next = %q, want unsafe constructor warning", got)
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
			{
				Metric:  "occupancy_pct",
				Binding: "XRGPUAPSDataProcessor derived counters",
				Status:  "binding present; adapter missing",
				Next:    "resolve occupancy counter",
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
	if got, want := timelineDispatchSIMDGroup(dispatch), uint64(32); got != want {
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
		"Profiling Cost",
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
		KernelOccupancy:            0.81,
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

	tracks := generateCounterTracksFromPerfData(&gputrace.PerfCounterStats{}, streamStats, encoderMetrics, timeline)
	occupancy := findCounterTrackForTest(t, tracks, "Occupancy")
	if len(occupancy.Samples) != 2 {
		t.Fatalf("occupancy samples = %d, want 2", len(occupancy.Samples))
	}
	if got := occupancy.Samples[0].Timestamp; got != uint64(100) {
		t.Fatalf("occupancy first timestamp = %d, want 100", got)
	}
	if got := occupancy.Samples[1].Timestamp; got != uint64(200) {
		t.Fatalf("occupancy second timestamp = %d, want 200", got)
	}
	if got := occupancy.Samples[0].Value; got != 0.81 {
		t.Fatalf("occupancy value = %v, want 0.81", got)
	}

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

	activeCores := findCounterTrackForTest(t, tracks, "Active Cores")
	if len(activeCores.Samples) != 0 {
		t.Fatalf("active cores samples = %+v, want none", activeCores.Samples)
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
	}}

	tracks := generateCounterTracksFromPerfData(&gputrace.PerfCounterStats{}, nil, encoderMetrics, timeline)
	alu := findCounterTrackForTest(t, tracks, "ALU Utilization")
	if len(alu.Samples) != 2 {
		t.Fatalf("ALU samples = %d, want 2", len(alu.Samples))
	}
	if got := alu.Samples[0].Value; got != 0 {
		t.Fatalf("ALU value = %v, want 0", got)
	}
}

func TestDispatchKernelArgsKeepsSourceBackedZeroEncoderCounters(t *testing.T) {
	args := dispatchKernelArgs(counter.DispatchInfo{}, nil, 0, 0, nil, nil, &counter.EncoderCounterMetrics{}, nil)
	if got, ok := args["occupancy_pct"]; !ok || got != float64(0) {
		t.Fatalf("occupancy_pct = %#v, %v, want source-backed zero", got, ok)
	}
	if got, ok := args["alu_utilization_pct"]; !ok || got != float64(0) {
		t.Fatalf("alu_utilization_pct = %#v, %v, want source-backed zero", got, ok)
	}
	if got, want := args["occupancy_source"], "encoder counter fallback"; got != want {
		t.Fatalf("occupancy_source = %#v, want %#v", got, want)
	}
	if got, want := args["alu_utilization_source"], "encoder counter fallback"; got != want {
		t.Fatalf("alu_utilization_source = %#v, want %#v", got, want)
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
