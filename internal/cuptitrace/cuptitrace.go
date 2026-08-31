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

// APIEvent is one host-side CUDA runtime/driver call record.
type APIEvent = gpuevent.APIEvent

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
// bundle directory (including per-PID event shards, merged on read).
func ReadInput(path string) ([]Event, error) {
	cap, err := ReadCapture(path)
	if err != nil {
		return nil, err
	}
	return cap.Events, err
}

// ReadCapture reads the full capture: events plus any host API records
// and metadata records found alongside them.
func ReadCapture(path string) (gpuevent.Capture, error) {
	r, closers, err := cupticapture.OpenEvents(path)
	if err != nil {
		return gpuevent.Capture{}, err
	}
	defer closers()
	return gpuevent.DecodeJSONL(r)
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

// Build converts CUPTI events (plus optional NVML samples, host API
// records, and application spans) into a Perfetto trace.
func Build(events []Event, samples []Sample, apis []APIEvent, sourcePath string, opts Options) (*perfetto.Trace, error) {
	return build(gpuevent.Capture{Events: events, Samples: samples, APIs: apis}, sourcePath, opts)
}

// BuildCapture converts a decoded capture into a Perfetto trace. Spans in
// the capture render as parent slices on per-label tracks with attributed
// kernels nested beneath; kernels matching no span keep the flat tracks.
func BuildCapture(cap gpuevent.Capture, sourcePath string, opts Options) (*perfetto.Trace, error) {
	if len(cap.Events) == 0 {
		return nil, fmt.Errorf("no CUPTI events in %s", sourcePath)
	}
	return build(cap, sourcePath, opts)
}

func build(cap gpuevent.Capture, sourcePath string, opts Options) (*perfetto.Trace, error) {
	origin := cap.Normalize()
	events, samples, apis := cap.Events, cap.Samples, cap.APIs
	spans := gpuevent.AttributeSpans(cap)
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartNS < events[j].StartNS })

	trace := &perfetto.Trace{
		ClockDomain: "wall",
		API:         "CUDA",
		GPUName:     "NVIDIA GPU",
		GPUVendor:   "NVIDIA",
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
	// Normalize() rebased every timestamp to the capture's own start, so on
	// its own this trace begins at zero and so does every other one -- which
	// is why two captures could not be placed on a shared timeline. The shim
	// records a CLOCK_REALTIME/CUPTI pair; carry the wall time of that zero
	// so a viewer can put two processes' traces in their true relative
	// positions. Without a sync record the anchor stays unset and the trace
	// keeps its own clock rather than claiming a wall time it does not have.
	if cs := cap.ClockSync; cs != nil && cs.UnixNS != 0 && cs.CuptiNS != 0 {
		if anchor, ok := realtimeAnchor(origin, *cs); ok {
			trace.RealtimeAnchorNS = anchor
			trace.Metadata["clock_realtime_anchor_ns"] = anchor
		}
	}

	kernelGroupUUID := perfetto.TrackUUID("gputrace.cupti", "kernels")
	kernelTrackUUID := perfetto.TrackUUID("gputrace.cupti", "kernels/all")
	memcpyGroupUUID := perfetto.TrackUUID("gputrace.cupti", "memcpy")
	memcpyTrackUUID := perfetto.TrackUUID("gputrace.cupti", "memcpy/all")

	// Streams with observed activity get their own kernel tracks: real
	// concurrency becomes visible as parallel lanes instead of one
	// interleaved row.
	streamTracks := map[uint32]uint64{}
	for _, e := range events {
		if e.Kind == gpuevent.KindKernel {
			streamTracks[e.StreamID] = 0
		}
	}
	for sid := range streamTracks {
		streamTracks[sid] = perfetto.TrackUUID("gputrace.cupti/stream", fmt.Sprint(sid))
	}

	trace.Tracks = append(trace.Tracks,
		perfetto.Track{UUID: kernelGroupUUID, Name: "CUDA kernels (CUPTI)"},
		perfetto.Track{
			UUID:        kernelTrackUUID,
			ParentUUID:  kernelGroupUUID,
			Name:        "all kernels",
			Description: "Per-launch kernel execution measured by CUPTI concurrent-kernel activity",
		},
	)
	// Deterministic stream track order.
	streamIDs := make([]uint32, 0, len(streamTracks))
	for sid := range streamTracks {
		streamIDs = append(streamIDs, sid)
	}
	sort.Slice(streamIDs, func(i, j int) bool { return streamIDs[i] < streamIDs[j] })
	for _, sid := range streamIDs {
		trace.Tracks = append(trace.Tracks, perfetto.Track{
			UUID:       streamTracks[sid],
			ParentUUID: kernelGroupUUID,
			Name:       fmt.Sprintf("stream %d", sid),
		})
	}
	trace.Tracks = append(trace.Tracks,
		perfetto.Track{UUID: memcpyGroupUUID, Name: "Memory transfers (CUPTI)"},
		perfetto.Track{
			UUID:        memcpyTrackUUID,
			ParentUUID:  memcpyGroupUUID,
			Name:        "all transfers",
			Description: "Host<->device copies measured by CUPTI activity tracing; src/dst memory kinds in slice args",
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

	// Spans render luminal-style: each named phase has a track under an
	// "Application" group, with attributed kernels emitted as child slices on
	// that track — SLICE_BEGIN/Slice_END nesting puts them inside the span on
	// the timeline. Repeated phase names share their track; they are distinct
	// slices, not duplicate track descriptors. Span labels become debug
	// annotations. Kernels attributed to a span are not duplicated on the flat
	// tracks; unattributed kernels stay there.
	attributedKernelIDs := make(map[kernelKeyT]bool)
	nextEventID := uint64(len(events) + 1) // flat loop reuses i+1; spans take IDs beyond it
	if len(spans) > 0 {
		appGroupUUID := perfetto.TrackUUID("gputrace.cupti", "app")
		trace.Tracks = append(trace.Tracks, perfetto.Track{
			UUID:        appGroupUUID,
			Name:        "Application spans",
			Description: "Host-declared eval/phase spans from the app-events sidecar; attributed kernels nested beneath",
		})
		spanTracks := make(map[string]uint64)
		for _, s := range spans {
			trackUUID, ok := spanTracks[s.Name]
			if !ok {
				trackUUID = perfetto.TrackUUID("gputrace.cupti/span", s.Name)
				spanTracks[s.Name] = trackUUID
				trace.Tracks = append(trace.Tracks, perfetto.Track{
					UUID:       trackUUID,
					ParentUUID: appGroupUUID,
					Name:       ShortName(s.Name),
				})
			}
			args := map[string]any{"eval_seq": s.EvalSeq}
			for k, v := range s.Labels {
				args["label."+k] = v
			}
			trace.Events = append(trace.Events, perfetto.Event{
				ID:         nextEventID,
				TrackUUID:  trackUUID,
				Name:       ShortName(s.Name),
				Category:   "app_span",
				StartNS:    s.StartNS,
				DurationNS: s.EndNS - s.StartNS,
				Kind:       perfetto.EventSlice,
				Required:   true,
				Args:       args,
			})
			nextEventID++
			for _, k := range s.Kernels {
				attributedKernelIDs[kernelKey(k.Event)] = true
				trace.Events = append(trace.Events, perfetto.Event{
					ID:         nextEventID,
					TrackUUID:  trackUUID,
					Name:       ShortName(Demangle(DisplayName(k.Event))),
					Category:   "cuda_kernel",
					StartNS:    k.StartNS,
					DurationNS: k.DurationNS(),
					Kind:       perfetto.EventGPUCompute,
					Args: map[string]any{
						"raw_symbol":     k.RawSymbol,
						"grid":           k.Grid,
						"block":          k.Block,
						"registers":      k.Registers,
						"stream_id":      k.StreamID,
						"correlation_id": k.CorrelationID,
						"attribution":    k.Attribution,
						"timebase":       "cupti_activity_ns",
					},
				})
				nextEventID++
			}
		}
	}

	for i := range events {
		e := &events[i]
		if e.Kind == gpuevent.KindKernel && attributedKernelIDs[kernelKey(*e)] {
			continue // rendered nested under its span
		}
		ev := perfetto.Event{
			ID:         uint64(i + 1),
			StartNS:    e.StartNS,
			DurationNS: e.DurationNS(),
			Args: map[string]any{
				"raw_symbol":     e.RawSymbol,
				"grid":           e.Grid,
				"block":          e.Block,
				"registers":      e.Registers,
				"timebase":       "cupti_activity_ns",
				"stream_id":      e.StreamID,
				"correlation_id": e.CorrelationID,
			},
		}
		// Launch latency renders as durations, never as the raw activity
		// timestamps: those sit in a different clock domain from the
		// normalized slice times around them.
		if e.Latency.Known {
			ev.Args["queue_to_submit_ns"] = e.Latency.QueueToSubmitNS
			ev.Args["submit_to_start_ns"] = e.Latency.SubmitToStartNS
		}
		switch e.Kind {
		case gpuevent.KindKernel:
			ev.Category = "cuda_kernel"
			ev.Name = ShortName(Demangle(e.Name))
			ev.Kind = perfetto.EventGPUCompute
			if t, ok := nameTracks[e.Name]; ok {
				ev.TrackUUID = t
			} else if tu, ok := streamTracks[e.StreamID]; ok && !opts.PerKernelTracks {
				ev.TrackUUID = tu
			} else {
				ev.TrackUUID = kernelTrackUUID
			}
		case gpuevent.KindMemcpy, gpuevent.KindMemset:
			ev.Category = "cuda_" + string(e.Kind)
			if e.Kind == gpuevent.KindMemcpy {
				ev.Args["src_kind"] = e.SrcKind
				ev.Args["dst_kind"] = e.DstKind
			}
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

	// Host-side API call records become slices on per-thread tracks, so the
	// launch-submission cost and the gap between host submission and GPU
	// start are visible directly in the timeline.
	apiTrackUUID := perfetto.TrackUUID("gputrace.cupti", "api")
	threadTracks := map[uint32]uint64{}
	if len(apis) > 0 {
		trace.Tracks = append(trace.Tracks, perfetto.Track{
			UUID:        apiTrackUUID,
			Name:        "Host CUDA API calls (CUPTI)",
			Description: "Runtime/driver call timing on the submitting thread; join to kernels via correlation_id",
		})
		for _, a := range apis {
			tt, ok := threadTracks[a.ThreadID]
			if !ok {
				tt = perfetto.TrackUUID("gputrace.cupti/thread", fmt.Sprint(a.ThreadID))
				threadTracks[a.ThreadID] = tt
				trace.Tracks = append(trace.Tracks, perfetto.Track{
					UUID:       tt,
					ParentUUID: apiTrackUUID,
					Name:       fmt.Sprintf("thread %d", a.ThreadID),
				})
			}
			dur := uint64(0)
			if a.EndNS > a.StartNS {
				dur = a.EndNS - a.StartNS
			}
			name := a.Name
			if name == "" {
				name = fmt.Sprintf("cbid %d", a.Cbid)
			}
			trace.Events = append(trace.Events, perfetto.Event{
				ID:         uint64(len(trace.Events) + 1),
				TrackUUID:  tt,
				Name:       name,
				Category:   "cuda_api_" + a.API,
				StartNS:    a.StartNS,
				DurationNS: dur,
				Kind:       perfetto.EventSlice,
				Args: map[string]any{
					"cbid":           a.Cbid,
					"correlation_id": a.CorrelationID,
					"timebase":       "cupti_activity_ns",
				},
			})
		}
		sort.SliceStable(trace.Events, func(i, j int) bool {
			return trace.Events[i].StartNS < trace.Events[j].StartNS
		})
		trace.Metadata["api_record_count"] = len(apis)
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

// kernelKey identifies an event for span-attribution bookkeeping. Events
// carry a map field and are not comparable; correlation ID plus start is
// unique within one process's capture.
type kernelKeyT struct {
	CorrelationID uint64
	StartNS       uint64
	Stream        uint32
}

func kernelKey(e gpuevent.Event) kernelKeyT {
	return kernelKeyT{e.CorrelationID, e.StartNS, e.StreamID}
}

// DisplayName returns the name to show for one activity record: the
// decoded name when the capture supplied one, and the vendor symbol
// otherwise. It exists so every reader of a bundle resolves names the same
// way. Three readers once did not, and the two that grouped on Name alone
// reported a capture of twenty kernels as "1 distinct kernels" — the same
// class of failure as a silently partial capture, arrived at by a
// different route.
//
// Callers that want a readable name pass the result through Demangle; the
// fallback and the demangling are separate steps because a structure dump
// needs the fallback and has nothing to demangle.
func DisplayName(e gpuevent.Event) string {
	if e.Name != "" {
		return e.Name
	}
	return e.RawSymbol
}

// realtimeAnchor converts a capture-relative origin into the CLOCK_REALTIME
// nanosecond value of source timestamp zero.
//
// The shim records one (unix, cupti) pair. On the machines seen so far CUPTI
// activity timestamps are already unix-epoch nanoseconds and the two differ by
// a few hundred nanoseconds, but the delta is applied rather than assumed --
// nothing in CUPTI's contract promises that epoch.
//
// The delta is signed even though both inputs are unsigned, so it is computed
// in that direction explicitly. A capture whose arithmetic would underflow
// gets no anchor rather than a wrapped one.
func realtimeAnchor(origin uint64, cs gpuevent.ClockSync) (uint64, bool) {
	if cs.UnixNS >= cs.CuptiNS {
		return origin + (cs.UnixNS - cs.CuptiNS), true
	}
	back := cs.CuptiNS - cs.UnixNS
	if back > origin {
		return 0, false
	}
	return origin - back, true
}
