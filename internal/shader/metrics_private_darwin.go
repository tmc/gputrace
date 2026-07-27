//go:build darwin && gputrace_private_bindings

package shader

import (
	"fmt"

	"github.com/tmc/apple/objc"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

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
