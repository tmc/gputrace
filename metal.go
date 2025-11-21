//go:build metal
// +build metal

package gputrace

import (
	"github.com/tmc/mlx-go/experiments/gputrace/internal/replay"
)

// Metal Replay Engine types and functions

type (
	MetalReplayEngine     = replay.MetalReplayEngine
	MetalReplayResult     = replay.MetalReplayResult
	MetalValidationResult = replay.MetalValidationResult
	ReplayPlan            = replay.ReplayPlan
)

// NewMetalReplayEngine creates a new Metal replay engine for the given trace.
func NewMetalReplayEngine(trace *Trace) (*MetalReplayEngine, error) {
	return replay.NewMetalReplayEngine(trace)
}

// FormatReplayPlan formats a replay plan as a human-readable string.
func FormatReplayPlan(plan *ReplayPlan) string {
	return replay.FormatReplayPlan(plan)
}

// FormatMetalReplayResult formats a Metal replay result as a human-readable string.
func FormatMetalReplayResult(result *MetalReplayResult) string {
	return replay.FormatMetalReplayResult(result)
}

// FormatMetalValidationResult formats a Metal validation result as a human-readable string.
func FormatMetalValidationResult(validation *MetalValidationResult) string {
	return replay.FormatMetalValidationResult(validation)
}
