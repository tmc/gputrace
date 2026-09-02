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
	// Index the shaders by name first: the nested scan was
	// len(Shaders)*len(Pipelines) string compares. Duplicate names keep the
	// last shader in report order, which is what the nested loops did.
	byName := make(map[string]*shader.ShaderMetrics, len(report.Shaders))
	for _, metric := range report.Shaders {
		if metric == nil {
			continue
		}
		byName[metric.Name] = metric
	}
	byPipeline := make(map[int]*shader.ShaderMetrics, len(stats.Pipelines))
	for _, pipeline := range stats.Pipelines {
		if metric, ok := byName[pipeline.FunctionName]; ok {
			byPipeline[pipeline.PipelineID] = metric
		}
	}
	return shader.ApplyPipelineShaderMetricsFromStreamData(byPipeline, streamPath)
}
