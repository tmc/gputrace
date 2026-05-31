package shader

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tmc/gputrace/internal/counter"
)

func TestCorrelationTimingsFromShaderMetricsRequireTimingSource(t *testing.T) {
	report := &ShaderMetricsReport{
		Shaders: []*ShaderMetrics{
			{
				Name:            "kernel_a",
				InvocationCount: 2,
				TotalDurationNs: 6_000,
				AvgDurationNs:   3_000,
				MinDurationNs:   2_000,
				MaxDurationNs:   4_000,
				TimingSource:    timingSourceStreamDataDispatch,
			},
			{
				Name:            "missing_source",
				InvocationCount: 1,
				TotalDurationNs: 1_000,
				AvgDurationNs:   1_000,
			},
		},
	}

	timings := correlationTimingsFromShaderMetrics(report)
	if got, want := len(timings), 1; got != want {
		t.Fatalf("len(timings) = %d, want %d", got, want)
	}
	if got := timings[0].Name; got != "kernel_a" {
		t.Fatalf("timing name = %q, want kernel_a", got)
	}
	if got := timings[0].TimingSource; got != timingSourceStreamDataDispatch {
		t.Fatalf("TimingSource = %q, want %q", got, timingSourceStreamDataDispatch)
	}
}

func TestCorrelateByNamePropagatesApproximateTimingSource(t *testing.T) {
	timingMap := buildTimingMap([]*correlationTiming{
		{
			Name:            "kernel_a",
			InvocationCount: 2,
			TotalDuration:   10 * time.Microsecond,
			AvgDuration:     5 * time.Microsecond,
			MinDuration:     4 * time.Microsecond,
			MaxDuration:     6 * time.Microsecond,
			TimingSource:    timingSourceCaptureHeuristic,
			TimingApprox:    true,
		},
	})
	hardwareMap := map[string]*counter.ShaderHardwareMetrics{
		"kernel_a": {
			ShaderName:     "kernel_a",
			ALUUtilization: 75,
			TotalCycles:    10_000,
		},
	}
	report := &ShaderCorrelationReport{Shaders: make([]*CorrelatedShaderMetrics, 0)}

	correlateByName(timingMap, hardwareMap, report)

	if got, want := len(report.Shaders), 1; got != want {
		t.Fatalf("len(report.Shaders) = %d, want %d", got, want)
	}
	shader := report.Shaders[0]
	if got := shader.TimingSource; got != timingSourceCaptureHeuristic {
		t.Fatalf("TimingSource = %q, want %q", got, timingSourceCaptureHeuristic)
	}
	if !shader.TimingApprox {
		t.Fatal("TimingApprox = false, want true")
	}
	if got, want := shader.CyclesPerInvocation, uint64(5_000); got != want {
		t.Fatalf("CyclesPerInvocation = %d, want %d", got, want)
	}
	if shader.EstimatedGPUFreqGHz != 0 {
		t.Fatalf("EstimatedGPUFreqGHz = %f, want 0 for approximate timing", shader.EstimatedGPUFreqGHz)
	}
}

func TestCorrelateByNameComputesFrequencyForStreamDataTiming(t *testing.T) {
	timingMap := buildTimingMap([]*correlationTiming{
		{
			Name:            "kernel_a",
			InvocationCount: 2,
			TotalDuration:   10 * time.Microsecond,
			AvgDuration:     5 * time.Microsecond,
			MinDuration:     5 * time.Microsecond,
			MaxDuration:     5 * time.Microsecond,
			TimingSource:    timingSourceStreamDataDispatch,
		},
	})
	hardwareMap := map[string]*counter.ShaderHardwareMetrics{
		"kernel_a": {
			ShaderName:  "kernel_a",
			TotalCycles: 20_000,
		},
	}
	report := &ShaderCorrelationReport{Shaders: make([]*CorrelatedShaderMetrics, 0)}

	correlateByName(timingMap, hardwareMap, report)

	if got := report.Shaders[0].EstimatedGPUFreqGHz; math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("EstimatedGPUFreqGHz = %f, want 2.0", got)
	}
}

func TestCalculateCorrelationSummaryCountsMetricsIndependently(t *testing.T) {
	report := &ShaderCorrelationReport{
		CorrelatedShaders: 3,
		Shaders: []*CorrelatedShaderMetrics{
			{
				ShaderName:  "cycles_only",
				TotalCycles: 1_000,
			},
			{
				ShaderName:            "kernel_a",
				ALUUtilization:        80,
				KernelOccupancy:       50,
				TotalCycles:           2_000,
				EstimatedGPUFreqGHz:   1.5,
				CorrelationConfidence: 1,
			},
			{
				ShaderName:            "kernel_b",
				KernelOccupancy:       70,
				TotalCycles:           3_000,
				EstimatedGPUFreqGHz:   2.5,
				CorrelationConfidence: 1,
			},
		},
	}

	calculateCorrelationSummary(report)

	if got, want := report.TotalGPUCycles, uint64(6_000); got != want {
		t.Fatalf("TotalGPUCycles = %d, want %d", got, want)
	}
	if got, want := report.AvgALUUtilization, 80.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("AvgALUUtilization = %f, want %f", got, want)
	}
	if got, want := report.AvgKernelOccupancy, 60.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("AvgKernelOccupancy = %f, want %f", got, want)
	}
	if got, want := report.EstimatedGPUFreqGHz, 2.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("EstimatedGPUFreqGHz = %f, want %f", got, want)
	}
	if got, want := report.CorrelationRate, 100.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("CorrelationRate = %f, want %f", got, want)
	}

	out := FormatCorrelationReport(report)
	if !strings.Contains(out, "Total GPU Cycles: 6000") {
		t.Fatalf("formatted report missing total cycles:\n%s", out)
	}
	if !strings.Contains(out, "Estimated GPU Frequency: 2.00 GHz") {
		t.Fatalf("formatted report missing averaged frequency:\n%s", out)
	}
}

func TestFormatCorrelationReportDisplaysTimingSource(t *testing.T) {
	report := &ShaderCorrelationReport{
		TraceSource:        "trace.gputrace",
		ProfilerSource:     "(not available)",
		TotalShaders:       1,
		CorrelatedShaders:  1,
		CorrelationRate:    100,
		AvgALUUtilization:  50,
		AvgKernelOccupancy: 25,
		Shaders: []*CorrelatedShaderMetrics{
			{
				ShaderName:            "kernel_a",
				ExecutionCount:        1,
				AvgDuration:           time.Microsecond,
				TimingSource:          timingSourceSyntheticThread,
				TimingApprox:          true,
				ALUUtilization:        50,
				KernelOccupancy:       25,
				CorrelationMethod:     "timing-only",
				CorrelationConfidence: 1,
			},
		},
	}

	out := FormatCorrelationReport(report)
	if !strings.Contains(out, "Timing Sources: "+timingSourceSyntheticThread+" (approximate)") {
		t.Fatalf("formatted report missing approximate timing source:\n%s", out)
	}
	if !strings.Contains(out, "duration-derived frequency is omitted") {
		t.Fatalf("formatted report missing approximate timing note:\n%s", out)
	}
}
