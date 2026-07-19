package gputrace

import (
	"io"

	"github.com/tmc/gputrace/internal/shader"
)

// ExtractShaderMetrics extracts shader metrics from t.
func ExtractShaderMetrics(t *Trace) (*ShaderMetricsReport, error) {
	return shader.ExtractShaderMetrics(t)
}

// NewShaderSourceMapper returns a source mapper that searches searchPaths.
func NewShaderSourceMapper(searchPaths ...string) *ShaderSourceMapper {
	return shader.NewShaderSourceMapper(searchPaths...)
}

// FormatShadersSimple writes a simple shader report to w.
func FormatShadersSimple(w io.Writer, report *ShaderMetricsReport) error {
	return shader.FormatShadersSimple(w, report)
}

// FormatShadersXcodeStyle writes an Xcode-style shader report to w.
func FormatShadersXcodeStyle(w io.Writer, report *ShaderMetricsReport, t *Trace, showEstimates bool) error {
	return shader.FormatShadersXcodeStyle(w, report, t, showEstimates)
}

// ExtractShaderSourceAttribution extracts source attribution for shaderName.
func ExtractShaderSourceAttribution(t *Trace, shaderName string) (*shader.ShaderSourceAttribution, error) {
	return shader.ExtractShaderSourceAttribution(t, shaderName)
}

// FormatShaderSourceAttribution formats shader source attribution.
func FormatShaderSourceAttribution(attr *shader.ShaderSourceAttribution, showHints bool) string {
	return shader.FormatShaderSourceAttribution(attr, showHints)
}

// FormatShaderSourceAttributionHTML formats shader source attribution as HTML.
func FormatShaderSourceAttributionHTML(attr *shader.ShaderSourceAttribution) string {
	return shader.FormatShaderSourceAttributionHTML(attr)
}

// ExportShaderMetricsCSV writes shader metrics as CSV.
func ExportShaderMetricsCSV(w io.Writer, report *ShaderMetricsReport) error {
	return shader.ExportShaderMetricsCSV(w, report)
}

// ExportShaderMetricsJSON writes shader metrics as JSON.
func ExportShaderMetricsJSON(w io.Writer, report *ShaderMetricsReport) error {
	return shader.ExportShaderMetricsJSON(w, report)
}

// CorrelateShaderMetrics correlates shader metrics for t.
func CorrelateShaderMetrics(t *Trace) (*shader.ShaderCorrelationReport, error) {
	return shader.CorrelateShaderMetrics(t)
}

// FormatCorrelationReport formats a shader correlation report.
func FormatCorrelationReport(report *shader.ShaderCorrelationReport) string {
	return shader.FormatCorrelationReport(report)
}
