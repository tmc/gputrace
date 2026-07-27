//go:build darwin

package shader

import (
	"fmt"

	"github.com/tmc/gputrace/internal/xcodebindings"
)

// ApplyShaderBinaryMetrics records the highest live register reported by a
// parent-validated shader binary. It does not construct shader binaries; the
// caller must obtain one through xcodebindings.NewShaderBinaryData.
func ApplyShaderBinaryMetrics(metrics *ShaderMetrics, binary *xcodebindings.ShaderBinaryData) error {
	if metrics == nil {
		return fmt.Errorf("shader metrics is nil")
	}
	if binary == nil {
		return fmt.Errorf("shader binary is nil")
	}
	count, err := binary.InstructionInfoCount()
	if err != nil {
		return fmt.Errorf("read shader instruction count: %w", err)
	}
	if count > uint64(^uint32(0)) {
		return fmt.Errorf("shader instruction count %d exceeds adapter limit", count)
	}
	var high int32
	for i := uint64(0); i < count; i++ {
		value, err := binary.LiveRegister(uint32(i))
		if err != nil {
			return fmt.Errorf("read live register %d: %w", i, err)
		}
		if value > high {
			high = value
		}
	}
	metrics.HighRegister = int(high)
	return nil
}
