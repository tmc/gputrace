//go:build !darwin

package counter

import "errors"

// DecodeAPSCounterShard is unavailable away from macOS because it requires
// Xcode's private GTShaderProfiler framework.
func DecodeAPSCounterShard(data []byte, config APSGPUConfig) (*APSCounterShard, error) {
	return nil, errors.New("counter: APS counter decoding requires macOS and Xcode")
}
