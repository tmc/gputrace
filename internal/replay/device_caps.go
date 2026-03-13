//go:build darwin

package replay

import (
	"fmt"

	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// DeviceCapabilities holds Metal device capabilities for validation.
type DeviceCapabilities struct {
	Name                       string
	MaxThreadsPerThreadgroup   metal.MTLSize // Per-dimension limits
	MaxTotalThreadsPerGroup    uint          // Actual max (width usually)
	MaxThreadgroupMemoryLength uint
	MaxBufferLength            uint64
	SupportsFamily             map[string]bool

	// Counter sampling capabilities
	SupportsCounterSamplingAtStageBoundary        bool
	SupportsCounterSamplingAtDrawBoundary         bool
	SupportsCounterSamplingAtBlitBoundary         bool
	SupportsCounterSamplingAtDispatchBoundary     bool
	SupportsCounterSamplingAtTileDispatchBoundary bool

	// Timestamp conversion
	TimestampFrequency uint64 // GPU timestamp ticks per second (for converting to nanoseconds)

	// Available counter sets
	CounterSets []string

	device metal.MTLDeviceObject
}

// QueryDeviceCapabilities returns the capabilities of the default Metal device.
func QueryDeviceCapabilities() (*DeviceCapabilities, error) {
	devicePtr := metal.MTLCreateSystemDefaultDevice()
	if devicePtr == nil {
		return nil, fmt.Errorf("no Metal device available")
	}
	device := metal.MTLDeviceObjectFromID(objc.IDFrom(devicePtr))

	caps := &DeviceCapabilities{
		device:         device,
		SupportsFamily: make(map[string]bool),
	}

	// Get device name
	caps.Name = queryDeviceName(device)

	// Query thread limits
	caps.MaxThreadsPerThreadgroup = queryMaxThreadsPerThreadgroupSize(device)
	caps.MaxTotalThreadsPerGroup = uint(caps.MaxThreadsPerThreadgroup.Width) // Width is the typical limit
	caps.MaxThreadgroupMemoryLength = queryMaxThreadgroupMemoryLength(device)
	caps.MaxBufferLength = queryMaxBufferLength(device)

	// Query counter sampling support
	caps.SupportsCounterSamplingAtStageBoundary = queryCounterSamplingSupport(device, 0)
	caps.SupportsCounterSamplingAtDrawBoundary = queryCounterSamplingSupport(device, 1)
	caps.SupportsCounterSamplingAtBlitBoundary = queryCounterSamplingSupport(device, 2)
	caps.SupportsCounterSamplingAtDispatchBoundary = queryCounterSamplingSupport(device, 3)
	caps.SupportsCounterSamplingAtTileDispatchBoundary = queryCounterSamplingSupport(device, 4)

	// Query timestamp frequency for converting GPU ticks to nanoseconds
	caps.TimestampFrequency = queryTimestampFrequency(device)

	// Query available counter sets
	caps.CounterSets = queryCounterSetNames(device)

	// Query GPU family support
	caps.queryFamilySupport()

	return caps, nil
}

func queryDeviceName(device metal.MTLDeviceObject) string {
	nameID := objc.Send[objc.ID](device.GetID(), objc.Sel("name"))
	if nameID == 0 {
		return ""
	}
	cstr := objc.Send[*byte](nameID, objc.Sel("UTF8String"))
	if cstr == nil {
		return ""
	}
	return objc.GoString(cstr)
}

func queryTimestampFrequency(device metal.MTLDeviceObject) uint64 {
	return objc.Send[uint64](device.GetID(), objc.Sel("queryTimestampFrequency"))
}

func queryCounterSetNames(device metal.MTLDeviceObject) []string {
	// Get counterSets array
	counterSetsID := objc.Send[objc.ID](device.GetID(), objc.Sel("counterSets"))
	if counterSetsID == 0 {
		return nil
	}

	count := objc.Send[uint](counterSetsID, objc.Sel("count"))
	names := make([]string, 0, count)

	for i := uint(0); i < count; i++ {
		setID := objc.Send[objc.ID](counterSetsID, objc.Sel("objectAtIndex:"), i)
		if setID == 0 {
			continue
		}
		nameID := objc.Send[objc.ID](setID, objc.Sel("name"))
		if nameID == 0 {
			continue
		}
		cstr := objc.Send[*byte](nameID, objc.Sel("UTF8String"))
		if cstr != nil {
			names = append(names, objc.GoString(cstr))
		}
	}
	return names
}

func queryMaxThreadsPerThreadgroupSize(device metal.MTLDeviceObject) metal.MTLSize {
	// Query maxThreadsPerThreadgroup - returns MTLSize with per-dimension limits
	return objc.Send[metal.MTLSize](device.GetID(), objc.Sel("maxThreadsPerThreadgroup"))
}

func queryMaxThreadgroupMemoryLength(device metal.MTLDeviceObject) uint {
	return objc.Send[uint](device.GetID(), objc.Sel("maxThreadgroupMemoryLength"))
}

func queryMaxBufferLength(device metal.MTLDeviceObject) uint64 {
	return objc.Send[uint64](device.GetID(), objc.Sel("maxBufferLength"))
}

func queryCounterSamplingSupport(device metal.MTLDeviceObject, samplingPoint uint) bool {
	return objc.Send[bool](device.GetID(), objc.Sel("supportsCounterSampling:"), samplingPoint)
}

func (caps *DeviceCapabilities) queryFamilySupport() {
	// Query common GPU families
	families := []struct {
		name string
		val  uint
	}{
		{"Apple1", 1001},
		{"Apple2", 1002},
		{"Apple3", 1003},
		{"Apple4", 1004},
		{"Apple5", 1005},
		{"Apple6", 1006},
		{"Apple7", 1007},
		{"Apple8", 1008},
		{"Apple9", 1009},
		{"Mac1", 2001},
		{"Mac2", 2002},
		{"Common1", 3001},
		{"Common2", 3002},
		{"Common3", 3003},
	}

	for _, f := range families {
		supported := objc.Send[bool](caps.device.GetID(), objc.Sel("supportsFamily:"), f.val)
		caps.SupportsFamily[f.name] = supported
	}
}

// ValidateDispatch checks if dispatch parameters are valid for this device.
func (caps *DeviceCapabilities) ValidateDispatch(threadsX, threadsY, threadsZ, tgX, tgY, tgZ int) error {
	if tgX <= 0 || tgY <= 0 || tgZ <= 0 {
		return fmt.Errorf("invalid threadgroup size: %dx%dx%d", tgX, tgY, tgZ)
	}

	// Check per-dimension limits
	if tgX > int(caps.MaxThreadsPerThreadgroup.Width) {
		return fmt.Errorf("threadgroup X=%d exceeds device max %d", tgX, caps.MaxThreadsPerThreadgroup.Width)
	}
	if tgY > int(caps.MaxThreadsPerThreadgroup.Height) {
		return fmt.Errorf("threadgroup Y=%d exceeds device max %d", tgY, caps.MaxThreadsPerThreadgroup.Height)
	}
	if tgZ > int(caps.MaxThreadsPerThreadgroup.Depth) {
		return fmt.Errorf("threadgroup Z=%d exceeds device max %d", tgZ, caps.MaxThreadsPerThreadgroup.Depth)
	}

	// Check total threads per threadgroup (typically limited to 1024)
	totalThreadsPerGroup := tgX * tgY * tgZ
	if uint(totalThreadsPerGroup) > caps.MaxTotalThreadsPerGroup {
		return fmt.Errorf("total threadgroup size %d exceeds device maximum %d",
			totalThreadsPerGroup, caps.MaxTotalThreadsPerGroup)
	}

	if threadsX <= 0 || threadsY <= 0 || threadsZ <= 0 {
		return fmt.Errorf("invalid grid size: %dx%dx%d", threadsX, threadsY, threadsZ)
	}

	return nil
}

// ValidateBufferSize checks if a buffer size is valid for this device.
func (caps *DeviceCapabilities) ValidateBufferSize(size uint64) error {
	if size > caps.MaxBufferLength {
		return fmt.Errorf("buffer size %d exceeds device maximum %d", size, caps.MaxBufferLength)
	}
	return nil
}

// TicksToNanoseconds converts GPU timestamp ticks to nanoseconds.
func (caps *DeviceCapabilities) TicksToNanoseconds(ticks uint64) float64 {
	if caps.TimestampFrequency == 0 {
		return 0
	}
	return float64(ticks) * 1e9 / float64(caps.TimestampFrequency)
}

// TicksToMicroseconds converts GPU timestamp ticks to microseconds.
func (caps *DeviceCapabilities) TicksToMicroseconds(ticks uint64) float64 {
	if caps.TimestampFrequency == 0 {
		return 0
	}
	return float64(ticks) * 1e6 / float64(caps.TimestampFrequency)
}

// String returns a human-readable summary of device capabilities.
func (caps *DeviceCapabilities) String() string {
	counterSets := "none"
	if len(caps.CounterSets) > 0 {
		counterSets = fmt.Sprintf("%v", caps.CounterSets)
	}

	return fmt.Sprintf("Device: %s\n"+
		"  MaxThreadsPerThreadgroup: %dx%dx%d (total: %d)\n"+
		"  MaxThreadgroupMemoryLength: %d bytes\n"+
		"  MaxBufferLength: %d bytes (%.1f GB)\n"+
		"  TimestampFrequency: %d Hz (%.1f MHz)\n"+
		"  CounterSets: %s\n"+
		"  CounterSampling:\n"+
		"    StageBoundary: %v\n"+
		"    DrawBoundary: %v\n"+
		"    BlitBoundary: %v\n"+
		"    DispatchBoundary: %v\n",
		caps.Name,
		caps.MaxThreadsPerThreadgroup.Width,
		caps.MaxThreadsPerThreadgroup.Height,
		caps.MaxThreadsPerThreadgroup.Depth,
		caps.MaxTotalThreadsPerGroup,
		caps.MaxThreadgroupMemoryLength,
		caps.MaxBufferLength, float64(caps.MaxBufferLength)/(1024*1024*1024),
		caps.TimestampFrequency, float64(caps.TimestampFrequency)/1e6,
		counterSets,
		caps.SupportsCounterSamplingAtStageBoundary,
		caps.SupportsCounterSamplingAtDrawBoundary,
		caps.SupportsCounterSamplingAtBlitBoundary,
		caps.SupportsCounterSamplingAtDispatchBoundary,
	)
}
