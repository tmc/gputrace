// Package hostcorrelation validates evidence used to correlate host events
// with GPU traces.
package hostcorrelation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

const (
	Schema        = "gputrace.host-correlation/v1"
	maximumBytes  = 1 << 20
	maximumEvents = 100_000
)

var (
	ErrInvalid      = errors.New("invalid host correlation")
	ErrUncorrelated = errors.New("host and GPU artifacts are not correlated")
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Artifact identifies one retained capture artifact and its source clock.
type Artifact struct {
	Kind          string `json:"kind"`
	RunID         string `json:"run_id"`
	ContentDigest string `json:"content_digest"`
	ClockDomain   string `json:"clock_domain"`
}

// ClockBridge maps timestamps from the host clock to the GPU clock.
type ClockBridge struct {
	HostClock    string        `json:"host_clock"`
	GPUClock     string        `json:"gpu_clock"`
	SourceDigest string        `json:"source_digest"`
	Samples      []ClockSample `json:"samples"`
}

// ClockSample is one simultaneously observed host and GPU timestamp pair.
type ClockSample struct {
	HostNS int64 `json:"host_ns"`
	GPUNS  int64 `json:"gpu_ns"`
}

// Event is one host-clock signpost observation.
type Event struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	TimestampNS int64  `json:"timestamp_ns"`
	DurationNS  int64  `json:"duration_ns,omitempty"`
}

// ProjectedEvent is an event mapped into the GPU clock domain.
type ProjectedEvent struct {
	ID          string
	Kind        string
	Name        string
	TimestampNS int64
	DurationNS  int64
	MaxErrorNS  float64
}

// Receipt records the evidence required to authorize a temporal join.
type Receipt struct {
	Schema string       `json:"schema"`
	Host   Artifact     `json:"host"`
	GPU    Artifact     `json:"gpu"`
	Bridge *ClockBridge `json:"bridge,omitempty"`
	Events []Event      `json:"events"`
}

// Validate reports whether the receipt authorizes a host-to-GPU temporal join.
func (r Receipt) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("%w: unsupported schema %q", ErrInvalid, r.Schema)
	}
	if err := validateArtifact(r.Host, "host-signpost"); err != nil {
		return err
	}
	if err := validateArtifact(r.GPU, "gpu-trace"); err != nil {
		return err
	}
	if r.Host.RunID != r.GPU.RunID {
		return fmt.Errorf("%w: run identity differs", ErrUncorrelated)
	}
	if err := validateEvents(r.Events); err != nil {
		return err
	}
	if r.Host.ClockDomain == r.GPU.ClockDomain {
		if r.Bridge != nil {
			return fmt.Errorf("%w: bridge supplied for identical clocks", ErrInvalid)
		}
		return nil
	}
	if r.Bridge == nil {
		return fmt.Errorf("%w: clock domains differ without a bridge", ErrUncorrelated)
	}
	b := r.Bridge
	if b.HostClock != r.Host.ClockDomain || b.GPUClock != r.GPU.ClockDomain {
		return fmt.Errorf("%w: bridge clock identity differs", ErrUncorrelated)
	}
	if !validDigest(b.SourceDigest) {
		return fmt.Errorf("%w: malformed clock bridge", ErrInvalid)
	}
	if _, _, _, err := b.parameters(); err != nil {
		return err
	}
	first := b.Samples[0].HostNS
	last := b.Samples[len(b.Samples)-1].HostNS
	for _, event := range r.Events {
		if event.TimestampNS < first || event.DurationNS > last-event.TimestampNS {
			return fmt.Errorf("%w: event %q is outside sampled host clock range", ErrUncorrelated, event.ID)
		}
	}
	return nil
}

// Project maps host events into the GPU clock domain.
func (r Receipt) Project() ([]ProjectedEvent, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	scale, offset, maxError := 1.0, 0.0, 0.0
	if r.Bridge != nil {
		var err error
		scale, offset, maxError, err = r.Bridge.parameters()
		if err != nil {
			return nil, err
		}
	}
	projected := make([]ProjectedEvent, len(r.Events))
	for i, event := range r.Events {
		timestamp, ok := project(event.TimestampNS, scale, offset)
		if !ok {
			return nil, fmt.Errorf("%w: event %q timestamp overflows projected clock", ErrInvalid, event.ID)
		}
		duration, ok := project(event.DurationNS, scale, 0)
		if !ok {
			return nil, fmt.Errorf("%w: event %q duration overflows projected clock", ErrInvalid, event.ID)
		}
		projected[i] = ProjectedEvent{
			ID: event.ID, Kind: event.Kind, Name: event.Name,
			TimestampNS: timestamp, DurationNS: duration, MaxErrorNS: maxError,
		}
	}
	return projected, nil
}

// Canonical returns the canonical JSON encoding of a valid receipt.
func (r Receipt) Canonical() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// Read reads a bounded canonical receipt from path.
func Read(path string) (Receipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("read host correlation: %w", err)
	}
	if len(data) > maximumBytes {
		return Receipt{}, fmt.Errorf("%w: receipt exceeds %d bytes", ErrInvalid, maximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("%w: decode receipt: %v", ErrInvalid, err)
	}
	canonical, err := receipt.Canonical()
	if err != nil {
		return Receipt{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Receipt{}, fmt.Errorf("%w: receipt is not canonical", ErrInvalid)
	}
	return receipt, nil
}

func validateArtifact(a Artifact, kind string) error {
	if a.Kind != kind || a.RunID == "" || a.RunID != strings.TrimSpace(a.RunID) || len(a.RunID) > 256 ||
		a.ClockDomain == "" || a.ClockDomain != strings.TrimSpace(a.ClockDomain) ||
		len(a.ClockDomain) > 256 || !validDigest(a.ContentDigest) {
		return fmt.Errorf("%w: malformed %s artifact", ErrInvalid, kind)
	}
	return nil
}

func validateEvents(events []Event) error {
	if len(events) == 0 || len(events) > maximumEvents {
		return fmt.Errorf("%w: event count must be in [1,%d]", ErrInvalid, maximumEvents)
	}
	seen := make(map[string]struct{}, len(events))
	var previous int64
	for i, event := range events {
		if event.ID == "" || event.ID != strings.TrimSpace(event.ID) || len(event.ID) > 256 ||
			event.Name == "" || event.Name != strings.TrimSpace(event.Name) || len(event.Name) > 4096 ||
			(event.Kind != "instant" && event.Kind != "interval") || event.TimestampNS < 0 ||
			event.DurationNS < 0 || (event.Kind == "instant" && event.DurationNS != 0) ||
			(event.Kind == "interval" && event.DurationNS == 0) {
			return fmt.Errorf("%w: malformed event at index %d", ErrInvalid, i)
		}
		if i > 0 && event.TimestampNS < previous {
			return fmt.Errorf("%w: events are not timestamp ordered", ErrInvalid)
		}
		if _, ok := seen[event.ID]; ok {
			return fmt.Errorf("%w: duplicate event %q", ErrInvalid, event.ID)
		}
		seen[event.ID] = struct{}{}
		previous = event.TimestampNS
	}
	return nil
}

func validDigest(s string) bool {
	return digestPattern.MatchString(s) && s != "sha256:"+strings.Repeat("0", 64)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (b ClockBridge) parameters() (scale, offset, maxError float64, err error) {
	if len(b.Samples) < 3 || len(b.Samples) > 4096 {
		return 0, 0, 0, fmt.Errorf("%w: clock bridge needs 3 to 4096 samples", ErrInvalid)
	}
	for i, sample := range b.Samples {
		if sample.HostNS < 0 || sample.GPUNS < 0 ||
			i > 0 && (sample.HostNS <= b.Samples[i-1].HostNS || sample.GPUNS <= b.Samples[i-1].GPUNS) {
			return 0, 0, 0, fmt.Errorf("%w: clock samples are not strictly ordered", ErrInvalid)
		}
	}
	first, last := b.Samples[0], b.Samples[len(b.Samples)-1]
	scale = float64(last.GPUNS-first.GPUNS) / float64(last.HostNS-first.HostNS)
	offset = float64(first.GPUNS) - float64(first.HostNS)*scale
	if !finite(scale) || scale <= 0 || !finite(offset) {
		return 0, 0, 0, fmt.Errorf("%w: malformed clock bridge", ErrInvalid)
	}
	for _, sample := range b.Samples {
		predicted := float64(sample.HostNS)*scale + offset
		residual := math.Abs(float64(sample.GPUNS) - predicted)
		if residual > maxError {
			maxError = residual
		}
	}
	return scale, offset, maxError, nil
}

func project(value int64, scale, offset float64) (int64, bool) {
	v := float64(value)*scale + offset
	if !finite(v) || v < 0 || v >= float64(math.MaxInt64) {
		return 0, false
	}
	return int64(math.Round(v)), true
}
