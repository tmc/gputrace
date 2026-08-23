// Package cuptitrace converts CUPTI activity JSONL captures into gputrace's
// native Perfetto trace format. Decoding and normalization live in
// internal/gpuevent; this package owns CUPTI-specific presentation:
// c++filt demangling, per-kernel track layout, and Perfetto projection.
package cuptitrace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/perfetto"
)

// Event is one decoded CUPTI activity record.
type Event = gpuevent.Event

// Sample is one NVML device observation taken while the workload ran.
type Sample = gpuevent.Sample

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
	cap, err := gpuevent.DecodeJSONL(f)
	return cap.Events, err
}

// ReadInput reads events from either a bare JSONL file or a .gpucapture
// bundle directory.
func ReadInput(path string) ([]Event, error) {
	eventsPath, err := cupticapture.ResolveEvents(path)
	if err != nil {
		return nil, err
	}
	return ReadJSONL(eventsPath)
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
	cap, err := gpuevent.DecodeJSONL(f)
	return cap.Samples, err
}

// Build converts CUPTI events (plus optional NVML samples) into a Perfetto trace.
func Build(events []Event, samples []Sample, sourcePath string, opts Options) (*perfetto.Trace, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no CUPTI events in %s", sourcePath)
	}
	cap := gpuevent.Capture{Events: events, Samples: samples}
	origin := cap.Normalize()
	events, samples = cap.Events, cap.Samples
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartNS < events[j].StartNS })

	trace := &perfetto.Trace{
		ClockDomain: "wall",
		GPUName:     "NVIDIA GPU",
		Metadata: map[string]any{
			"schema":         "gputrace.cupti/v1",
			"source":         sourcePath,
			"event_count":    len(events),
			"sample_count":   len(samples),
			"clock_domain":   "CUPTI activity timestamps normalized to capture start",
			"timebase":       "cupti activity record Start/End (ns)",
			"timing_quality": "measured",
			"demangler":      "c++filt with raw-symbol fallback",
		},
	}
	if origin != 0 {
		trace.Metadata["clock_origin_ns"] = origin
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
	nameTracks := map[string]uint64{}
	if opts.PerKernelTracks {
		names := map[string]bool{}
		for _, e := range events {
			if e.Kind == gpuevent.KindKernel {
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
			nameTracks[n] = uuid
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
			StartNS:    e.StartNS,
			DurationNS: e.DurationNS(),
			Args: map[string]any{
				"raw_symbol": e.RawSymbol,
				"grid":       e.Grid,
				"block":      e.Block,
				"registers":  e.Registers,
				"timebase":   "cupti_activity_ns",
			},
		}
		switch e.Kind {
		case gpuevent.KindKernel:
			ev.Category = "cuda_kernel"
			ev.Name = ShortName(Demangle(e.Name))
			ev.Kind = perfetto.EventGPUCompute
			if t, ok := nameTracks[e.Name]; ok {
				ev.TrackUUID = t
			} else {
				ev.TrackUUID = kernelTrackUUID
			}
		case gpuevent.KindMemcpy, gpuevent.KindMemset:
			ev.Category = "cuda_" + string(e.Kind)
			ev.Name = string(e.Kind)
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

	// NVML device counters as native Perfetto counter series on the same
	// normalized clock.
	if len(samples) > 0 {
		addCounter := func(name, desc string, value func(Sample) float64) {
			c := perfetto.Counter{ID: uint32(len(trace.Counters) + 1), Name: name, Description: desc}
			for _, s := range samples {
				c.Samples = append(c.Samples, perfetto.CounterSample{
					TimestampNS: s.TimestampNS,
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

// Demangle converts an Itanium-mangled kernel symbol into a readable name.
// It shells out to c++filt (binutils, present on essentially all Linux
// systems with a CUDA toolchain) and falls back to the raw symbol when
// c++filt is unavailable or rejects the input. Results are memoized because
// GPU workloads launch the same kernels thousands of times.
var (
	demangleMu     sync.Mutex
	demangleCache  = make(map[string]string)
	demangleFilt   string
	demangleLookup sync.Once
)

func cxxfiltPath() string {
	demangleLookup.Do(func() {
		p, err := exec.LookPath("c++filt")
		if err != nil {
			p = ""
		}
		demangleFilt = p
	})
	return demangleFilt
}

// Demangle returns a readable form of a mangled C++ symbol.
func Demangle(symbol string) string {
	if !strings.HasPrefix(symbol, "_Z") {
		return symbol
	}
	demangleMu.Lock()
	defer demangleMu.Unlock()
	if cached, ok := demangleCache[symbol]; ok {
		return cached
	}
	name := symbol
	if filt := cxxfiltPath(); filt != "" {
		if out, err := exec.Command(filt, symbol).Output(); err == nil {
			name = strings.TrimSpace(string(out))
		}
	}
	// Keep only the qualified function name; the full template argument list
	// is retained but signature parameter types after "(" are dropped for
	// track readability.
	if i := strings.Index(name, "("); i > 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), ")"))
	if name == "" || name == symbol {
		name = symbol // fall back when c++filt cannot demangle
	}
	demangleCache[symbol] = name
	return name
}

// ShortName collapses a long demangled template instantiation so Perfetto
// track names stay readable.
func ShortName(demangled string) string {
	const maxLen = 96
	if len(demangled) <= maxLen {
		return demangled
	}
	return demangled[:maxLen-3] + "..."
}
