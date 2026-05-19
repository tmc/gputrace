package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
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

	if !addDispatchKernelEvents(timeline, stats, shaderReport, perfStats) {
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
	checkArg("xcode_cost_pct", 88.5)
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
