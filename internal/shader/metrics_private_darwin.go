//go:build darwin && gputrace_private_bindings

package shader

import (
	"fmt"

	"github.com/tmc/apple/objc"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

// ApplyPipelineShaderMetricsFromStreamData applies source-backed live-register
// metrics to reports keyed by the pipeline IDs parsed from streamData. The
// stream parent owns every binary; no caller-supplied NSData is constructed.
func ApplyPipelineShaderMetricsFromStreamData(metrics map[int]*ShaderMetrics, streamPath string) error {
	if len(metrics) == 0 {
		return nil
	}
	if streamPath == "" {
		return fmt.Errorf("streamData path is empty")
	}
	return xcodebindings.WithStreamData(streamPath, func(parent objc.ID) error {
		for pipelineID, metric := range metrics {
			if metric == nil {
				continue
			}
			if err := ApplyPipelineShaderMetrics(metric, parent, uint64(pipelineID)); err != nil {
				return fmt.Errorf("pipeline %d: %w", pipelineID, err)
			}
		}
		return nil
	})
}

// ApplyPipelineShaderMetrics obtains binaries from a verified stream-data
// parent and applies the highest live-register value to metrics. The returned
// binary objects are released after the scan completes.
func ApplyPipelineShaderMetrics(metrics *ShaderMetrics, parent objc.ID, pipelineState uint64) error {
	if metrics == nil {
		return fmt.Errorf("shader metrics is nil")
	}
	binaries, err := xcodebindings.EnumeratePipelineShaderBinaries(parent, pipelineState)
	if err != nil {
		return err
	}
	defer func() {
		for _, binary := range binaries {
			binary.Release()
		}
	}()
	for _, binary := range binaries {
		if err := ApplyShaderBinaryMetrics(metrics, binary); err != nil {
			return err
		}
	}
	return nil
}
