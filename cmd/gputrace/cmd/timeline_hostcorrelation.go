package cmd

import (
	"fmt"

	"github.com/tmc/gputrace/internal/hostcorrelation"
	"github.com/tmc/gputrace/internal/mlxsemantic"
	"github.com/tmc/gputrace/internal/perfetto"
)

type hostCorrelationProjection struct {
	Schema       string                           `json:"schema"`
	RunID        string                           `json:"run_id"`
	HostDigest   string                           `json:"host_digest"`
	TraceDigest  string                           `json:"trace_digest"`
	HostClock    string                           `json:"host_clock"`
	GPUClock     string                           `json:"gpu_clock"`
	BridgeDigest string                           `json:"bridge_digest,omitempty"`
	MaxErrorNS   float64                          `json:"max_error_ns"`
	Events       []hostcorrelation.ProjectedEvent `json:"events"`
}

func attachHostCorrelation(timeline *Timeline, tracePath string, clock timelineClock, receiptPath string) error {
	if timeline == nil {
		return fmt.Errorf("attach host correlation: nil timeline")
	}
	if clock == timelineClockBoth {
		return fmt.Errorf("attach host correlation: --clock both has no shared timeline axis")
	}
	if !timelineHasMeasuredClock(timeline, clock) {
		return fmt.Errorf("attach host correlation: selected %s clock is not measured", clock)
	}
	receipt, err := hostcorrelation.Read(receiptPath)
	if err != nil {
		return fmt.Errorf("attach host correlation: %w", err)
	}
	if receipt.GPU.ClockDomain != string(clock) {
		return fmt.Errorf("attach host correlation: receipt GPU clock %q differs from selected clock %q", receipt.GPU.ClockDomain, clock)
	}
	digest, err := mlxsemantic.Digest(tracePath)
	if err != nil {
		return fmt.Errorf("attach host correlation: %w", err)
	}
	if receipt.GPU.ContentDigest != digest {
		return fmt.Errorf("attach host correlation: receipt GPU digest does not identify input trace")
	}
	events, err := receipt.Project()
	if err != nil {
		return fmt.Errorf("attach host correlation: %w", err)
	}
	projection := &hostCorrelationProjection{
		Schema:      receipt.Schema,
		RunID:       receipt.GPU.RunID,
		HostDigest:  receipt.Host.ContentDigest,
		TraceDigest: receipt.GPU.ContentDigest,
		HostClock:   receipt.Host.ClockDomain,
		GPUClock:    receipt.GPU.ClockDomain,
		Events:      append([]hostcorrelation.ProjectedEvent(nil), events...),
	}
	if receipt.Bridge != nil {
		projection.BridgeDigest = receipt.Bridge.SourceDigest
		projection.MaxErrorNS = events[0].MaxErrorNS
	}
	timeline.HostCorrelation = projection
	return nil
}

func timelineHasMeasuredClock(timeline *Timeline, clock timelineClock) bool {
	if timeline == nil {
		return false
	}
	if clock == timelineClockLive {
		return timeline.LiveTiming != nil && timeline.LiveTiming.ClockSamples >= 3 && timeline.LiveTiming.CommandBuffers > 0
	}
	if timeline.Timing == nil {
		return false
	}
	switch clock {
	case timelineClockBusy:
		return !timeline.Timing.EncoderTimingApproximate &&
			timeline.Timing.EncoderTimingSource != "" &&
			timeline.Timing.EncoderTimingSource != "unavailable"
	case timelineClockWall:
		if timeline.Timing.CommandBufferWallNs == 0 {
			return false
		}
		for _, event := range timeline.Events {
			if event.Category == "command_buffer" {
				return true
			}
		}
	}
	return false
}

func appendHostCorrelationEvents(trace *perfetto.Trace, timeline *Timeline) {
	projection := timeline.HostCorrelation
	if projection == nil {
		return
	}
	trackID := perfetto.TrackUUID("gputrace.host-correlation", projection.RunID)
	trace.Tracks = append(trace.Tracks, perfetto.Track{
		UUID:        trackID,
		Name:        "Host signposts (verified correlation)",
		Description: "Host events projected through a trace-identified clock receipt",
	})
	nextID := nextPerfettoEventID(trace)
	for _, event := range projection.Events {
		kind := perfetto.EventInstant
		if event.Kind == "interval" {
			kind = perfetto.EventSlice
		}
		trace.Events = append(trace.Events, perfetto.Event{
			ID:         nextID,
			TrackUUID:  trackID,
			Name:       event.Name,
			Category:   "host_signpost",
			Kind:       kind,
			StartNS:    uint64(event.TimestampNS),
			DurationNS: uint64(event.DurationNS),
			Required:   true,
			Args: map[string]any{
				"event_id":       event.ID,
				"join_basis":     "host-correlation-receipt",
				"run_id":         projection.RunID,
				"host_digest":    projection.HostDigest,
				"trace_digest":   projection.TraceDigest,
				"host_clock":     projection.HostClock,
				"clock_domain":   projection.GPUClock,
				"bridge_digest":  projection.BridgeDigest,
				"max_error_ns":   event.MaxErrorNS,
				"timing_quality": "measured-with-declared-error",
			},
		})
		nextID++
	}
}
