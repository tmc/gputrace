package replay

import "github.com/tmc/gputrace/internal/counter"

func aggregateReplayCounterSamples(plan *ReplayPlan, samples []counter.CounterSample, gpuFrequency uint64) ([]counter.EncoderCounterMetrics, []counter.DispatchCounterMetrics) {
	if plan == nil || len(samples) == 0 {
		return nil, nil
	}
	return aggregateReplayEncoderCounterSamples(plan, samples, gpuFrequency),
		aggregateReplayDispatchCounterSamples(plan, samples, gpuFrequency)
}

func aggregateReplayEncoderCounterSamples(plan *ReplayPlan, samples []counter.CounterSample, gpuFrequency uint64) []counter.EncoderCounterMetrics {
	metrics := make([]counter.EncoderCounterMetrics, 0, len(plan.Encoders))
	for _, encoder := range plan.Encoders {
		start, okStart := findReplayCounterSample(samples, encoder.Index, -1, "encoder_start")
		end, okEnd := findReplayCounterSample(samples, encoder.Index, -1, "encoder_end")
		if !okStart || !okEnd || !counterSampleResolved(start) || !counterSampleResolved(end) {
			continue
		}

		startTimestamp := sampleTimestamp(start)
		endTimestamp := sampleTimestamp(end)
		durationCycles := timestampDelta(startTimestamp, endTimestamp)

		metric := counter.EncoderCounterMetrics{
			EncoderIndex:        encoder.Index,
			EncoderLabel:        encoder.Label,
			EncoderType:         encoder.Type,
			StartTimestamp:      startTimestamp,
			EndTimestamp:        endTimestamp,
			DurationCycles:      durationCycles,
			Duration:            durationNanos(durationCycles, gpuFrequency),
			VertexUtilization:   averageSampleValue(start, end, "vertexUtilization"),
			FragmentUtilization: averageSampleValue(start, end, "fragmentUtilization"),
			ComputeUtilization:  averageSampleValue(start, end, "computeUtilization"),
			DrawCount:           int(counterDelta(start, end, "drawCount")),
			DispatchCount:       int(counterDelta(start, end, "dispatchCount")),
		}
		if metric.DispatchCount == 0 {
			metric.DispatchCount = computeDispatchCount(plan.EncoderCommands(encoder.Index))
		}
		metrics = append(metrics, metric)
	}
	return metrics
}

func aggregateReplayDispatchCounterSamples(plan *ReplayPlan, samples []counter.CounterSample, gpuFrequency uint64) []counter.DispatchCounterMetrics {
	var metrics []counter.DispatchCounterMetrics
	dispatchIndex := 0
	for _, encoder := range plan.Encoders {
		for commandIndex, cmd := range plan.EncoderCommands(encoder.Index) {
			if cmd.Type != "compute_dispatch" {
				continue
			}

			start, okStart := findReplayCounterSample(samples, encoder.Index, commandIndex, "dispatch_start")
			end, okEnd := findReplayCounterSample(samples, encoder.Index, commandIndex, "dispatch_end")
			if !okStart || !okEnd || !counterSampleResolved(start) || !counterSampleResolved(end) {
				dispatchIndex++
				continue
			}

			startTimestamp := sampleTimestamp(start)
			endTimestamp := sampleTimestamp(end)
			durationCycles := timestampDelta(startTimestamp, endTimestamp)

			metrics = append(metrics, counter.DispatchCounterMetrics{
				DispatchIndex:      dispatchIndex,
				EncoderIndex:       encoder.Index,
				FunctionName:       cmd.FunctionName,
				StartTimestamp:     startTimestamp,
				EndTimestamp:       endTimestamp,
				DurationCycles:     durationCycles,
				Duration:           durationNanos(durationCycles, gpuFrequency),
				ComputeUtilization: averageSampleValue(start, end, "computeUtilization"),
			})
			dispatchIndex++
		}
	}
	return metrics
}

func findReplayCounterSample(samples []counter.CounterSample, encoderIndex, commandIndex int, samplingPoint string) (counter.CounterSample, bool) {
	for _, sample := range samples {
		if sample.EncoderIndex != encoderIndex || sample.SamplingPoint != samplingPoint {
			continue
		}
		if commandIndex >= 0 && sample.CommandIndex != commandIndex {
			continue
		}
		return sample, true
	}
	return counter.CounterSample{}, false
}

func counterSampleResolved(sample counter.CounterSample) bool {
	return sampleTimestamp(sample) != 0 || len(sample.Values) > 0
}

func sampleTimestamp(sample counter.CounterSample) uint64 {
	if sample.Timestamp != 0 {
		return sample.Timestamp
	}
	if value, ok := sample.Values["timestamp"]; ok && value > 0 {
		return uint64(value)
	}
	return 0
}

func timestampDelta(start, end uint64) uint64 {
	if end <= start {
		return 0
	}
	return end - start
}

func durationNanos(cycles, gpuFrequency uint64) uint64 {
	if cycles == 0 || gpuFrequency == 0 {
		return 0
	}
	return uint64(float64(cycles) * 1e9 / float64(gpuFrequency))
}

func averageSampleValue(start, end counter.CounterSample, name string) float64 {
	var total float64
	var count int
	if value, ok := start.Values[name]; ok {
		total += value
		count++
	}
	if value, ok := end.Values[name]; ok {
		total += value
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func counterDelta(start, end counter.CounterSample, name string) float64 {
	endValue, ok := end.Values[name]
	if !ok {
		return 0
	}
	startValue := start.Values[name]
	if endValue >= startValue {
		return endValue - startValue
	}
	return endValue
}

func computeDispatchCount(commands []ReplayCommand) int {
	count := 0
	for _, cmd := range commands {
		if cmd.Type == "compute_dispatch" {
			count++
		}
	}
	return count
}
