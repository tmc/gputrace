//go:build darwin && gputrace_private_bindings

package xcodebindings

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/tmc/apple/objc"
)

// GetRDEBuffer copies one RDE buffer into caller-owned dst. The generated
// XRGPUAPSDataProcessor binding exposes this selector's binary buffer as a
// Go string; call this adapter instead so arbitrary bytes never cross the
// Objective-C boundary through a string representation.
func GetRDEBuffer(processor objc.ID, sourceIndex, bufferIndex uint32, dst []byte) (n int, ok bool, err error) {
	return getAPSBuffer(processor, objc.Sel("getBufferAtRDESourceIndex:rdeBufferIndex:buffer:length:"), sourceIndex, bufferIndex, dst)
}

// GetUSCBuffer copies one USC buffer into caller-owned dst. It has the same
// bounds and binary-data guarantees as GetRDEBuffer.
func GetUSCBuffer(processor objc.ID, uscIndex uint32, dst []byte) (n int, ok bool, err error) {
	if processor == 0 {
		return 0, false, fmt.Errorf("APS data processor is nil")
	}
	if !objc.RespondsToSelector(processor, objc.Sel("getBufferAtUSCIndex:buffer:length:")) {
		return 0, false, fmt.Errorf("APS data processor does not support USC buffer extraction")
	}
	if len(dst) == 0 {
		return 0, false, fmt.Errorf("APS destination buffer is empty")
	}
	return getAPSBuffer(processor, objc.Sel("getBufferAtUSCIndex:buffer:length:"), uscIndex, 0, dst)
}

func getAPSBuffer(processor objc.ID, selector objc.SEL, first, second uint32, dst []byte) (n int, ok bool, err error) {
	if processor == 0 {
		return 0, false, fmt.Errorf("APS data processor is nil")
	}
	if !objc.RespondsToSelector(processor, selector) {
		return 0, false, fmt.Errorf("APS data processor does not support %v", selector)
	}
	if len(dst) == 0 {
		return 0, false, fmt.Errorf("APS destination buffer is empty")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		var length uint64
		var result bool
		if selector == objc.Sel("getBufferAtUSCIndex:buffer:length:") {
			result = objc.Send[bool](processor, selector, first, unsafe.Pointer(&dst[0]), unsafe.Pointer(&length))
		} else {
			result = objc.Send[bool](processor, selector, first, second, unsafe.Pointer(&dst[0]), unsafe.Pointer(&length))
		}
		if length > uint64(len(dst)) {
			err = fmt.Errorf("APS buffer requires %d bytes, destination has %d", length, len(dst))
			return
		}
		n = int(length)
		ok = result
	})
	return n, ok, err
}
