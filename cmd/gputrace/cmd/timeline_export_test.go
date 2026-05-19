package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	tracepkg "github.com/tmc/gputrace/internal/trace"
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

	if !addDispatchKernelEvents(timeline, stats, simd, shaderReport, perfStats) {
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
		InstructionThroughputUtil:  2.5,
		ComputeUtilization:         3.25,
		ComputeShaderLaunchLimiter: 0.17,
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
