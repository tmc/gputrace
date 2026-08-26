// Package cuptiprofile renders CUPTI activity captures as pprof profiles.
//
// Each distinct kernel becomes a pprof function; every launch contributes a
// sample valued at its GPU execution time. The result is diffable with
// `go tool pprof -base` across captures and viewable as a flamegraph —
// the same evidence loop gputrace uses for Metal traces, applied to CUDA.
package cuptiprofile

import (
	"fmt"
	"io"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// Build converts a capture's kernel activity into a pprof profile. Sample
// values are per-launch GPU durations in nanoseconds; labels carry the
// grid/block shape so `pprof -tagfocus` can slice by geometry.
func Build(cap gpuevent.Capture) (*profile.Profile, error) {
	var kernels []gpuevent.Event
	for _, e := range cap.Events {
		if e.Kind == gpuevent.KindKernel {
			kernels = append(kernels, e)
		}
	}
	if len(kernels) == 0 {
		return nil, fmt.Errorf("cuptiprofile: capture has no kernel events")
	}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu_time", Unit: "nanoseconds"},
		},
	}
	if minStart := minStartNS(kernels); minStart != 0 {
		prof.TimeNanos = int64(minStart)
	}
	var spanEnd uint64
	for _, k := range kernels {
		if e := k.StartNS + k.DurationNS(); e > spanEnd {
			spanEnd = e
		}
	}
	if prof.TimeNanos != 0 && spanEnd > uint64(prof.TimeNanos) {
		prof.DurationNanos = int64(spanEnd - uint64(prof.TimeNanos))
	}

	locs := make(map[string]*profile.Location)
	nextID := uint64(1)

	getLoc := func(name string) *profile.Location {
		if l, ok := locs[name]; ok {
			return l
		}
		f := &profile.Function{
			ID:         nextID,
			Name:       name,
			SystemName: name,
		}
		nextID++
		prof.Function = append(prof.Function, f)
		l := &profile.Location{
			ID:   nextID,
			Line: []profile.Line{{Function: f}},
		}
		nextID++
		prof.Location = append(prof.Location, l)
		locs[name] = l
		return l
	}

	for _, k := range kernels {
		name := k.Name
		if name == "" {
			name = k.RawSymbol
		}
		sample := &profile.Sample{
			Location: []*profile.Location{getLoc(name)},
			Value:    []int64{int64(k.DurationNS())},
			Label: map[string][]string{
				"kind": {"cuda_kernel"},
			},
		}
		if k.Grid != "" {
			sample.Label["grid"] = []string{k.Grid}
		}
		if k.Block != "" {
			sample.Label["block"] = []string{k.Block}
		}
		prof.Sample = append(prof.Sample, sample)
	}
	return prof, nil
}

// Write serializes the profile in pprof protobuf format.
func Write(p *profile.Profile, w io.Writer) error {
	return p.Write(w)
}

func minStartNS(events []gpuevent.Event) uint64 {
	min := ^uint64(0)
	for _, e := range events {
		if e.StartNS < min {
			min = e.StartNS
		}
	}
	if min == ^uint64(0) {
		return 0
	}
	return min
}
