// nvml_sampler samples NVML device counters (power, utilization, clocks,
// temperature, memory) and writes newline-delimited JSON to
// <bundle>/nvml_samples.jsonl. Invoked by `gputrace capture --samples`.
//
// Usage: nvml_sampler -interval 200ms -out DIR -stop-file PATH
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	nvml "github.com/tmc/lib/nvidia/nvml"
)

type sample struct {
	TimestampNS int64  `json:"timestamp_ns"`
	PowerMW     uint32 `json:"power_mw"`
	GPUUtilPct  uint32 `json:"gpu_util_pct"`
	MemUtilPct  uint32 `json:"mem_util_pct"`
	SMClockMHz  uint32 `json:"sm_clock_mhz,omitempty"`
	MemClockMHz uint32 `json:"mem_clock_mhz,omitempty"`
	TempC       uint32 `json:"temp_c"`
	MemUsedB    uint64 `json:"mem_used_bytes"`
	// Pstate is the NVML performance state (P0 fastest). Empty when
	// unavailable so old consumers ignore it.
	Pstate string `json:"pstate,omitempty"`
	// ThrottleReasons is a bitmask of nvmlDeviceGetCurrentClocksEventReasons
	// (bit0 idle/gpu, bit1 applications clocks, bit2 sw power cap, bit3 hw
	// slowdown, bit4 sync boost, bit5 sw thermal slowdown, bit6 hw thermal
	// slowdown, bit7 hw power brake, bit8 display clock settings).
	ThrottleReasons uint64 `json:"throttle_reasons,omitempty"`
	// EnergyMJ is cumulative energy consumption in millijoules since driver
	// load; differencing two samples gives exact energy per interval.
	EnergyMJ uint64 `json:"energy_mj,omitempty"`
}

func main() {
	interval := flag.Duration("interval", 200*time.Millisecond, "sampling interval")
	outDir := flag.String("out", "", "output directory for nvml_samples.jsonl")
	stopFile := flag.String("stop-file", "", "exit when this file appears")
	flag.Parse()
	if *outDir == "" {
		fmt.Fprintln(os.Stderr, "nvml_sampler: -out is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "nvml_sampler:", err)
		os.Exit(1)
	}

	if err := nvml.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "nvml_sampler: init:", err)
		os.Exit(1)
	}
	defer nvml.Shutdown()

	count, err := nvml.DeviceGetCount()
	if err != nil || count == 0 {
		fmt.Fprintln(os.Stderr, "nvml_sampler: no devices")
		os.Exit(1)
	}
	dev, err := nvml.NewDeviceByIndex(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nvml_sampler: device:", err)
		os.Exit(1)
	}

	f, err := os.Create(filepath.Join(*outDir, "nvml_samples.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "nvml_sampler:", err)
		os.Exit(1)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			s := sample{TimestampNS: time.Now().UnixNano()}
			if p, err := dev.GetPowerUsage(); err == nil {
				s.PowerMW = p
			}
			if u, err := dev.GetUtilizationRates(); err == nil {
				s.GPUUtilPct, s.MemUtilPct = u.Gpu, u.Memory
			}
			if c, err := dev.GetClockInfo(nvml.ClockSm); err == nil {
				s.SMClockMHz = c
			}
			if c, err := dev.GetClockInfo(nvml.ClockMem); err == nil {
				s.MemClockMHz = c
			}
			if t, err := dev.GetTemperature(nvml.TemperatureGpu); err == nil {
				s.TempC = t
			}
			if m, err := dev.GetMemoryInfo(); err == nil {
				s.MemUsedB = m.Used
			}
			if ps, err := dev.GetPerformanceState(); err == nil {
				s.Pstate = fmt.Sprintf("P%d", uint8(ps))
			}
			if r, err := dev.GetCurrentClocksEventReasons(); err == nil {
				s.ThrottleReasons = r
			}
			if e, err := dev.GetTotalEnergyConsumption(); err == nil {
				s.EnergyMJ = e
			}
			if err := enc.Encode(s); err != nil {
				return
			}
		case <-sigc:
			return
		case <-time.After(50 * time.Millisecond):
			if *stopFile != "" {
				if _, err := os.Stat(*stopFile); err == nil {
					return
				}
			}
		}
	}
}
