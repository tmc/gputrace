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
	Kind          Kind           `json:"kind"`
	Name          string         `json:"name,omitempty"`   // decoded/display name
	RawSymbol     string         `json:"raw_symbol"`       // vendor-mangled original
	StartNS       uint64         `json:"start_ns"`
	EndNS         uint64         `json:"end_ns"`
	Grid          string         `json:"grid,omitempty"`
	Block         string         `json:"block,omitempty"`
	Registers     int            `json:"registers,omitempty"`
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

// Capture is one decoded capture: activity events plus optional concurrent
// device samples sharing one clock domain.
type Capture struct {
	Events  []Event
	Samples []Sample
}

// Normalize shifts every timestamp so the earliest event starts at zero,
// which lets separately captured sources join on one clock. It returns the
// subtracted origin so callers can report absolute time if needed.
func (c *Capture) Normalize() (origin uint64) {
	if len(c.Events) == 0 && len(c.Samples) == 0 {
		return 0
	}
	origin = ^uint64(0)
	for _, e := range c.Events {
		if e.StartNS < origin {
			origin = e.StartNS
		}
	}
	for _, s := range c.Samples {
		if s.TimestampNS < origin {
			origin = s.TimestampNS
		}
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
	return cap, scanner.Err()
}

func decodeEvent(data []byte) (Event, error) {
	var wire struct {
		Kind          string         `json:"kind"`
		Name          string         `json:"name"`
		RawSymbol     string         `json:"raw_symbol"`
		LegacyName    string         `json:"name_raw"` // older probes
		Symbol        string         `json:"symbol"`
		StartNS       uint64         `json:"start_ns"`
		EndNS         uint64         `json:"end_ns"`
		Grid          string         `json:"grid"`
		Block         string         `json:"block"`
		Registers     int            `json:"registers"`
		Bytes         uint64         `json:"bytes"`
		DeviceID      uint32         `json:"device_id"`
		StreamID      uint32         `json:"stream_id"`
		CorrelationID uint64         `json:"correlation_id"`
		Attrs         map[string]any `json:"attrs"`
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
		Kind:          Kind(wire.Kind),
		Name:          name,
		RawSymbol:     symbol,
		StartNS:       wire.StartNS,
		EndNS:         wire.EndNS,
		Grid:          wire.Grid,
		Block:         wire.Block,
		Registers:     wire.Registers,
		Bytes:         wire.Bytes,
		DeviceID:      wire.DeviceID,
		StreamID:      wire.StreamID,
		CorrelationID: wire.CorrelationID,
		Attrs:         wire.Attrs,
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
