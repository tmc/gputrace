package shader

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

func TestApplyStreamDataDispatchTimingAggregatesDurations(t *testing.T) {
	metricsMap := map[string]*ShaderMetrics{
		"Encoder_5_complex_math": {
			Name:            "Encoder_5_complex_math",
			InvocationCount: 1,
			MinDurationNs:   ^uint64(0),
		},
	}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{
			{
				PipelineID:             27,
				PipelineAddress:        0xabc,
				FunctionName:           "complex_math",
				InstructionCount:       123,
				TemporaryRegisterCount: 64,
				SpilledBytes:           128,
			},
			{
				PipelineID:             28,
				PipelineAddress:        0xdef,
				FunctionName:           "simple_add",
				InstructionCount:       12,
				TemporaryRegisterCount: 16,
			},
		},
		Dispatches: []counter.DispatchInfo{
			{Index: 0, PipelineIndex: 0, PipelineID: 27, FunctionName: "complex_math", DurationUs: 100},
			{Index: 1, PipelineIndex: 0, PipelineID: 27, FunctionName: "complex_math", DurationUs: 250},
			{Index: 2, PipelineIndex: 1, PipelineID: 28, FunctionName: "simple_add", DurationUs: 50},
		},
	}

	if ok := applyStreamDataDispatchTiming(stats, metricsMap); !ok {
		t.Fatal("applyStreamDataDispatchTiming returned false")
	}

	complex := metricsMap["Encoder_5_complex_math"]
	if got, want := complex.InvocationCount, 2; got != want {
		t.Fatalf("complex InvocationCount = %d, want %d", got, want)
	}
	if got, want := complex.TotalDurationNs, uint64(350_000); got != want {
		t.Fatalf("complex TotalDurationNs = %d, want %d", got, want)
	}
	if got, want := complex.AvgDurationNs, uint64(175_000); got != want {
		t.Fatalf("complex AvgDurationNs = %d, want %d", got, want)
	}
	if got, want := complex.MinDurationNs, uint64(100_000); got != want {
		t.Fatalf("complex MinDurationNs = %d, want %d", got, want)
	}
	if got, want := complex.MaxDurationNs, uint64(250_000); got != want {
		t.Fatalf("complex MaxDurationNs = %d, want %d", got, want)
	}
	if got := complex.TimingSource; got != timingSourceStreamDataDispatch {
		t.Fatalf("complex TimingSource = %q, want %q", got, timingSourceStreamDataDispatch)
	}
	if complex.TimingApprox {
		t.Fatal("complex timing should not be approximate")
	}
	if got, want := complex.AllocatedRegisters, 64; got != want {
		t.Fatalf("complex AllocatedRegisters = %d, want %d", got, want)
	}

	simple := metricsMap["simple_add"]
	if simple == nil {
		t.Fatal("simple_add metric was not created")
	}
	if got, want := simple.TotalDurationNs, uint64(50_000); got != want {
		t.Fatalf("simple TotalDurationNs = %d, want %d", got, want)
	}
	if got, want := simple.Address, uint64(0xdef); got != want {
		t.Fatalf("simple Address = %#x, want %#x", got, want)
	}
}

func TestPopulateFallbackTimingMetricsMarksCaptureHeuristic(t *testing.T) {
	const label = "kernel_a"
	data := make([]byte, 160)
	labelOffset := 100
	copy(data[labelOffset:], label)
	start := uint64(10_000_000_000_000_123)
	end := start + 5_000
	binary.LittleEndian.PutUint64(data[labelOffset-40:], start)
	binary.LittleEndian.PutUint64(data[labelOffset+len(label):], end)

	metricsMap := map[string]*ShaderMetrics{
		label: {
			Name:            label,
			InvocationCount: 1,
			MinDurationNs:   ^uint64(0),
		},
	}

	populateFallbackTimingMetrics(&trace.Trace{
		CaptureData:   data,
		EncoderLabels: []string{label},
	}, metricsMap)

	metrics := metricsMap[label]
	if got, want := metrics.TotalDurationNs, uint64(5_000); got != want {
		t.Fatalf("TotalDurationNs = %d, want %d", got, want)
	}
	if got := metrics.TimingSource; got != timingSourceCaptureHeuristic {
		t.Fatalf("TimingSource = %q, want %q", got, timingSourceCaptureHeuristic)
	}
	if !metrics.TimingApprox {
		t.Fatal("capture heuristic timing should be marked approximate")
	}
}

func TestPopulateFallbackTimingMetricsMarksSyntheticThreadEstimate(t *testing.T) {
	metricsMap := map[string]*ShaderMetrics{
		"unknown_kernel": {
			Name:             "unknown_kernel",
			InvocationCount:  2,
			ThreadgroupsX:    2,
			ThreadgroupsY:    1,
			ThreadgroupsZ:    1,
			ThreadsPerGroupX: 64,
			ThreadsPerGroupY: 1,
			ThreadsPerGroupZ: 1,
			MinDurationNs:    ^uint64(0),
		},
	}

	populateFallbackTimingMetrics(&trace.Trace{}, metricsMap)

	metrics := metricsMap["unknown_kernel"]
	if got, want := metrics.TotalDurationNs, uint64(200_000); got != want {
		t.Fatalf("TotalDurationNs = %d, want %d", got, want)
	}
	if got, want := metrics.AvgDurationNs, uint64(100_000); got != want {
		t.Fatalf("AvgDurationNs = %d, want %d", got, want)
	}
	if got := metrics.TimingSource; got != timingSourceSyntheticThread {
		t.Fatalf("TimingSource = %q, want %q", got, timingSourceSyntheticThread)
	}
	if !metrics.TimingApprox {
		t.Fatal("synthetic thread estimate should be marked approximate")
	}
}
