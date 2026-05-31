//go:build darwin && metal
// +build darwin,metal

package gputrace

import (
	"github.com/tmc/gputrace/internal/replay"
)

type (
	// MetalReplayEngine replays trace commands through Metal.
	MetalReplayEngine = replay.MetalReplayEngine

	// MetalReplayResult reports the result of replaying a trace through Metal.
	MetalReplayResult = replay.MetalReplayResult

	// MetalValidationResult reports validation results from Metal replay.
	MetalValidationResult = replay.MetalValidationResult
)

// NewMetalReplayEngine creates a new Metal replay engine for the given trace.
func NewMetalReplayEngine(trace *Trace) (*MetalReplayEngine, error) {
	return replay.NewMetalReplayEngine(trace)
}

// FormatMetalReplayResult formats a Metal replay result as a human-readable string.
func FormatMetalReplayResult(result *MetalReplayResult) string {
	return replay.FormatMetalReplayResult(result)
}

// FormatMetalValidationResult formats a Metal validation result as a human-readable string.
func FormatMetalValidationResult(validation *MetalValidationResult) string {
	return replay.FormatMetalValidationResult(validation)
}
