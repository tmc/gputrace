//go:build darwin

package replay

import (
	"errors"
	"fmt"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
)

const gputoolsReplayPath = "/System/Library/PrivateFrameworks/GPUToolsReplay.framework/GPUToolsReplay"

// GPUToolsReplay is the dynamically loaded command-buffer replay surface.
//
// The framework is private and its shape varies by macOS release. On releases
// where GPUToolsReplay loads but exports neither entry point, replay is driven
// through the Objective-C GTMTLReplayService class over an XPC service port
// rather than through these C functions, and OpenGPUToolsReplay fails. That
// out-of-process path is not implemented: its request encoding is unverified.
type GPUToolsReplay struct {
	handle        uintptr
	dispatch      uintptr
	commitCommand uintptr
}

// OpenGPUToolsReplay loads the system replay framework and resolves the two
// command-buffer entry points used by headless replay.
func OpenGPUToolsReplay() (*GPUToolsReplay, error) {
	handle, err := purego.Dlopen(gputoolsReplayPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load GPUToolsReplay: %w", err)
	}
	dispatch, err := purego.Dlsym(handle, "GTMTLReplayController_defaultDispatchFunction_noPinning")
	if err != nil || dispatch == 0 {
		return nil, missingReplaySymbol("GTMTLReplayController_defaultDispatchFunction_noPinning", err)
	}
	commitCommand, err := purego.Dlsym(handle, "GTMTLReplay_commitCommandBuffer")
	if err != nil || commitCommand == 0 {
		return nil, missingReplaySymbol("GTMTLReplay_commitCommandBuffer", err)
	}
	return &GPUToolsReplay{
		handle:        handle,
		dispatch:      dispatch,
		commitCommand: commitCommand,
	}, nil
}

func missingReplaySymbol(name string, err error) error {
	if err == nil {
		err = errors.New("symbol not found")
	}
	return fmt.Errorf("GPUToolsReplay symbol %s: %w", name, err)
}

// DefaultDispatchFunctionNoPinning calls the private default dispatch entry
// point. Arguments are private-framework ABI values and must be supplied by
// the replay controller that owns the command buffer.
func (r *GPUToolsReplay) DefaultDispatchFunctionNoPinning(args ...uintptr) (uintptr, error) {
	if r == nil || r.dispatch == 0 {
		return 0, errors.New("GPUToolsReplay is not loaded")
	}
	value, _, err := purego.SyscallN(r.dispatch, args...)
	if err != 0 {
		return 0, fmt.Errorf("call GTMTLReplayController_defaultDispatchFunction_noPinning: errno %d", err)
	}
	return value, nil
}

// CommitCommandBuffer calls the private command-buffer commit entry point.
// The command buffer is passed as an Objective-C object ID. Additional ABI
// arguments are accepted because the private signature varies by OS release.
func (r *GPUToolsReplay) CommitCommandBuffer(commandBuffer objc.ID, args ...uintptr) error {
	if r == nil || r.commitCommand == 0 {
		return errors.New("GPUToolsReplay is not loaded")
	}
	callArgs := make([]uintptr, 1, 1+len(args))
	callArgs[0] = uintptr(commandBuffer)
	callArgs = append(callArgs, args...)
	_, _, err := purego.SyscallN(r.commitCommand, callArgs...)
	if err != 0 {
		return fmt.Errorf("call GTMTLReplay_commitCommandBuffer: errno %d", err)
	}
	return nil
}

// ExecuteCommandBuffer invokes the default dispatch hook and then commits the
// command buffer through GPUToolsReplay. The argument slices are the private
// ABI arguments for the current macOS release and must be obtained from the
// replay controller implementation.
func (r *GPUToolsReplay) ExecuteCommandBuffer(commandBuffer objc.ID, dispatchArgs, commitArgs []uintptr) error {
	if commandBuffer == 0 {
		return errors.New("GPUToolsReplay command buffer is nil")
	}
	if _, err := r.DefaultDispatchFunctionNoPinning(dispatchArgs...); err != nil {
		return err
	}
	return r.CommitCommandBuffer(commandBuffer, commitArgs...)
}
