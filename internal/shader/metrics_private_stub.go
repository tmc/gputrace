//go:build !darwin || !gputrace_private_bindings

package shader

// ApplyPipelineShaderMetricsFromStreamData is unavailable without the private
// GTShaderProfiler bindings. The portable parser leaves HighRegister unset.
func ApplyPipelineShaderMetricsFromStreamData(metrics map[int]*ShaderMetrics, streamPath string) error {
	return nil
}
