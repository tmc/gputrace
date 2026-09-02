package cmd

import (
	"fmt"
	"strings"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/livetiming"
	"github.com/tmc/gputrace/internal/mlxsemantic"
)

type liveTimingProjection struct {
	RunID          string `json:"run_id"`
	ContentDigest  string `json:"content_digest"`
	ClockSamples   int    `json:"clock_samples"`
	CommandBuffers int    `json:"command_buffers"`
	Projected      int    `json:"projected_command_buffers"`
	Unmatched      int    `json:"unmatched_command_buffers"`
}

func attachLiveTiming(timeline *Timeline, trace *gputrace.Trace, path string) error {
	if timeline == nil || trace == nil {
		return fmt.Errorf("attach live timing: nil timeline or trace")
	}
	sidecar, err := livetiming.Read(path)
	if err != nil {
		return fmt.Errorf("attach live timing: %w", err)
	}
	digest, err := mlxsemantic.Digest(timeline.TracePath)
	if err != nil {
		return fmt.Errorf("attach live timing: %w", err)
	}
	if sidecar.TraceDigest != digest {
		return fmt.Errorf("attach live timing: sidecar does not identify input trace")
	}
	commandBuffers, err := trace.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("attach live timing: parse command buffers: %w", err)
	}
	captured := make(map[string]struct{})
	for _, command := range commandBuffers {
		if strings.HasPrefix(command.Label, "gputrace.live.cb.") {
			captured[command.Label] = struct{}{}
		}
	}
	return projectLiveTiming(timeline, captured, sidecar)
}

func projectLiveTiming(timeline *Timeline, captured map[string]struct{}, sidecar livetiming.Sidecar) error {
	observed := make(map[string]struct{}, len(sidecar.CommandBuffers))
	projected := 0
	unmatched := 0
	for _, command := range sidecar.CommandBuffers {
		if _, ok := captured[command.CaptureLabel]; !ok {
			unmatched++
			continue
		}
		projected++
		observed[command.CaptureLabel] = struct{}{}
		timeline.Events = append(timeline.Events, TimelineEvent{
			Name: command.FinalLabel, Category: "live_command_buffer", Phase: "X",
			Timestamp:   uint64(command.GPUStartNS / 1000),
			Duration:    uint64((command.GPUEndNS - command.GPUStartNS) / 1000),
			TimestampNS: uint64(command.GPUStartNS),
			DurationNS:  uint64(command.GPUEndNS - command.GPUStartNS),
			ProcessID:   2, ThreadID: 0,
			Args: map[string]any{
				"command_buffer_id":    command.ID,
				"capture_label":        command.CaptureLabel,
				"final_label":          command.FinalLabel,
				"timing_source":        "MTLCommandBuffer.GPUStartTime/GPUEndTime",
				"kernel_start_ns":      command.KernelStartNS,
				"kernel_duration_ns":   command.KernelEndNS - command.KernelStartNS,
				"kernel_timing_source": "MTLCommandBuffer.kernelStartTime/kernelEndTime",
				"clock_domain":         "live",
				"run_id":               sidecar.RunID,
				"sidecar_digest":       sidecar.ContentDigest,
			},
		})
	}
	for label := range captured {
		if _, ok := observed[label]; !ok {
			return fmt.Errorf("attach live timing: captured command buffer %q has no completed sidecar record", label)
		}
	}
	if projected == 0 {
		return fmt.Errorf("attach live timing: no sidecar command buffer belongs to trace")
	}
	timeline.LiveTiming = &liveTimingProjection{
		RunID: sidecar.RunID, ContentDigest: sidecar.ContentDigest,
		ClockSamples: len(sidecar.ClockSamples), CommandBuffers: len(sidecar.CommandBuffers),
		Projected: projected, Unmatched: unmatched,
	}
	return nil
}
