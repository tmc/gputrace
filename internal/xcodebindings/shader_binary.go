//go:build darwin

package xcodebindings

import (
	"fmt"

	"github.com/tmc/apple/objc"
)

// ShaderBinaryData owns a GTMioShaderBinaryData object created from a verified
// GTShaderProfilerStreamData parent.
type ShaderBinaryData struct {
	id objc.ID
}

// NewShaderBinaryData constructs a shader binary only in the context of an
// active GTShaderProfilerStreamData object. A raw NSData object is not a valid
// parent and cannot be used to create this wrapper.
func NewShaderBinaryData(parent, binaryData objc.ID, index uint64) (*ShaderBinaryData, error) {
	if parent == 0 {
		return nil, fmt.Errorf("shader binary parent is nil")
	}
	if binaryData == 0 {
		return nil, fmt.Errorf("shader binary data is nil")
	}
	streamClass := objc.GetClass("GTShaderProfilerStreamData")
	if streamClass == 0 || !objc.Send[bool](parent, objc.Sel("isKindOfClass:"), objc.ID(streamClass)) {
		return nil, fmt.Errorf("shader binary parent is not GTShaderProfilerStreamData")
	}
	return nil, fmt.Errorf("standalone GTMioShaderBinaryData construction is disabled; enumerate it from the stream parent")
}

// InstructionInfoCount returns the number of instruction records.
func (b *ShaderBinaryData) InstructionInfoCount() (uint64, error) {
	if b == nil || b.id == 0 {
		return 0, fmt.Errorf("shader binary is nil")
	}
	return objc.Send[uint64](b.id, objc.Sel("instructionInfoCount")), nil
}

// LiveRegister returns the live-register count for one instruction.
func (b *ShaderBinaryData) LiveRegister(index uint32) (int32, error) {
	if b == nil || b.id == 0 {
		return 0, fmt.Errorf("shader binary is nil")
	}
	count, err := b.InstructionInfoCount()
	if err != nil {
		return 0, err
	}
	if uint64(index) >= count {
		return 0, fmt.Errorf("instruction index %d out of range %d", index, count)
	}
	return objc.Send[int32](b.id, objc.Sel("liveRegisterForInstructionAtIndex:"), index), nil
}

// Release releases the underlying Objective-C object.
func (b *ShaderBinaryData) Release() {
	if b != nil && b.id != 0 {
		objc.Send[objc.ID](b.id, objc.Sel("release"))
		b.id = 0
	}
}
