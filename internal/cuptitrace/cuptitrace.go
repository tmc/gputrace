package cuptitrace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/tmc/gputrace/internal/perfetto"
)

// Event is one decoded CUPTI activity record from the capture probe.
type Event struct {
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	StartNS   uint64 `json:"start_ns"`
	EndNS     uint64 `json:"end_ns"`
	Grid      string `json:"grid,omitempty"`
	Block     string `json:"block,omitempty"`
	Registers int    `json:"registers,omitempty"`
	Bytes     uint64 `json:"bytes,omitempty"` // memcpy size
}

// Sample is one NVML device observation taken while the workload ran.
type Sample struct {
	TimestampNS uint64 `json:"timestamp_ns"`
	PowerMW     uint32 `json:"power_mw"`
	GPUUtilPct  uint32 `json:"gpu_util_pct"`
	MemUtilPct  uint32 `json:"mem_util_pct"`
	TempC       uint32 `json:"temp_c"`
	MemUsedB    uint64 `json:"mem_used_bytes"`
}

// Options controls trace construction.
type Options struct {
	// PerKernelTracks puts each distinct kernel name on its own track,
	// grouped under a synthetic parent, mirroring an Xcode-style shader
	// grouping. When false, all kernels share one track.
	PerKernelTracks bool
}

// ReadJSONL reads newline-delimited CUPTI events. Trailing partial lines
// (from a tracer killed mid-write) are tolerated.
func ReadJSONL(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decode(f)
}

// ReadSamples reads newline-delimited NVML samples; a missing file yields
// no samples rather than an error so counter overlay stays optional.
func ReadSamples(path string) ([]Sample, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var samples []Sample
	for _, line := range bufioScannerLines(data) {
		var s Sample
		if err := json.Unmarshal(line, &s); err != nil || s.TimestampNS == 0 {
			continue
		}
		samples = append(samples, s)
	}
	return samples, nil
}

func decode(r io.Reader) ([]Event, error) {
	scanner := bufio.NewScanner(bufioReader(r))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var events []Event
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) != nil {
			continue // tolerate trailing partial records
		}
		if e.StartNS == 0 && e.EndNS == 0 {
			continue
		}
		events = append(events, e)
	}
	return events, scanner.Err()
}

// Build converts CUPTI events (plus optional NVML samples) into a Perfetto trace.
func Build(events []Event, samples []Sample, sourcePath string, opts Options) (*perfetto.Trace, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no CUPTI events in %s", sourcePath)
	}
	var minStart uint64 = ^uint64(0)
	for i := range events {
		if events[i].StartNS < minStart {
			minStart = events[i].StartNS
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartNS < events[j].StartNS })

	trace := &perfetto.Trace{
		ClockDomain: "wall",
		GPUName:     "NVIDIA GPU",
		Metadata: map[string]any{
			"schema":          "gputrace.cupti/v1",
			"source":          sourcePath,
			"event_count":     len(events),
			"sample_count":    len(samples),
			"clock_domain":    "CUPTI activity timestamps normalized to capture start",
			"timebase":        "cupti activity record Start/End (ns)",
			"timing_quality":  "measured",
			"demangler":       "c++filt with raw-symbol fallback",
		},
	}

	kernelGroupUUID := perfetto.TrackUUID("gputrace.cupti", "kernels")
	kernelTrackUUID := perfetto.TrackUUID("gputrace.cupti", "kernels/all")
	memcpyTrackUUID := perfetto.TrackUUID("gputrace.cupti", "memcpy")

	trace.Tracks = append(trace.Tracks,
		perfetto.Track{UUID: kernelGroupUUID, Name: "CUDA kernels (CUPTI)"},
		perfetto.Track{
			UUID:        kernelTrackUUID,
			ParentUUID:  kernelGroupUUID,
			Name:        "all kernels",
			Description: "Per-launch kernel execution measured by CUPTI concurrent-kernel activity",
		},
		perfetto.Track{
			UUID:        memcpyTrackUUID,
			Name:        "Memory transfers (CUPTI)",
			Description: "Host<->device copies measured by CUPTI activity tracing",
		},
	)

	// Optional per-kernel-name child tracks under the group.
	type nameTrack struct{ uuid uint64 }
	nameTracks := map[string]nameTrack{}
	if opts.PerKernelTracks {
		names := map[string]bool{}
		for _, e := range events {
			if e.Kind == "kernel" {
				names[e.Name] = true
			}
		}
		keys := make([]string, 0, len(names))
		for n := range names {
			keys = append(keys, n)
		}
		sort.Strings(keys)
		for _, n := range keys {
			uuid := perfetto.TrackUUID("gputrace.cupti/kernel", n)
			nameTracks[n] = nameTrack{uuid}
			trace.Tracks = append(trace.Tracks, perfetto.Track{
				UUID:       uuid,
				ParentUUID: kernelGroupUUID,
				Name:       ShortName(Demangle(n)),
			})
		}
	}

	for i := range events {
		e := &events[i]
		ev := perfetto.Event{
			ID:         uint64(i + 1),
			StartNS:    e.StartNS - minStart,
			DurationNS: e.EndNS - e.StartNS,
			Args: map[string]any{
				"raw_symbol": e.Name,
				"grid":       e.Grid,
				"block":      e.Block,
				"registers":  e.Registers,
				"timebase":   "cupti_activity_ns",
			},
		}
		switch e.Kind {
		case "kernel":
			ev.Category = "cuda_kernel"
			ev.Name = ShortName(Demangle(e.Name))
			ev.Kind = perfetto.EventGPUCompute
			if t, ok := nameTracks[e.Name]; ok {
				ev.TrackUUID = t.uuid
			} else {
				ev.TrackUUID = kernelTrackUUID
			}
		case "memcpy", "memset":
			ev.Category = "cuda_" + e.Kind
			ev.Name = e.Kind
			if e.Bytes > 0 {
				ev.Args["bytes"] = e.Bytes
			}
			ev.TrackUUID = memcpyTrackUUID
			ev.Kind = perfetto.EventSlice
		default:
			continue
		}
		trace.Events = append(trace.Events, ev)
	}

	// NVML device counters as native Perfetto counter series, aligned to the
	// same normalized start.
	if len(samples) > 0 {
		addCounter := func(name, desc string, value func(Sample) float64) {
			c := perfetto.Counter{ID: uint32(len(trace.Counters) + 1), Name: name, Description: desc}
			for _, s := range samples {
				c.Samples = append(c.Samples, perfetto.CounterSample{
					TimestampNS: s.TimestampNS - minStart,
					Value:       value(s),
				})
			}
			trace.Counters = append(trace.Counters, c)
		}
		addCounter("GPU power", "Board power draw (W)", func(s Sample) float64 { return float64(s.PowerMW) / 1000 })
		addCounter("GPU utilization", "Percent of time one or more kernels execute (%)", func(s Sample) float64 { return float64(s.GPUUtilPct) })
		addCounter("Memory utilization", "Percent bandwidth utilization (%)", func(s Sample) float64 { return float64(s.MemUtilPct) })
		addCounter("GPU temperature", "Die temperature (C)", func(s Sample) float64 { return float64(s.TempC) })
		addCounter("Memory used", "Framebuffer memory in use (MiB)", func(s Sample) float64 { return float64(s.MemUsedB) / (1 << 20) })
		trace.Metadata["nvml_samples"] = len(samples)
		trace.Metadata["nvml_counter_source"] = "NVML sampled concurrently with execution"
	}
	return trace, nil
}

// Write renders trace as a Perfetto protobuf to w.
func Write(trace *perfetto.Trace, w io.Writer) error {
	return perfetto.Write(w, trace)
}
