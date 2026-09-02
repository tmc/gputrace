//go:build darwin

package xcodebindings

import (
	"fmt"
	"unsafe"

	"github.com/tmc/apple/objc"
)

const (
	getRDEBufferSelector = "getBufferAtRDESourceIndex:rdeBufferIndex:buffer:length:"
	addRDEBufferSelector = "addBufferAtRDESourceIndex:rdeBufferIndex:buffer:length:"
	addUSCBufferSelector = "addBufferAtUSCIndex:buffer:length:"
)

// CopyRDEBuffer obtains one RDE buffer and copies at most maxBytes into Go
// memory. The private framework's returned pointer is never exposed to Go.
func CopyRDEBuffer(processor objc.ID, sourceIndex, bufferIndex uint32, maxBytes int) ([]byte, error) {
	if processor == 0 {
		return nil, fmt.Errorf("APS data processor is nil")
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("maximum buffer length is negative")
	}
	if !objc.RespondsToSelector(processor, objc.Sel(getRDEBufferSelector)) {
		return nil, fmt.Errorf("APS data processor does not respond to %s", getRDEBufferSelector)
	}

	var buffer *byte
	var length uint64
	ok := objc.Send[bool](processor, objc.Sel(getRDEBufferSelector), sourceIndex, bufferIndex, &buffer, &length)
	if !ok {
		return nil, fmt.Errorf("get RDE buffer failed")
	}
	if length > uint64(maxBytes) {
		return nil, fmt.Errorf("RDE buffer length %d exceeds maximum %d", length, maxBytes)
	}
	if length == 0 {
		return nil, nil
	}
	if buffer == nil {
		return nil, fmt.Errorf("get RDE buffer returned nil data")
	}
	data := unsafe.Slice(buffer, int(length))
	return append([]byte(nil), data...), nil
}

// AddRDEBuffer passes a caller-owned byte slice to an RDE buffer setter.
func AddRDEBuffer(processor objc.ID, sourceIndex, bufferIndex uint32, data []byte) error {
	return addBuffer(processor, addRDEBufferSelector, sourceIndex, bufferIndex, data)
}

// AddUSCBuffer passes a caller-owned byte slice to a USC buffer setter.
func AddUSCBuffer(processor objc.ID, uscIndex uint32, data []byte) error {
	return addBuffer(processor, addUSCBufferSelector, uscIndex, 0, data)
}

func addBuffer(processor objc.ID, selector string, first, second uint32, data []byte) error {
	if processor == 0 {
		return fmt.Errorf("APS data processor is nil")
	}
	if !objc.RespondsToSelector(processor, objc.Sel(selector)) {
		return fmt.Errorf("APS data processor does not respond to %s", selector)
	}
	var pointer unsafe.Pointer
	if len(data) != 0 {
		pointer = unsafe.Pointer(&data[0])
	}
	if selector == addUSCBufferSelector {
		objc.Send[struct{}](processor, objc.Sel(selector), first, pointer, uint64(len(data)))
	} else {
		objc.Send[struct{}](processor, objc.Sel(selector), first, second, pointer, uint64(len(data)))
	}
	return nil
}
