//go:build darwin

package cmd

import (
	"fmt"
	"io"

	"github.com/tmc/gputrace/internal/tracebundle"
)

func applyXcodePayload(output *xcodeProfileActionOutput, payload tracebundle.Payload) {
	selfContained := payload.Class == tracebundle.PayloadFull
	profilerTiming := payload.HasProfilerStream
	structural := selfContained
	output.PayloadClass = string(payload.Class)
	output.SelfContained = boolPointer(selfContained)
	output.ProfilerTimingAvailable = boolPointer(profilerTiming)
	output.StructuralAnalysisAvailable = boolPointer(structural)
}

func writeXcodePayloadStatus(w io.Writer, payload tracebundle.Payload) {
	switch payload.Class {
	case tracebundle.PayloadFull:
		fmt.Fprintln(w, "Trace payload: full and self-contained")
		fmt.Fprintln(w, "  Profiler timing: available")
		fmt.Fprintln(w, "  Structural/threadgroup analysis: available")
	case tracebundle.PayloadProfilerOnly:
		fmt.Fprintln(w, "Trace payload: profiler-only (not self-contained)")
		fmt.Fprintln(w, "  Aggregate profiler timing: available")
		fmt.Fprintln(w, "  Structural/threadgroup analysis: unavailable; capture and raw resource payload are missing")
	default:
		fmt.Fprintln(w, "Trace payload: incomplete (not self-contained)")
		if payload.HasProfilerStream {
			fmt.Fprintln(w, "  Aggregate profiler timing: available")
		} else {
			fmt.Fprintln(w, "  Aggregate profiler timing: unavailable")
		}
		fmt.Fprintln(w, "  Structural/threadgroup analysis: unavailable; capture or raw resource payload is missing")
	}
}

func requireSelfContainedExport(path string, payload tracebundle.Payload) error {
	if payload.Class == tracebundle.PayloadFull {
		return nil
	}
	timing := "aggregate profiler timing is unavailable"
	if payload.HasProfilerStream {
		timing = "aggregate profiler timing remains usable"
	}
	return fmt.Errorf(
		"exported trace is %s and not self-contained: %s; %s, but structural/threadgroup analysis is unavailable because capture or raw resource payload is missing; preserving incomplete export",
		payload.Class, path, timing,
	)
}
