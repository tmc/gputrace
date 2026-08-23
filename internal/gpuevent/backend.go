package gpuevent

import (
	"github.com/tmc/lib/nvidia/cupti"
	"github.com/tmc/lib/nvidia/nvml"
)

// Backend reports one capture/observation stack and whether it is usable
// on this host. Availability is probed lazily by loading the vendor
// library; a backend that cannot load is reported unavailable with the
// reason, not hidden.
type Backend struct {
	Name       string `json:"name"`
	Vendor     string `json:"vendor"`
	Available  bool   `json:"available"`
	Devices    int    `json:"devices,omitempty"`
	Tracing    bool   `json:"tracing,omitempty"`    // can capture per-launch activity
	Counters   bool   `json:"counters,omitempty"`   // can sample device metrics
	Detail     string `json:"detail,omitempty"`     // driver/library version or failure reason
}

// Registry probes every known backend in vendor order. Probing is cheap:
// each is a shared-library open plus one or two queries.
func Registry() []Backend {
	var out []Backend
	out = append(out, probeNVIDIA())
	out = append(out, probeMetal())
	return out
}

// AnyAvailable reports whether at least one capture backend works here.
func AnyAvailable() bool {
	for _, b := range Registry() {
		if b.Available {
			return true
		}
	}
	return false
}

func probeNVIDIA() Backend {
	b := Backend{Name: "cuda", Vendor: "NVIDIA"}
	if err := nvml.Init(); err != nil {
		b.Detail = "nvml unavailable"
		return b
	}
	count, err := nvml.DeviceGetCount()
	if err != nil {
		b.Detail = "nvml device enumeration failed"
		return b
	}
	b.Available = true
	b.Devices = int(count)
	b.Counters = true
	if v, err := nvml.SystemGetDriverVersion(); err == nil {
		b.Detail = "driver " + v
	}
	// Tracing needs CUPTI; its absence degrades to counters-only.
	if err := cupti.Load("libcupti.so"); err == nil {
		b.Tracing = true
	} else if path := defaultCuptiPath(); path != "" {
		if err := cupti.Load(path); err == nil {
			b.Tracing = true
		}
	}
	if !b.Tracing {
		b.Detail += "; cupti unavailable (no kernel tracing)"
	}
	return b
}

func defaultCuptiPath() string {
	for _, p := range []string{
		"/usr/local/cuda/lib64/libcupti.so",
		"/usr/local/cuda/lib64/libcupti.so.13",
		"/usr/local/cuda/lib64/libcupti.so.12",
	} {
		return p // first candidate; Load decides availability
	}
	return ""
}

func probeMetal() Backend {
	b := Backend{Name: "metal", Vendor: "Apple", Detail: "capture via gputrace interposer on darwin"}
	if metalAvailable() {
		b.Available = true
		b.Devices = 1
		b.Tracing = true
		b.Counters = true
	} else {
		b.Detail += "; not darwin"
	}
	return b
}
