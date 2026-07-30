//go:build !darwin || !gputrace_private_bindings

package cmd

import (
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

func applySourceBackedShaderMetrics(_ string, _ *counter.StreamDataStats, _ *gputrace.ShaderMetricsReport) error {
	return nil
}
