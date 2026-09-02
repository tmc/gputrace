// Package tracebench turns GPU trace evidence into benchmark measurements.
//
// Trace totals are reported per trace unless the caller supplies an explicit
// positive work count and unit. The package never infers workload semantics
// from a trace name.
package tracebench

import (
	"fmt"
	"path/filepath"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/buildinfo"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilerraw"
	gputraceTrace "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

const SchemaVersion = 1

// Status describes the quality of evidence in a report section.
type Status string

const (
	StatusMeasured    Status = "measured"
	StatusStructural  Status = "structural"
	StatusUnsupported Status = "unsupported"
	StatusInvalid     Status = "invalid"
	StatusIncomplete  Status = "incomplete"
)

// Section records whether a collector produced evidence and where it came
// from. Detail is present only when the section did not complete normally.
type Section struct {
	Status Status `json:"status"`
	Source string `json:"source,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Work declares the logical work represented by one trace.
type Work struct {
	Count uint64 `json:"count"`
	Unit  string `json:"unit"`
}

// Identity binds measurements to their trace and observer.
type Identity struct {
	Path            string `json:"path"`
	TraceUUID       string `json:"trace_uuid,omitempty"`
	Payload         string `json:"payload"`
	ObserverVersion string `json:"observer_version"`
}

// Structure contains trace-derived workload shape.
type Structure struct {
	Section
	CommandBuffers *uint64 `json:"command_buffers,omitempty"`
	Encoders       *uint64 `json:"encoders,omitempty"`
	Dispatches     *uint64 `json:"dispatches,omitempty"`
	UniqueKernels  *uint64 `json:"unique_kernels,omitempty"`
}

// Timing contains distinct measured GPU timing boundaries. A nil duration was
// not present in the profiler stream; it is not a measured zero.
type Timing struct {
	Section
	DispatchSpanNS        *uint64 `json:"dispatch_span_ns,omitempty"`
	CommandBufferActiveNS *uint64 `json:"command_buffer_active_ns,omitempty"`
	CommandBufferWallNS   *uint64 `json:"command_buffer_wall_ns,omitempty"`
	EffectiveGPUNS        *uint64 `json:"effective_gpu_ns,omitempty"`
}

// Refusal records evidence that could not support a requested claim.
type Refusal struct {
	Collector string `json:"collector"`
	Reason    string `json:"reason"`
}

// Report is the stable, sectioned result of analyzing one trace.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Identity      Identity  `json:"identity"`
	Work          *Work     `json:"work,omitempty"`
	Structure     Structure `json:"structure"`
	Timing        Timing    `json:"timing"`
	Refusals      []Refusal `json:"refusals,omitempty"`
}

// Options controls report identity and explicit normalization.
type Options struct {
	Work *Work
}

// Analyze collects all supported evidence from path. A missing collector is
// recorded in Report.Refusals and does not erase independent valid sections.
func Analyze(path string, opts Options) (*Report, error) {
	if path == "" {
		return nil, fmt.Errorf("tracebench: empty trace path")
	}
	work, err := validateWork(opts.Work)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("tracebench: resolve trace path: %w", err)
	}
	payload, err := tracebundle.InspectPayload(abs)
	if err != nil {
		return nil, fmt.Errorf("tracebench: inspect payload: %w", err)
	}
	r := &Report{
		SchemaVersion: SchemaVersion,
		Identity: Identity{
			Path:            abs,
			Payload:         string(payload.Class),
			ObserverVersion: buildinfo.EffectiveVersion(),
		},
		Work: work,
		Structure: Structure{Section: Section{
			Status: StatusUnsupported,
			Detail: "capture records are not present",
		}},
		Timing: Timing{Section: Section{
			Status: StatusUnsupported,
			Detail: "profiler streamData is not present",
		}},
	}
	if metadata, err := gputraceTrace.ReadMetadata(abs); err == nil {
		r.Identity.TraceUUID = metadata.UUID
	}
	if payload.HasCapture {
		collectStructure(r, abs)
	} else {
		r.Refusals = append(r.Refusals, Refusal{"structure", r.Structure.Detail})
	}
	if profilerDir := profilerraw.FindDirWithStreamData(abs); profilerDir != "" {
		collectTiming(r, profilerDir)
	} else {
		r.Refusals = append(r.Refusals, Refusal{"timing", r.Timing.Detail})
	}
	return r, nil
}

func validateWork(work *Work) (*Work, error) {
	if work == nil {
		return nil, nil
	}
	if work.Count == 0 {
		return nil, fmt.Errorf("tracebench: work count must be positive")
	}
	switch work.Unit {
	case "op", "token", "step", "byte":
	default:
		return nil, fmt.Errorf("tracebench: unsupported work unit %q", work.Unit)
	}
	copy := *work
	return &copy, nil
}

func collectStructure(r *Report, path string) {
	trace, err := gputrace.Open(path)
	if err != nil {
		r.Structure.Section = Section{Status: StatusInvalid, Source: "capture", Detail: err.Error()}
		r.Refusals = append(r.Refusals, Refusal{"structure", err.Error()})
		return
	}
	stats, err := gputrace.ExtractStatistics(trace)
	if err != nil {
		r.Structure.Section = Section{Status: StatusInvalid, Source: "capture", Detail: err.Error()}
		r.Refusals = append(r.Refusals, Refusal{"structure", err.Error()})
		return
	}
	commandBuffers := uint64(stats.CommandBuffers)
	dispatches := uint64(stats.DispatchCalls)
	uniqueKernels := uint64(stats.UniqueKernels)
	r.Structure = Structure{
		Section:        Section{Status: StatusStructural, Source: "Metal capture records"},
		CommandBuffers: &commandBuffers,
		Dispatches:     &dispatches,
		UniqueKernels:  &uniqueKernels,
	}
	if stats.ComputeEncodersAvailable {
		encoders := uint64(stats.ComputeEncoders)
		r.Structure.Encoders = &encoders
	}
}

func collectTiming(r *Report, profilerDir string) {
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		r.Timing.Section = Section{Status: StatusInvalid, Source: "streamData", Detail: err.Error()}
		r.Refusals = append(r.Refusals, Refusal{"timing", err.Error()})
		return
	}
	counter.CorrelateDispatchSamples(stats)
	r.Timing.Section = Section{Status: StatusMeasured, Source: stats.TimingSource}
	if stats.TotalDispatchTimeUs > 0 {
		v := uint64(stats.TotalDispatchTimeUs) * 1000
		r.Timing.DispatchSpanNS = &v
	}
	if stats.CommandBufferActiveNs > 0 {
		v := stats.CommandBufferActiveNs
		r.Timing.CommandBufferActiveNS = &v
	}
	if stats.CommandBufferWallNs > 0 {
		v := stats.CommandBufferWallNs
		r.Timing.CommandBufferWallNS = &v
	}
	if stats.EffectiveGPUTimeNs != nil {
		v := *stats.EffectiveGPUTimeNs
		r.Timing.EffectiveGPUNS = &v
	}

	// Profiler streams are authoritative for these counts when no structural
	// capture is available.
	if r.Structure.Status == StatusUnsupported {
		dispatches := uint64(stats.NumGPUCommands)
		encoders := uint64(stats.NumEncoders)
		r.Structure = Structure{
			Section:        Section{Status: StatusStructural, Source: "streamData"},
			CommandBuffers: commandBufferCount(stats),
			Encoders:       &encoders,
			Dispatches:     &dispatches,
		}
		removeRefusal(r, "structure")
	}
}

func commandBufferCount(stats *counter.StreamDataStats) *uint64 {
	if stats.Timeline == nil {
		return nil
	}
	count := uint64(len(stats.Timeline.CommandBufferTimestamps))
	return &count
}

func removeRefusal(r *Report, collector string) {
	out := r.Refusals[:0]
	for _, refusal := range r.Refusals {
		if refusal.Collector != collector {
			out = append(out, refusal)
		}
	}
	r.Refusals = out
}
