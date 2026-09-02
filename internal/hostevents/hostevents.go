// Package hostevents reads measured host intervals emitted by an instrumented
// workload and binds them to live GPU timing evidence.
package hostevents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/hostcorrelation"
	"github.com/tmc/gputrace/internal/livetiming"
)

const (
	Schema         = "gputrace.host-event/v1"
	ClockDomain    = "cpu_uptime_ns"
	maximumBytes   = 8 << 20
	maximumLine    = 1 << 20
	maximumRecords = 100_000
)

// ErrInvalid reports malformed or inconsistent host-event evidence.
var ErrInvalid = errors.New("invalid host events")

type record struct {
	Schema      string `json:"schema"`
	Kind        string `json:"kind"`
	RunID       string `json:"run_id"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	ClockDomain string `json:"clock_domain"`
	TimestampNS uint64 `json:"timestamp_ns"`
	DurationNS  uint64 `json:"duration_ns"`
}

// Evidence is a validated host-event sidecar and its exact content identity.
type Evidence struct {
	RunID         string
	ContentDigest string
	Events        []hostcorrelation.Event
}

// Read reads a bounded newline-delimited host-event sidecar.
func Read(path string) (Evidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Evidence{}, fmt.Errorf("read host events: %w", err)
	}
	if len(data) == 0 || len(data) > maximumBytes {
		return Evidence{}, fmt.Errorf("%w: byte count must be in [1,%d]", ErrInvalid, maximumBytes)
	}
	sum := sha256.Sum256(data)
	evidence := Evidence{ContentDigest: "sha256:" + hex.EncodeToString(sum[:])}
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maximumLine)
	for scanner.Scan() {
		if len(evidence.Events) >= maximumRecords {
			return Evidence{}, fmt.Errorf("%w: too many records", ErrInvalid)
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var raw record
		if err := decoder.Decode(&raw); err != nil {
			return Evidence{}, fmt.Errorf("%w: decode record: %v", ErrInvalid, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Evidence{}, fmt.Errorf("%w: trailing record data", ErrInvalid)
		}
		if raw.Schema != Schema || raw.Kind != "interval" || raw.ClockDomain != ClockDomain ||
			raw.RunID == "" || raw.RunID != strings.TrimSpace(raw.RunID) || len(raw.RunID) > 256 ||
			raw.ID == "" || raw.ID != strings.TrimSpace(raw.ID) || len(raw.ID) > 256 ||
			raw.Name == "" || raw.Name != strings.TrimSpace(raw.Name) || len(raw.Name) > 4096 ||
			raw.DurationNS == 0 || raw.TimestampNS > uint64(^uint64(0)>>1) ||
			raw.DurationNS > uint64(^uint64(0)>>1)-raw.TimestampNS {
			return Evidence{}, fmt.Errorf("%w: malformed record", ErrInvalid)
		}
		if evidence.RunID == "" {
			evidence.RunID = raw.RunID
		} else if evidence.RunID != raw.RunID {
			return Evidence{}, fmt.Errorf("%w: mixed run IDs", ErrInvalid)
		}
		if _, ok := seen[raw.ID]; ok {
			return Evidence{}, fmt.Errorf("%w: duplicate event %q", ErrInvalid, raw.ID)
		}
		seen[raw.ID] = struct{}{}
		evidence.Events = append(evidence.Events, hostcorrelation.Event{
			ID: raw.ID, Kind: raw.Kind, Name: raw.Name,
			TimestampNS: int64(raw.TimestampNS), DurationNS: int64(raw.DurationNS),
		})
	}
	if err := scanner.Err(); err != nil {
		return Evidence{}, fmt.Errorf("%w: scan records: %v", ErrInvalid, err)
	}
	if len(evidence.Events) == 0 {
		return Evidence{}, fmt.Errorf("%w: no events", ErrInvalid)
	}
	sort.Slice(evidence.Events, func(i, j int) bool {
		return evidence.Events[i].TimestampNS < evidence.Events[j].TimestampNS
	})
	return evidence, nil
}

// Receipt binds host intervals to a live timing sidecar from the same run.
// Host intervals that fall outside the sidecar's sampled clock range cannot
// be bound and are returned as withheld rather than invalidating the events
// that can: sampling starts at the first Metal device, so host phases that
// begin earlier (process start, model load) are routinely unbindable while
// the rest of the run is fine.
func Receipt(hostPath, timingPath string) (hostcorrelation.Receipt, []hostcorrelation.Event, error) {
	host, err := Read(hostPath)
	if err != nil {
		return hostcorrelation.Receipt{}, nil, err
	}
	timing, err := livetiming.Read(timingPath)
	if err != nil {
		return hostcorrelation.Receipt{}, nil, err
	}
	if host.RunID != timing.RunID {
		return hostcorrelation.Receipt{}, nil, fmt.Errorf("%w: run identity differs", ErrInvalid)
	}
	samples := make([]hostcorrelation.ClockSample, len(timing.ClockSamples))
	for i, sample := range timing.ClockSamples {
		samples[i] = hostcorrelation.ClockSample{HostNS: sample.CPUTimeNS, GPUNS: sample.GPUTimeNS}
	}
	events := append([]hostcorrelation.Event(nil), host.Events...)
	var withheld []hostcorrelation.Event
	if len(samples) > 0 {
		first := samples[0].HostNS
		last := samples[len(samples)-1].HostNS
		bindable := events[:0]
		for _, event := range events {
			if event.TimestampNS < first || event.DurationNS > last-event.TimestampNS {
				withheld = append(withheld, event)
				continue
			}
			bindable = append(bindable, event)
		}
		events = bindable
	}
	if len(events) == 0 {
		return hostcorrelation.Receipt{}, withheld, fmt.Errorf("%w: no host event falls inside the sampled clock range", ErrInvalid)
	}
	receipt := hostcorrelation.Receipt{
		Schema: hostcorrelation.Schema,
		Host: hostcorrelation.Artifact{
			Kind: "host-signpost", RunID: host.RunID,
			ContentDigest: host.ContentDigest, ClockDomain: ClockDomain,
		},
		GPU: hostcorrelation.Artifact{
			Kind: "gpu-trace", RunID: timing.RunID,
			ContentDigest: timing.TraceDigest, ClockDomain: "live",
		},
		Bridge: &hostcorrelation.ClockBridge{
			HostClock: ClockDomain, GPUClock: "live",
			SourceDigest: timing.ContentDigest, Samples: samples,
		},
		Events: events,
	}
	if err := receipt.Validate(); err != nil {
		return hostcorrelation.Receipt{}, withheld, err
	}
	return receipt, withheld, nil
}
