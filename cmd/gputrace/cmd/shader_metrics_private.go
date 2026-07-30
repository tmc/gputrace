//go:build darwin && gputrace_private_bindings

package cmd

import (
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/shader"
)

func applySourceBackedShaderMetrics(streamPath string, stats *counter.StreamDataStats, report *gputrace.ShaderMetricsReport) error {
	if stats == nil || report == nil {
		return nil
	}
	byPipeline := make(map[int]*shader.ShaderMetrics)
	for _, metric := range report.Shaders {
		if metric == nil {
			continue
		}
		for _, pipeline := range stats.Pipelines {
			if pipeline.FunctionName == metric.Name {
				byPipeline[pipeline.PipelineID] = metric
			}
		}
	}
	return shader.ApplyPipelineShaderMetricsFromStreamData(byPipeline, streamPath)
}
