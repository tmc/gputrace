// Package nvidia provides Linux GPU introspection for gputrace via NVML.
//
// It wraps github.com/tmc/lib/nvidia/nvml and reports per-device identity,
// memory, utilization, temperature, and power. All functions return
// ErrNVMLUnavailable when the NVML shared library or a GPU is not present,
// so callers on non-NVIDIA systems degrade gracefully.
package nvidia

import (
	"errors"
	"fmt"

	"github.com/tmc/lib/nvidia/nvml"
)

// ErrNVMLUnavailable reports that NVML could not be initialized, typically
// because the NVIDIA driver is not installed or no GPU is visible.
var ErrNVMLUnavailable = errors.New("nvidia: NVML unavailable")

// Device is one GPU as reported by NVML.
type Device struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	UUID        string `json:"uuid,omitempty"`
	DriverVer   string `json:"driver_version,omitempty"`
	MemoryTotal uint64 `json:"memory_total_bytes"`
	MemoryUsed  uint64 `json:"memory_used_bytes"`
	MemoryFree  uint64 `json:"memory_free_bytes"`
	GPUUtilPct  uint32 `json:"gpu_utilization_percent"`
	MemUtilPct  uint32 `json:"memory_utilization_percent"`
	TempC       uint32 `json:"temperature_celsius"`
	PowerWatts  uint32 `json:"power_milliwatts"`
}

// PowerMilliWatts reports power draw in milliwatts as NVML does.
func (d Device) PowerMilliWatts() uint32 { return d.PowerWatts }

// initOnce initializes NVML at most once per process.
func ensureInit() error {
	if err := nvml.Init(); err != nil {
		return fmt.Errorf("%w: %v", ErrNVMLUnavailable, err)
	}
	return nil
}

// DriverVersion returns the installed NVIDIA driver version.
func DriverVersion() (string, error) {
	if err := ensureInit(); err != nil {
		return "", err
	}
	return nvml.SystemGetDriverVersion()
}

// Devices enumerates all visible NVIDIA GPUs.
func Devices() ([]Device, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}
	count, err := nvml.DeviceGetCount()
	if err != nil {
		return nil, fmt.Errorf("nvidia: device count: %w", err)
	}
	driver, _ := nvml.SystemGetDriverVersion()

	devices := make([]Device, 0, count)
	for i := uint32(0); i < count; i++ {
		h, err := nvml.NewDeviceByIndex(i)
		if err != nil {
			return nil, fmt.Errorf("nvidia: device %d handle: %w", i, err)
		}
		d := Device{Index: int(i), DriverVer: driver}
		if name, err := h.GetName(); err == nil {
			d.Name = name
		}
		if uuid, err := h.GetUUID(); err == nil {
			d.UUID = uuid
		}
		if mem, err := h.GetMemoryInfo(); err == nil {
			d.MemoryTotal, d.MemoryUsed, d.MemoryFree = mem.Total, mem.Used, mem.Free
		}
		if util, err := h.GetUtilizationRates(); err == nil {
			d.GPUUtilPct, d.MemUtilPct = util.Gpu, util.Memory
		}
		if temp, err := h.GetTemperature(nvml.TemperatureGpu); err == nil {
			d.TempC = temp
		}
		if power, err := h.GetPowerUsage(); err == nil {
			d.PowerWatts = power
		}
		devices = append(devices, d)
	}
	return devices, nil
}
