package gputrace

import (
	"github.com/google/pprof/profile"
)

// ToPprof converts GPU trace timing directly to pprof format.
// This uses the correct pprof API (strings are strings, not indices).
//
// Deprecated: Use ToPprofWithMetrics for better timing accuracy and aggregation.
func (t *Trace) ToPprof(timings []*EncoderTiming) (*profile.Profile, error) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "cpu", Unit: "nanoseconds"},
		},
	}

	if len(timings) > 0 {
		prof.TimeNanos = int64(timings[0].StartTimestamp)
		var totalDuration uint64
		for _, t := range timings {
			totalDuration += t.DurationNs
		}
		prof.DurationNanos = int64(totalDuration)
	}

	// Create hierarchy: GPU Trace > Command Queue > Encoders
	gpuTraceFunc := &profile.Function{
		ID:         1,
		Name:       "GPU Trace",
		SystemName: "GPU Trace",
	}
	gpuTraceLoc := &profile.Location{
		ID:   1,
		Line: []profile.Line{{Function: gpuTraceFunc}},
	}

	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
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

	// Add encoder samples
	for i, timing := range timings {
		fnID := uint64(i + 3)
		locID := uint64(i + 3)

		fn := &profile.Function{
			ID:         fnID,
			Name:       timing.Label,
			SystemName: timing.Label,
		}
		prof.Function = append(prof.Function, fn)

		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn}},
		}
		prof.Location = append(prof.Location, loc)

		// Stack: encoder -> queue -> root (leaf to root)
		sample := &profile.Sample{
			Location: []*profile.Location{loc, queueLoc, gpuTraceLoc},
			Value:    []int64{int64(timing.DurationNs)},
		}
		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}
