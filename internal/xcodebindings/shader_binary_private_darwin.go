//go:build darwin && gputrace_private_bindings

package xcodebindings

import (
	"fmt"
	"runtime"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// EnumeratePipelineShaderBinaries returns shader binaries owned by an active
// stream-data parent. The private framework creates each binary; this package
// never constructs GTMioShaderBinaryData from caller-supplied NSData.
func EnumeratePipelineShaderBinaries(parent objc.ID, pipelineState uint64) ([]*ShaderBinaryData, error) {
	if parent == 0 {
		return nil, fmt.Errorf("shader binary parent is nil")
	}
	streamClass := objc.GetClass("GTShaderProfilerStreamData")
	if streamClass == 0 || !objc.Send[bool](parent, objc.Sel("isKindOfClass:"), objc.ID(streamClass)) {
		return nil, fmt.Errorf("shader binary parent is not GTShaderProfilerStreamData")
	}
	protocol := gtshaderprofiler.GTMioTraceDataProtocolObjectFromID(parent)
	selector := objc.Sel("enumerateBinariesForPipelineState:enumerator:")
	if !objc.RespondsToSelector(protocol.ID, selector) {
		return nil, fmt.Errorf("shader binary parent does not support enumerateBinariesForPipelineState:enumerator:")
	}
	var binaries []*ShaderBinaryData
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		protocol.EnumerateBinariesForPipelineStateEnumerator(pipelineState, func(binary *gtshaderprofiler.GTMioShaderBinaryData) {
			if binary == nil || binary.ID == 0 {
				return
			}
			binaries = append(binaries, &ShaderBinaryData{id: binary.ID})
		})
	})
	return binaries, nil
}
