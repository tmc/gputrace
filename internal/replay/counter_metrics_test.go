package replay

import (
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func TestAggregateReplayCounterSamplesFailsClosedForPlaceholders(t *testing.T) {
	plan := &ReplayPlan{
		Encoders: []ReplayEncoderInfo{
			{Index: 0, Type: "compute", Label: "encoder", CommandCount: 1},
		},
		Commands: []ReplayCommand{
			{Type: "compute_dispatch", EncoderIndex: 0, FunctionName: "kernel"},
		},
	}
	samples := []counter.CounterSample{
		{Index: 0, EncoderIndex: 0, CommandIndex: -1, SamplingPoint: "encoder_start"},
		{Index: 1, EncoderIndex: 0, CommandIndex: 0, SamplingPoint: "encoder_end"},
		{Index: 2, EncoderIndex: 0, CommandIndex: 0, SamplingPoint: "dispatch_start"},
		{Index: 3, EncoderIndex: 0, CommandIndex: 0, SamplingPoint: "dispatch_end"},
	}

	encoderMetrics, dispatchMetrics := aggregateReplayCounterSamples(plan, samples, 0)
	if len(encoderMetrics) != 0 {
		t.Fatalf("len(encoderMetrics) = %d, want 0", len(encoderMetrics))
	}
	if len(dispatchMetrics) != 0 {
		t.Fatalf("len(dispatchMetrics) = %d, want 0", len(dispatchMetrics))
	}
}

func TestAggregateReplayCounterSamplesUsesResolvedBoundaries(t *testing.T) {
	plan := &ReplayPlan{
		Encoders: []ReplayEncoderInfo{
			{Index: 0, Type: "compute", Label: "encoder", CommandCount: 1},
		},
		Commands: []ReplayCommand{
			{Type: "compute_dispatch", EncoderIndex: 0, FunctionName: "kernel"},
		},
	}
	samples := []counter.CounterSample{
		{
			Index:         0,
			Timestamp:     100,
			EncoderIndex:  0,
			CommandIndex:  -1,
			SamplingPoint: "encoder_start",
			Values: map[string]float64{
				"computeUtilization": 10,
				"dispatchCount":      0,
			},
		},
		{
			Index:         1,
			Timestamp:     160,
			EncoderIndex:  0,
			CommandIndex:  0,
			SamplingPoint: "encoder_end",
			Values: map[string]float64{
				"computeUtilization": 30,
				"dispatchCount":      1,
			},
		},
		{
			Index:         2,
			Timestamp:     120,
			EncoderIndex:  0,
			CommandIndex:  0,
			SamplingPoint: "dispatch_start",
			Values: map[string]float64{
				"computeUtilization": 20,
			},
		},
		{
			Index:         3,
			Timestamp:     150,
			EncoderIndex:  0,
			CommandIndex:  0,
			SamplingPoint: "dispatch_end",
			Values: map[string]float64{
				"computeUtilization": 40,
			},
		},
	}

	encoderMetrics, dispatchMetrics := aggregateReplayCounterSamples(plan, samples, 1_000_000_000)
	if got, want := len(encoderMetrics), 1; got != want {
		t.Fatalf("len(encoderMetrics) = %d, want %d", got, want)
	}
	encoder := encoderMetrics[0]
	if got, want := encoder.DurationCycles, uint64(60); got != want {
		t.Fatalf("encoder DurationCycles = %d, want %d", got, want)
	}
	if got, want := encoder.Duration, uint64(60); got != want {
		t.Fatalf("encoder Duration = %d, want %d", got, want)
	}
	if got, want := encoder.ComputeUtilization, 20.0; got != want {
		t.Fatalf("encoder ComputeUtilization = %f, want %f", got, want)
	}
	if got, want := encoder.DispatchCount, 1; got != want {
		t.Fatalf("encoder DispatchCount = %d, want %d", got, want)
	}

	if got, want := len(dispatchMetrics), 1; got != want {
		t.Fatalf("len(dispatchMetrics) = %d, want %d", got, want)
	}
	dispatch := dispatchMetrics[0]
	if got, want := dispatch.FunctionName, "kernel"; got != want {
		t.Fatalf("dispatch FunctionName = %q, want %q", got, want)
	}
	if got, want := dispatch.DurationCycles, uint64(30); got != want {
		t.Fatalf("dispatch DurationCycles = %d, want %d", got, want)
	}
	if got, want := dispatch.ComputeUtilization, 30.0; got != want {
		t.Fatalf("dispatch ComputeUtilization = %f, want %f", got, want)
	}
}
