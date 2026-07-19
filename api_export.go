package gputrace

import (
	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/export"
)

// ToPprof converts timing data to a pprof profile.
func ToPprof(t *Trace, timings []*EncoderTiming) (*profile.Profile, error) {
	return export.ToPprof(t, timings)
}

// ToPprofWithSource converts timing data and source mappings to a pprof profile.
func ToPprofWithSource(t *Trace, timings []*EncoderTiming, mapper *ShaderSourceMapper) (*profile.Profile, error) {
	return export.ToPprofWithSource(t, timings, mapper)
}

// ToPprofWithMetrics converts counter metrics to a pprof profile.
func ToPprofWithMetrics(t *Trace, mapper *ShaderSourceMapper, stats *PerfCounterStats) (*profile.Profile, error) {
	return export.ToPprofWithMetrics(t, mapper, stats)
}
