// Package graph provides graph visualization generation for GPU traces.
package graph

import (
	"strings"

	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/trace"
)

// Generator is the interface for graph output generators.
type Generator interface {
	// Generate creates a graph visualization from a trace.
	Generate(t *trace.Trace, config *Config) (string, error)
}

// Config holds configuration for graph generation.
type Config struct {
	// Type of graph to generate (hierarchy, flow, resources)
	Type string

	// ShowTiming includes timing information in nodes
	ShowTiming bool

	// ShowMemory includes memory usage information
	ShowMemory bool

	// FilterEncoder filters to specific encoder (empty = all)
	FilterEncoder string

	// FilterShader filters to specific shader (empty = all)
	FilterShader string
}

// ShaderInfo holds information about a shader for visualization.
type ShaderInfo struct {
	Name           string
	ExecutionCount int
	Duration       int64 // nanoseconds
}

// getShaderMetrics extracts shader metrics for graph labels.
func getShaderMetrics(t *trace.Trace) (map[string]*ShaderInfo, error) {
	report, err := shader.ExtractShaderMetrics(t)
	if err != nil {
		return nil, err
	}

	metrics := make(map[string]*ShaderInfo, len(report.Shaders))
	for _, sm := range report.Shaders {
		info := &ShaderInfo{
			Name:           sm.Name,
			ExecutionCount: sm.InvocationCount,
			Duration:       int64(sm.TotalDurationNs),
		}
		addShaderMetric(metrics, sm.Name, info)
		addShaderMetric(metrics, extractShaderName(sm.Name), info)
	}
	return metrics, nil
}

func addShaderMetric(metrics map[string]*ShaderInfo, name string, info *ShaderInfo) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if _, ok := metrics[name]; !ok {
		metrics[name] = info
	}
}

// extractShaderName extracts the shader name from an encoder label.
// Example: "Encoder_1_simple_add" -> "simple_add".
func extractShaderName(encoderLabel string) string {
	parts := strings.Split(encoderLabel, "_")
	if len(parts) >= 3 && parts[0] == "Encoder" {
		return strings.Join(parts[2:], "_")
	}
	if encoderLabel != "" && !strings.HasPrefix(encoderLabel, "0x") {
		return encoderLabel
	}
	return ""
}
