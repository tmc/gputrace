package export

import (
	"fmt"
	"time"

	"github.com/google/pprof/profile"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/timing"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases
var NewTimingMetricsExtractor = timing.NewTimingMetricsExtractor

// ToPprofWithMetrics converts GPU trace timing metrics to pprof format with improved accuracy.
// This version aggregates by kernel name and provides better statistical representation.
func ToPprofWithMetrics(t *trace.Trace) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	// Set timing info
	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano() // Use current time as base
	}

	// Create root node
	gpuTraceFunc := &profile.Function{
		ID:         1,
		Name:       "GPU",
		SystemName: "GPU",
		Filename:   t.Path,
	}
	gpuTraceLoc := &profile.Location{
		ID:   1,
		Line: []profile.Line{{Function: gpuTraceFunc}},
	}

	// Create command queue node
	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "MTLCommandQueue"
	}

	queueFunc := &profile.Function{
		ID:         2,
		Name:       queueLabel,
		SystemName: queueLabel,
	}
	queueLoc := &profile.Location{
		ID:   2,
		Line: []profile.Line{{Function: queueFunc}},
	}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	// Add kernel samples (aggregated by name)
	nextID := uint64(3)
	for _, kt := range metrics.KernelTimings {
		fnID := nextID
		locID := nextID
		nextID++

		// Create function for this kernel
		fn := &profile.Function{
			ID:         fnID,
			Name:       kt.Name,
			SystemName: kt.Name,
			Filename:   "metal",
		}
		prof.Function = append(prof.Function, fn)

		// Create location
		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: 1}},
		}
		prof.Location = append(prof.Location, loc)

		// Create sample with aggregated timing
		// Use total duration across all invocations
		sample := &profile.Sample{
			// Stack: kernel -> queue -> GPU root (leaf to root)
			Location: []*profile.Location{loc, queueLoc, gpuTraceLoc},
			Value:    []int64{kt.TotalDuration.Nanoseconds()},
			Label: map[string][]string{
				"invocations": {fmt.Sprintf("%d", kt.InvocationCount)},
			},
			NumLabel: map[string][]int64{
				"count":   {int64(kt.InvocationCount)},
				"avg_ns":  {kt.AvgDuration.Nanoseconds()},
				"min_ns":  {kt.MinDuration.Nanoseconds()},
				"max_ns":  {kt.MaxDuration.Nanoseconds()},
				"p50_ns":  {kt.P50Duration.Nanoseconds()},
				"p95_ns":  {kt.P95Duration.Nanoseconds()},
				"p99_ns":  {kt.P99Duration.Nanoseconds()},
				"percent": {int64(kt.PercentOfTotal * 100)}, // Store as basis points
			},
		}
		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// ToPprofFlat converts GPU trace timing to a flat pprof format without hierarchy.
// This is useful for seeing kernel costs without the GPU/Queue overhead.
func ToPprofFlat(t *trace.Trace) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	// Set timing info
	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
	}

	// Add kernel samples (flat, no hierarchy)
	nextID := uint64(1)
	for _, kt := range metrics.KernelTimings {
		fnID := nextID
		locID := nextID
		nextID++

		// Create function for this kernel
		fn := &profile.Function{
			ID:         fnID,
			Name:       kt.Name,
			SystemName: kt.Name,
			Filename:   "metal",
		}
		prof.Function = append(prof.Function, fn)

		// Create location
		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: 1}},
		}
		prof.Location = append(prof.Location, loc)

		// Create sample (flat - just the kernel)
		sample := &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{kt.TotalDuration.Nanoseconds()},
			Label: map[string][]string{
				"invocations": {fmt.Sprintf("%d", kt.InvocationCount)},
			},
			NumLabel: map[string][]int64{
				"count":   {int64(kt.InvocationCount)},
				"avg_ns":  {kt.AvgDuration.Nanoseconds()},
				"percent": {int64(kt.PercentOfTotal * 100)},
			},
		}
		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// ToPprofPerInvocation creates a pprof profile with one sample per kernel invocation.
// This preserves timing variance and shows the distribution of execution times.
func ToPprofPerInvocation(t *trace.Trace) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
	}

	// Create root
	gpuTraceFunc := &profile.Function{
		ID:         1,
		Name:       "GPU",
		SystemName: "GPU",
		Filename:   t.Path,
	}
	gpuTraceLoc := &profile.Location{
		ID:   1,
		Line: []profile.Line{{Function: gpuTraceFunc}},
	}

	queueFunc := &profile.Function{
		ID:         2,
		Name:       t.CommandQueueLabel,
		SystemName: t.CommandQueueLabel,
	}
	queueLoc := &profile.Location{
		ID:   2,
		Line: []profile.Line{{Function: queueFunc}},
	}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	// Create function and location for each unique kernel
	kernelFuncs := make(map[string]*profile.Function)
	kernelLocs := make(map[string]*profile.Location)
	nextID := uint64(3)

	for _, kt := range metrics.KernelTimings {
		fnID := nextID
		locID := nextID
		nextID++

		fn := &profile.Function{
			ID:         fnID,
			Name:       kt.Name,
			SystemName: kt.Name,
			Filename:   "metal",
		}
		prof.Function = append(prof.Function, fn)
		kernelFuncs[kt.Name] = fn

		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: 1}},
		}
		prof.Location = append(prof.Location, loc)
		kernelLocs[kt.Name] = loc
	}

	// Add one sample per invocation
	invocationNum := make(map[string]int)
	for _, kt := range metrics.KernelTimings {
		loc := kernelLocs[kt.Name]

		// Create a sample for each invocation
		for _, duration := range kt.Durations {
			invocationNum[kt.Name]++

			sample := &profile.Sample{
				Location: []*profile.Location{loc, queueLoc, gpuTraceLoc},
				Value:    []int64{duration.Nanoseconds()},
				Label: map[string][]string{
					"invocation": {fmt.Sprintf("%d", invocationNum[kt.Name])},
				},
			}
			prof.Sample = append(prof.Sample, sample)
		}
	}

	return prof, nil
}
