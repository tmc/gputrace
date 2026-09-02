//go:build darwin

package replay

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
)

const gputoolsReplayPath = "/System/Library/PrivateFrameworks/GPUToolsReplay.framework/GPUToolsReplay"

// GPUToolsReplay is the dynamically loaded command-buffer replay surface.
//
// The framework is private and its shape varies by macOS release. On releases
// where GPUToolsReplay loads but contains neither entry point, replay is driven
// through the Objective-C GTMTLReplayService class over an XPC service port
// rather than through these C functions, and OpenGPUToolsReplay fails. That
// out-of-process path is not implemented: its request encoding is unverified.
type GPUToolsReplay struct {
	handle        uintptr
	supportInit   uintptr
	dispatch      uintptr
	commitCommand uintptr
}

// OpenGPUToolsReplay loads the system replay framework and resolves the support
// initializer plus the two command-buffer entry points used by headless replay.
func OpenGPUToolsReplay() (*GPUToolsReplay, error) {
	handle, err := purego.Dlopen(gputoolsReplayPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load GPUToolsReplay: %w", err)
	}
	dispatch, err := purego.Dlsym(handle, "GTMTLReplayController_defaultDispatchFunction_noPinning")
	if err != nil || dispatch == 0 {
		dispatch = dyldLocalSymbol("GPUToolsReplay.framework", "_GTMTLReplayController_defaultDispatchFunction_noPinning")
		if dispatch == 0 {
			return nil, missingReplaySymbol("GTMTLReplayController_defaultDispatchFunction_noPinning", err)
		}
	}
	commitCommand, err := purego.Dlsym(handle, "GTMTLReplay_commitCommandBuffer")
	if err != nil || commitCommand == 0 {
		commitCommand = dyldLocalSymbol("GPUToolsReplay.framework", "_GTMTLReplay_commitCommandBuffer")
		if commitCommand == 0 {
			return nil, missingReplaySymbol("GTMTLReplay_commitCommandBuffer", err)
		}
	}
	supportInit := dyldLocalSymbol("GPUToolsReplay.framework", "_GTMTLReplaySupport_init")
	return &GPUToolsReplay{
		handle:        handle,
		supportInit:   supportInit,
		dispatch:      dispatch,
		commitCommand: commitCommand,
	}, nil
}

// InitializeSupport initializes the replay support for a live Metal device.
// Apple calls this internal one-argument function before constructing a
// GTMTLReplayController. The device must come from the same process and OS
// release; this method does not infer or synthesize private ABI arguments.
func (r *GPUToolsReplay) InitializeSupport(device objc.ID) error {
	if r == nil || r.supportInit == 0 {
		return errors.New("GPUToolsReplay support is not loaded")
	}
	if device == 0 {
		return errors.New("GPUToolsReplay device is nil")
	}
	_, _, _ = purego.SyscallN(r.supportInit, uintptr(device))
	return nil
}

// dyldLocalSymbol finds a local Mach-O symbol in an image loaded from the
// dyld shared cache. dlsym only searches the export trie, while the replay
// entry points are deliberately local symbols in current system images.
func dyldLocalSymbol(imagePart, want string) uintptr {
	lib, err := purego.Dlopen("/usr/lib/system/libdyld.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	var imageCount func() uint32
	var imageName func(uint32) *byte
	var imageHeader func(uint32) unsafe.Pointer
	var imageSlide func(uint32) int64
	for symbol, fn := range map[string]any{
		"_dyld_image_count":            &imageCount,
		"_dyld_get_image_name":         &imageName,
		"_dyld_get_image_header":       &imageHeader,
		"_dyld_get_image_vmaddr_slide": &imageSlide,
	} {
		address, lookupErr := purego.Dlsym(lib, symbol)
		if lookupErr != nil || address == 0 {
			return 0
		}
		purego.RegisterFunc(fn, address)
	}
	for i := uint32(0); i < imageCount(); i++ {
		name := imageName(i)
		if name == nil || !strings.Contains(objc.GoString(name), imagePart) {
			continue
		}
		header := imageHeader(i)
		if header == nil {
			continue
		}
		if address := findLocalMachOSymbol(header, imageSlide(i), want); address != 0 {
			return address
		}
	}
	return 0
}

const (
	machHeader64Size = 32
	lcSegment64      = 0x19
	lcSymtab         = 0x2
)

type machLoadCommand struct {
	command uint32
	size    uint32
}

type machSegment64 struct {
	command    uint32
	size       uint32
	name       [16]byte
	vmaddr     uint64
	vmsize     uint64
	fileOffset uint64
	fileSize   uint64
	maxProt    int32
	initProt   int32
	nsects     uint32
	flags      uint32
}

type machSymtab struct {
	command uint32
	size    uint32
	symoff  uint32
	nsyms   uint32
	stroff  uint32
	strsize uint32
}

type machNlist64 struct {
	stringIndex uint32
	typeByte    uint8
	section     uint8
	description uint16
	value       uint64
}

func findLocalMachOSymbol(header unsafe.Pointer, slide int64, want string) uintptr {
	if header == nil {
		return 0
	}
	loadCommands := unsafe.Add(header, machHeader64Size)
	var linkeditBase uintptr
	var symtab *machSymtab
	headerValue := (*struct {
		magic, cpuType, cpuSubtype, fileType, commandCount, commandSize, flags, reserved uint32
	})(header)
	command := loadCommands
	remaining := headerValue.commandSize
	for i := uint32(0); i < headerValue.commandCount; i++ {
		load := (*machLoadCommand)(command)
		if load.size < 8 || load.size > remaining {
			return 0
		}
		switch load.command {
		case lcSegment64:
			segment := (*machSegment64)(command)
			if strings.TrimRight(string(segment.name[:]), "\x00") == "__LINKEDIT" {
				linkeditBase = uintptr(int64(segment.vmaddr) - int64(segment.fileOffset) + slide)
			}
		case lcSymtab:
			symtab = (*machSymtab)(command)
		}
		command = unsafe.Add(command, int(load.size))
		remaining -= load.size
	}
	if linkeditBase == 0 || symtab == nil {
		return 0
	}
	if symtab.nsyms > 1<<20 || symtab.strsize > 1<<30 {
		return 0
	}
	linkedit := unsafePointer(linkeditBase)
	symbols := unsafe.Slice((*machNlist64)(unsafe.Add(linkedit, uintptr(symtab.symoff))), int(symtab.nsyms))
	nameBytes := unsafe.Slice((*byte)(unsafe.Add(linkedit, uintptr(symtab.stroff))), int(symtab.strsize))
	for _, symbol := range symbols {
		if symbol.stringIndex >= uint32(len(nameBytes)) {
			continue
		}
		end := symbol.stringIndex
		for end < uint32(len(nameBytes)) && nameBytes[end] != 0 {
			end++
		}
		if string(nameBytes[symbol.stringIndex:end]) == want {
			return uintptr(int64(symbol.value) + slide)
		}
	}
	return 0
}

func unsafePointer(address uintptr) unsafe.Pointer { return unsafe.Add(nil, address) }

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
	if len(args) == 0 {
		return 0, errors.New("GPUToolsReplay controller ABI arguments are unavailable")
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
	if commandBuffer == 0 {
		return errors.New("GPUToolsReplay command buffer is nil")
	}
	if len(args) == 0 {
		return errors.New("GPUToolsReplay controller ABI arguments are unavailable")
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
	if len(dispatchArgs) == 0 || len(commitArgs) == 0 {
		return errors.New("GPUToolsReplay controller ABI arguments are unavailable")
	}
	if _, err := r.DefaultDispatchFunctionNoPinning(dispatchArgs...); err != nil {
		return err
	}
	return r.CommitCommandBuffer(commandBuffer, commitArgs...)
}
