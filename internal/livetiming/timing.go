// Package livetiming reads command-buffer timing recorded by the capture
// interposer during the original workload execution.
package livetiming

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

const (
	maximumBytes   = 64 << 20
	maximumLine    = 1 << 20
	maximumRecords = 1_000_000
)

// ErrInvalid reports a malformed or internally inconsistent timing sidecar.
var ErrInvalid = errors.New("invalid live timing sidecar")

// ErrNoTraceCarrier reports timing evidence whose native capture has no
// replayable Metal command stream.
var ErrNoTraceCarrier = errors.New("live timing sidecar has no trace carrier")

// ClockSample is one simultaneously sampled CPU and GPU timestamp pair.
type ClockSample struct {
	CPUTimeNS int64
	GPUTimeNS int64
}

// CommandBuffer is one completed command buffer in the live GPU clock.
type CommandBuffer struct {
	ID            uint64
	CaptureLabel  string
	FinalLabel    string
	GPUStartNS    int64
	GPUEndNS      int64
	KernelStartNS int64
	KernelEndNS   int64
	Status        int
}

// Sidecar is a validated original-execution timing observation.
type Sidecar struct {
	RunID          string
	ContentDigest  string
	TraceDigest    string
	CaptureDigest  string
	CaptureStatus  string
	ClockSamples   []ClockSample
	CommandBuffers []CommandBuffer
}

type record struct {
	Kind               string   `json:"kind"`
	RunID              string   `json:"run_id"`
	CPUTicks           *uint64  `json:"cpu_ticks,omitempty"`
	GPUTicks           *uint64  `json:"gpu_ticks,omitempty"`
	ID                 *uint64  `json:"id,omitempty"`
	CaptureLabel       string   `json:"capture_label,omitempty"`
	FinalLabel         string   `json:"final_label,omitempty"`
	GPUStartSeconds    *float64 `json:"gpu_start_seconds,omitempty"`
	GPUEndSeconds      *float64 `json:"gpu_end_seconds,omitempty"`
	KernelStartSeconds *float64 `json:"kernel_start_seconds,omitempty"`
	KernelEndSeconds   *float64 `json:"kernel_end_seconds,omitempty"`
	Status             *int     `json:"status,omitempty"`
	TraceDigest        string   `json:"trace_digest,omitempty"`
	CaptureDigest      string   `json:"capture_digest,omitempty"`
	CaptureStatus      string   `json:"capture_status,omitempty"`
	Reason             string   `json:"reason,omitempty"`
}

// Read reads and validates a bounded newline-delimited JSON sidecar.
func Read(path string) (Sidecar, error) {
	sidecar, err := Inspect(path)
	if err != nil {
		return Sidecar{}, err
	}
	if sidecar.TraceDigest == "" {
		return Sidecar{}, fmt.Errorf("%w: capture status %q", ErrNoTraceCarrier, sidecar.CaptureStatus)
	}
	return sidecar, nil
}

// Inspect reads timing observations even when capture produced no replayable
// command stream. Such observations cannot be joined to trace records.
func Inspect(path string) (Sidecar, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Sidecar{}, fmt.Errorf("read live timing sidecar: %w", err)
	}
	if len(data) == 0 || len(data) > maximumBytes {
		return Sidecar{}, fmt.Errorf("%w: byte count must be in [1,%d]", ErrInvalid, maximumBytes)
	}
	sum := sha256.Sum256(data)
	sidecar := Sidecar{ContentDigest: "sha256:" + hex.EncodeToString(sum[:])}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maximumLine)
	seenIDs := make(map[uint64]struct{})
	seenLabels := make(map[string]struct{})
	terminalSeen := false
	for scanner.Scan() {
		if terminalSeen {
			return Sidecar{}, fmt.Errorf("%w: terminal record is not last", ErrInvalid)
		}
		if len(sidecar.ClockSamples)+len(sidecar.CommandBuffers) >= maximumRecords {
			return Sidecar{}, fmt.Errorf("%w: too many records", ErrInvalid)
		}
		line := scanner.Bytes()
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var raw record
		if err := decoder.Decode(&raw); err != nil {
			return Sidecar{}, fmt.Errorf("%w: decode record: %v", ErrInvalid, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Sidecar{}, fmt.Errorf("%w: trailing record data", ErrInvalid)
		}
		if err := validateRun(&sidecar, raw.RunID); err != nil {
			return Sidecar{}, err
		}
		switch raw.Kind {
		case "clock_sample":
			if raw.CPUTicks == nil || raw.GPUTicks == nil || hasCommandFields(raw) {
				return Sidecar{}, fmt.Errorf("%w: malformed clock sample", ErrInvalid)
			}
			if *raw.CPUTicks > math.MaxInt64 || *raw.GPUTicks > math.MaxInt64 {
				return Sidecar{}, fmt.Errorf("%w: clock sample overflows", ErrInvalid)
			}
			sidecar.ClockSamples = append(sidecar.ClockSamples, ClockSample{
				CPUTimeNS: int64(*raw.CPUTicks), GPUTimeNS: int64(*raw.GPUTicks),
			})
		case "command_buffer":
			command, err := decodeCommand(raw)
			if err != nil {
				return Sidecar{}, err
			}
			if _, ok := seenIDs[command.ID]; ok {
				return Sidecar{}, fmt.Errorf("%w: duplicate command-buffer ID", ErrInvalid)
			}
			if _, ok := seenLabels[command.CaptureLabel]; ok {
				return Sidecar{}, fmt.Errorf("%w: duplicate capture label", ErrInvalid)
			}
			seenIDs[command.ID] = struct{}{}
			seenLabels[command.CaptureLabel] = struct{}{}
			sidecar.CommandBuffers = append(sidecar.CommandBuffers, command)
		case "artifact":
			if raw.CPUTicks != nil || raw.GPUTicks != nil || hasNonArtifactFields(raw) ||
				raw.CaptureDigest != "" || raw.CaptureStatus != "" || raw.Reason != "" || !validDigest(raw.TraceDigest) {
				return Sidecar{}, fmt.Errorf("%w: malformed artifact record", ErrInvalid)
			}
			sidecar.TraceDigest = raw.TraceDigest
			terminalSeen = true
		case "capture_attempt":
			if raw.CPUTicks != nil || raw.GPUTicks != nil || hasNonArtifactFields(raw) || raw.TraceDigest != "" ||
				raw.CaptureStatus != "timing_only" || raw.Reason != "no_command_stream" || !validDigest(raw.CaptureDigest) {
				return Sidecar{}, fmt.Errorf("%w: malformed capture attempt record", ErrInvalid)
			}
			sidecar.CaptureDigest = raw.CaptureDigest
			sidecar.CaptureStatus = raw.CaptureStatus
			terminalSeen = true
		default:
			return Sidecar{}, fmt.Errorf("%w: unknown record kind %q", ErrInvalid, raw.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		return Sidecar{}, fmt.Errorf("%w: scan records: %v", ErrInvalid, err)
	}
	if len(sidecar.ClockSamples) < 3 || len(sidecar.CommandBuffers) == 0 || !terminalSeen {
		return Sidecar{}, fmt.Errorf("%w: need clock samples, command buffers, and final capture identity", ErrInvalid)
	}
	for i := 1; i < len(sidecar.ClockSamples); i++ {
		previous, current := sidecar.ClockSamples[i-1], sidecar.ClockSamples[i]
		if current.CPUTimeNS <= previous.CPUTimeNS || current.GPUTimeNS <= previous.GPUTimeNS {
			return Sidecar{}, fmt.Errorf("%w: clock samples are not strictly ordered", ErrInvalid)
		}
	}
	first := sidecar.ClockSamples[0].GPUTimeNS
	last := sidecar.ClockSamples[len(sidecar.ClockSamples)-1].GPUTimeNS
	for _, command := range sidecar.CommandBuffers {
		if command.GPUStartNS < first || command.GPUEndNS > last {
			return Sidecar{}, fmt.Errorf("%w: command buffer %d is outside sampled GPU clock range", ErrInvalid, command.ID)
		}
	}
	return sidecar, nil
}

func validateRun(sidecar *Sidecar, runID string) error {
	if runID == "" || runID != strings.TrimSpace(runID) || len(runID) > 256 {
		return fmt.Errorf("%w: malformed run ID", ErrInvalid)
	}
	if sidecar.RunID == "" {
		sidecar.RunID = runID
	} else if sidecar.RunID != runID {
		return fmt.Errorf("%w: mixed run IDs", ErrInvalid)
	}
	return nil
}

func hasCommandFields(raw record) bool {
	return raw.ID != nil || raw.CaptureLabel != "" || raw.FinalLabel != "" ||
		raw.GPUStartSeconds != nil || raw.GPUEndSeconds != nil ||
		raw.KernelStartSeconds != nil || raw.KernelEndSeconds != nil || raw.Status != nil || raw.TraceDigest != "" ||
		raw.CaptureDigest != "" || raw.CaptureStatus != "" || raw.Reason != ""
}

func hasNonArtifactFields(raw record) bool {
	return raw.ID != nil || raw.CaptureLabel != "" || raw.FinalLabel != "" ||
		raw.GPUStartSeconds != nil || raw.GPUEndSeconds != nil ||
		raw.KernelStartSeconds != nil || raw.KernelEndSeconds != nil || raw.Status != nil
}

func decodeCommand(raw record) (CommandBuffer, error) {
	if raw.CPUTicks != nil || raw.GPUTicks != nil || raw.TraceDigest != "" || raw.ID == nil || *raw.ID == 0 ||
		raw.GPUStartSeconds == nil || raw.GPUEndSeconds == nil ||
		raw.KernelStartSeconds == nil || raw.KernelEndSeconds == nil || raw.Status == nil ||
		!validLabel(raw.CaptureLabel) || !validLabel(raw.FinalLabel) {
		return CommandBuffer{}, fmt.Errorf("%w: malformed command buffer", ErrInvalid)
	}
	start, ok := secondsToNS(*raw.GPUStartSeconds)
	if !ok {
		return CommandBuffer{}, fmt.Errorf("%w: malformed GPU start time", ErrInvalid)
	}
	end, ok := secondsToNS(*raw.GPUEndSeconds)
	if !ok || end < start {
		return CommandBuffer{}, fmt.Errorf("%w: malformed GPU end time", ErrInvalid)
	}
	kernelStart, ok := secondsToNS(*raw.KernelStartSeconds)
	if !ok {
		return CommandBuffer{}, fmt.Errorf("%w: malformed kernel start time", ErrInvalid)
	}
	kernelEnd, ok := secondsToNS(*raw.KernelEndSeconds)
	if !ok || kernelEnd < kernelStart {
		return CommandBuffer{}, fmt.Errorf("%w: malformed kernel end time", ErrInvalid)
	}
	if *raw.Status != 4 {
		return CommandBuffer{}, fmt.Errorf("%w: command buffer did not complete", ErrInvalid)
	}
	return CommandBuffer{
		ID: *raw.ID, CaptureLabel: raw.CaptureLabel, FinalLabel: raw.FinalLabel,
		GPUStartNS: start, GPUEndNS: end, KernelStartNS: kernelStart,
		KernelEndNS: kernelEnd, Status: *raw.Status,
	}, nil
}

func secondsToNS(seconds float64) (int64, bool) {
	value := seconds * 1e9
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= float64(math.MaxInt64) {
		return 0, false
	}
	return int64(math.Round(value)), true
}

func validLabel(label string) bool {
	return label != "" && label == strings.TrimSpace(label) && len(label) <= 4096
}

func validDigest(digest string) bool {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") || digest == "sha256:"+strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil && digest == strings.ToLower(digest)
}
