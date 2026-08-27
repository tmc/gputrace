// Package gpuevent defines a vendor-neutral GPU activity model that every
// capture backend (CUPTI, Metal interposer, rocprofiler) projects into, so
// analysis and export code never sees vendor formats.
package gpuevent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Kind classifies one measured GPU activity.
type Kind string

const (
	// KindKernel is one kernel launch executing on the device.
	KindKernel Kind = "kernel"
	// KindMemcpy is one host<->device or device<->device copy.
	KindMemcpy Kind = "memcpy"
	// KindMemset is one device memory fill.
	KindMemset Kind = "memset"
)

// Event is one measured GPU activity with provenance-preserving fields:
// the vendor symbol and geometry stay available alongside any decoded name
// so analysis can cite evidence without re-deriving it.
type Event struct {
	Kind          Kind           `json:"kind,omitempty"`
	Name          string         `json:"name,omitempty"`   // decoded/display name
	RawSymbol     string         `json:"raw_symbol"`       // vendor-mangled original
	StartNS       uint64         `json:"start_ns"`
	EndNS         uint64         `json:"end_ns"`
	Grid          string         `json:"grid,omitempty"`
	Block         string         `json:"block,omitempty"`
	Registers     int            `json:"registers,omitempty"`
	SharedMem     int            `json:"shared_mem,omitempty"`          // static+dynamic, bytes
	LocalMemThread uint32       `json:"local_mem_per_thread,omitempty"` // bytes per thread
	ContextID     uint32         `json:"context_id,omitempty"`
	GraphID       uint32         `json:"graph_id,omitempty"`
	GraphNodeID   uint64         `json:"graph_node_id,omitempty"`
	SrcKind       string         `json:"src_kind,omitempty"` // memcpy memory kinds
	DstKind       string         `json:"dst_kind,omitempty"`
	Bytes         uint64         `json:"bytes,omitempty"` // memcpy/memset size
	DeviceID      uint32         `json:"device_id,omitempty"`
	StreamID      uint32         `json:"stream_id,omitempty"`
	CorrelationID uint64         `json:"correlation_id,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty"` // vendor extras
}

// DurationNS reports the measured execution duration.
func (e Event) DurationNS() uint64 {
	if e.EndNS <= e.StartNS {
		return 0
	}
	return e.EndNS - e.StartNS
}

// Sample is one device-level observation taken while a workload ran,
// recorded on the same clock as the events it annotates.
type Sample struct {
	TimestampNS uint64 `json:"timestamp_ns"`
	PowerMW     uint32 `json:"power_mw"`
	GPUUtilPct  uint32 `json:"gpu_util_pct"`
	MemUtilPct  uint32 `json:"mem_util_pct"`
	TempC       uint32 `json:"temp_c"`
	MemUsedB    uint64 `json:"mem_used_bytes"`
}

// APIEvent is one host-side CUDA runtime/driver call, recorded when the
// capture enables API tracing. CorrelationID joins it to the kernel or
// transfer it launched: host-side cost per launch is EndNS-StartNS, and
// KernelStartNS-EndNS is the submission latency into the GPU queue.
type APIEvent struct {
	API           string `json:"api"` // "runtime" | "driver"
	Name          string `json:"name,omitempty"`
	Cbid          uint32 `json:"cbid"`
	StartNS       uint64 `json:"start_ns"`
	EndNS         uint64 `json:"end_ns"`
	ThreadID      uint32 `json:"thread_id"`
	CorrelationID uint64 `json:"correlation_id"`
}

// LaunchJoin pairs one cudaLaunchKernel-style host call with the kernel it
// produced. It is the unit of launch-overhead analysis.
type LaunchJoin struct {
	CorrelationID   uint64 `json:"correlation_id"`
	Name            string `json:"name"`
	HostCostNS      uint64 `json:"host_cost_ns"`
	GPUDurationNS   uint64 `json:"gpu_duration_ns"`
	SubmitGapNS     int64  `json:"submit_gap_ns"` // kernel start - api end; negative = pre-queued
}

// LaunchOverhead summarizes the host-vs-device split across joined launches.
type LaunchOverhead struct {
	Joins            int     `json:"joins"`
	TotalHostNS      uint64  `json:"total_host_ns"`
	TotalGPUNS       uint64  `json:"total_gpu_ns"`
	MeanHostCostNS   uint64  `json:"mean_host_cost_ns"`
	P50HostCostNS    uint64  `json:"p50_host_cost_ns"`
	P95HostCostNS    uint64  `json:"p95_host_cost_ns"`
	MeanSubmitGapNS  float64 `json:"mean_submit_gap_ns"`
}

// ClockSync records the CUPTI timestamp domain against wall clock at one
// instant, letting readers align NVML samples even when the domains
// diverge across drivers or platforms.
type ClockSync struct {
	UnixNS uint64 `json:"unix_ns"`
	CuptiNS uint64 `json:"cupti_ns"`
}

// CaptureMeta carries capture-mode provenance from the shim.
type CaptureMeta struct {
	ConcurrentKernel bool `json:"concurrent_kernel"`
	PID              int  `json:"pid"`
}

// Capture is one decoded capture: activity events plus optional concurrent
// device samples sharing one clock domain.
type Capture struct {
	Events    []Event
	Samples   []Sample
	APIs      []APIEvent
	Spans     []Span
	ClockSync *ClockSync
	Meta      *CaptureMeta
}

// Normalize shifts every timestamp so the earliest event starts at zero,
// which lets separately captured sources join on one clock. It returns the
// subtracted origin so callers can report absolute time if needed.
func (c *Capture) Normalize() (origin uint64) {
	if len(c.Events) == 0 && len(c.Samples) == 0 && len(c.Spans) == 0 && len(c.APIs) == 0 {
		return 0
	}
	origin = ^uint64(0)
	scan := func(start uint64) {
		if start < origin {
			origin = start
		}
	}
	for _, e := range c.Events {
		scan(e.StartNS)
	}
	for _, s := range c.Samples {
		scan(s.TimestampNS)
	}
	for _, s := range c.Spans {
		scan(s.StartNS)
	}
	for _, a := range c.APIs {
		scan(a.StartNS)
	}
	if origin == ^uint64(0) {
		return 0
	}
	for i := range c.Events {
		c.Events[i].StartNS -= origin
		c.Events[i].EndNS -= origin
	}
	for i := range c.Samples {
		c.Samples[i].TimestampNS -= origin
	}
	for i := range c.Spans {
		c.Spans[i].StartNS -= origin
		c.Spans[i].EndNS -= origin
	}
	for i := range c.APIs {
		c.APIs[i].StartNS -= origin
		c.APIs[i].EndNS -= origin
	}
	return origin
}

// DecodeJSONL reads newline-delimited JSON records into a Capture. Kinds it
// recognizes become Events; records carrying only sample fields become
// Samples; undecodable lines are dropped so a tracer killed mid-write still
// yields everything before the partial record.
func DecodeJSONL(r io.Reader) (Capture, error) {
	var cap Capture
	scanner := bufio.NewScanner(bufioReader(r))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(data) == 0 {
			continue
		}
		var probe struct {
			Kind        string `json:"kind"`
			TimestampNS uint64 `json:"timestamp_ns"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			continue // tolerate trailing partial records
		}
		switch {
		case probe.Kind == "capture_meta":
			var m CaptureMeta
			if json.Unmarshal(data, &m) == nil {
				cap.Meta = &m
			}
		case probe.Kind == "clock_sync":
			var cs ClockSync
			if json.Unmarshal(data, &cs) == nil && cap.ClockSync == nil {
				cap.ClockSync = &cs
			}
		case probe.Kind == "span":
			sp, err := decodeSpan(data)
			if err == nil {
				cap.Spans = append(cap.Spans, sp)
			}
		case probe.Kind == "api":
			var a APIEvent
			if json.Unmarshal(data, &a) == nil && (a.StartNS != 0 || a.EndNS != 0) {
				cap.APIs = append(cap.APIs, a)
			}
		case probe.Kind != "":
			e, err := decodeEvent(data)
			if err != nil || (e.StartNS == 0 && e.EndNS == 0) {
				continue
			}
			cap.Events = append(cap.Events, e)
		case probe.TimestampNS != 0:
			var s Sample
			if json.Unmarshal(data, &s) != nil {
				continue
			}
			cap.Samples = append(cap.Samples, s)
		}
	}
	cap.translateSpanClocks()
	return cap, scanner.Err()
}

// translateSpanClocks maps spans stamped on the unix clock into the CUPTI
// timestamp domain using the capture's clock_sync record. Producers outside
// the shim (app-events sidecars) have no CUPTI handle, so they stamp
// CLOCK_REALTIME and declare clock "unix"; without a clock_sync record such
// spans stay untranslated and simply attribute no kernels.
func (c *Capture) translateSpanClocks() {
	if c.ClockSync == nil {
		return
	}
	delta := int64(c.ClockSync.CuptiNS) - int64(c.ClockSync.UnixNS)
	for i := range c.Spans {
		if c.Spans[i].Clock != ClockUnix {
			continue
		}
		c.Spans[i].StartNS = uint64(int64(c.Spans[i].StartNS) + delta)
		c.Spans[i].EndNS = uint64(int64(c.Spans[i].EndNS) + delta)
		c.Spans[i].Clock = ""
	}
}

func decodeEvent(data []byte) (Event, error) {
	var wire struct {
		Kind           string         `json:"kind"`
		Name           string         `json:"name"`
		RawSymbol      string         `json:"raw_symbol"`
		LegacyName     string         `json:"name_raw"` // older probes
		Symbol         string         `json:"symbol"`
		StartNS        uint64         `json:"start_ns"`
		EndNS          uint64         `json:"end_ns"`
		Grid           string         `json:"grid"`
		Block          string         `json:"block"`
		Registers      int            `json:"registers"`
		SharedMem      int            `json:"shared_mem"`
		LocalMemThread uint32         `json:"local_mem_per_thread"`
		ContextID      uint32         `json:"context_id"`
		GraphID        uint32         `json:"graph_id"`
		GraphNodeID    uint64         `json:"graph_node_id"`
		SrcKind        string         `json:"src_kind"`
		DstKind        string         `json:"dst_kind"`
		Bytes          uint64         `json:"bytes"`
		DeviceID       uint32         `json:"device_id"`
		StreamID       uint32         `json:"stream_id"`
		CorrelationID  uint64         `json:"correlation_id"`
		Attrs          map[string]any `json:"attrs"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return Event{}, err
	}
	symbol := wire.RawSymbol
	if symbol == "" {
		symbol = wire.Symbol
	}
	name := wire.Name
	if name == "" {
		name = wire.LegacyName
	}
	return Event{
		Kind:           Kind(wire.Kind),
		Name:           name,
		RawSymbol:      symbol,
		StartNS:        wire.StartNS,
		EndNS:          wire.EndNS,
		Grid:           wire.Grid,
		Block:          wire.Block,
		Registers:      wire.Registers,
		SharedMem:      wire.SharedMem,
		LocalMemThread: wire.LocalMemThread,
		ContextID:      wire.ContextID,
		GraphID:        wire.GraphID,
		GraphNodeID:    wire.GraphNodeID,
		SrcKind:        wire.SrcKind,
		DstKind:        wire.DstKind,
		Bytes:          wire.Bytes,
		DeviceID:       wire.DeviceID,
		StreamID:       wire.StreamID,
		CorrelationID:  wire.CorrelationID,
		Attrs:          wire.Attrs,
	}, nil
}

// String renders a short human form used in logs and findings evidence.
func (e Event) String() string {
	name := e.Name
	if name == "" {
		name = e.RawSymbol
	}
	return fmt.Sprintf("%s %s [%s..%s]", e.Kind, name, gridOrDash(e), blockOrDash(e))
}

func gridOrDash(e Event) string {
	if e.Grid == "" {
		return "-"
	}
	return e.Grid
}

func blockOrDash(e Event) string {
	if e.Block == "" {
		return "-"
	}
	return e.Block
}

func bufioReader(r io.Reader) io.Reader { return r }

// LaunchOverhead analyzes the host-vs-device split by joining API records
// (cudaLaunchKernel and friends) to the kernels they produced via
// correlation ID. It quantifies launch-bound behavior directly: host cost
// per submission, and how long kernels waited between submission and start.
func LaunchOverheadAnalysis(cap Capture) *LaunchOverhead {
	apisByCorrelation := make(map[uint64]APIEvent)
	for _, a := range cap.APIs {
		switch a.Name {
		case "cudaLaunchKernel", "cudaLaunchKernelExC", "cuLaunchKernel",
			"cuLaunchKernelEx", "cudaLaunch":
			apisByCorrelation[a.CorrelationID] = a
		}
	}
	if len(apisByCorrelation) == 0 {
		return &LaunchOverhead{}
	}
	kernelsByCorrelation := make(map[uint64]Event)
	for _, e := range cap.Events {
		if e.Kind == KindKernel && e.CorrelationID != 0 {
			kernelsByCorrelation[e.CorrelationID] = e
		}
	}
	var hostCosts []uint64
	out := &LaunchOverhead{}
	for cid, k := range kernelsByCorrelation {
		a, ok := apisByCorrelation[cid]
		if !ok {
			continue
		}
		host := a.EndNS - a.StartNS
		gpu := k.DurationNS()
		var gap int64
		if k.StartNS >= a.EndNS {
			gap = int64(k.StartNS - a.EndNS)
		} else {
			gap = -int64(a.EndNS - k.StartNS) // pre-queued before API returned
		}
		out.Joins++
		out.TotalHostNS += host
		out.TotalGPUNS += gpu
		out.MeanSubmitGapNS = float64(gap)
		hostCosts = append(hostCosts, host)
		_ = gap
	}
	sortU64(hostCosts)
	if out.Joins > 0 {
		out.MeanHostCostNS = out.TotalHostNS / uint64(out.Joins)
		out.P50HostCostNS = percentile(hostCosts, 0.50)
		out.P95HostCostNS = percentile(hostCosts, 0.95)
	}
	return out
}

func sortU64(v []uint64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
