//go:build darwin

package mtlb

import (
	"fmt"

	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// MetalLibrary wraps a Metal library loaded via native APIs.
type MetalLibrary struct {
	library metal.MTLLibrary
	device  metal.MTLDevice
}

// LoadMTLBWithMetal loads an MTLB file using Metal's native APIs.
// This provides accurate function enumeration compared to binary parsing.
func LoadMTLBWithMetal(data []byte) (*MetalLibrary, error) {
	if len(data) < 4 || string(data[:4]) != "MTLB" {
		return nil, fmt.Errorf("not a valid MTLB file")
	}

	// Get the default Metal device
	devicePtr := metal.MTLCreateSystemDefaultDevice()
	if devicePtr == nil {
		return nil, fmt.Errorf("no Metal device available")
	}
	device := metal.MTLDeviceObjectFromID(objc.IDFrom(devicePtr))

	// Create NSData from bytes
	nsDataClass := objc.GetClass("NSData")
	nsData := objc.Send[objc.ID](objc.ID(uintptr(nsDataClass)), objc.Sel("dataWithBytes:length:"), &data[0], uint(len(data)))
	if nsData == 0 {
		return nil, fmt.Errorf("failed to create NSData")
	}

	// Create library from data
	var libErr objc.ID
	libraryID := objc.Send[objc.ID](device.GetID(), objc.Sel("newLibraryWithData:error:"), nsData, &libErr)
	if libraryID == 0 {
		errStr := "unknown error"
		if libErr != 0 {
			desc := objc.Send[objc.ID](libErr, objc.Sel("localizedDescription"))
			if desc != 0 {
				cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
				errStr = objc.GoString(cstr)
			}
		}
		return nil, fmt.Errorf("failed to load library: %s", errStr)
	}

	library := metal.MTLLibraryObjectFromID(libraryID)
	return &MetalLibrary{
		library: library,
		device:  device,
	}, nil
}

// FunctionNames returns all function names in the library.
func (ml *MetalLibrary) FunctionNames() []string {
	return ml.library.FunctionNames()
}

// FunctionCount returns the number of functions in the library.
func (ml *MetalLibrary) FunctionCount() int {
	return len(ml.library.FunctionNames())
}

// GetFunction returns a Metal function by name.
func (ml *MetalLibrary) GetFunction(name string) (metal.MTLFunction, error) {
	fn := ml.library.NewFunctionWithName(name)
	if fn == nil || fn.GetID() == 0 {
		return nil, fmt.Errorf("function not found: %s", name)
	}
	return fn, nil
}

// Label returns the library's label (if any).
func (ml *MetalLibrary) Label() string {
	return ml.library.Label()
}
