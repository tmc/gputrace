package trace

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// KDebugEvent represents a kernel debug trace event.
// These events provide low-level GPU timing information.
type KDebugEvent struct {
	Timestamp uint64    // Mach absolute time
	ThreadID  uint64    // Thread that triggered the event
	DebugID   uint32    // Debug code (class + subclass + code)
	CPUNum    uint32    // CPU number
	Args      [4]uint64 // Event-specific arguments
}

// GPU-related kdebug codes (class 0x85)
const (
	// DBG_MACH_IPC = 0x04
	// DBG_MACH_VM = 0x02
	// DBG_MACH_SCHED = 0x01
	// DBG_GPU = 0x85 (GPU-related events)

	KDebugClassGPU = 0x85 // GPU event class

	// GPU subclasses
	KDebugGPUSubmission     = 0x3  // GPU command submission
	KDebugGPUExecutionStart = 0x90 // GPU execution start
	KDebugGPUExecutionEnd   = 0xA9 // GPU execution end
)

// KDebugParser parses kernel debug events from trace files.
type KDebugParser struct {
	trace *Trace
}

// NewKDebugParser creates a new kdebug parser.
func NewKDebugParser(trace *Trace) *KDebugParser {
	return &KDebugParser{trace: trace}
}

// ParseKDebugEvents extracts kdebug events from the trace.
// These are typically stored in auxiliary trace files, not the main .gputrace bundle.
func (p *KDebugParser) ParseKDebugEvents() ([]*KDebugEvent, error) {
	// Try to find kdebug trace file
	// Common locations:
	// - trace.gputrace/kdebug.raw
	// - trace.trace (companion .trace file)
	// - trace.gputrace/.gpuprofiler_raw/kdebug_*.raw

	kdebugPaths := []string{
		filepath.Join(p.trace.Path, "kdebug.raw"),
		filepath.Join(p.trace.Path, ".gpuprofiler_raw", "kdebug.raw"),
	}

	// Also try .trace companion file
	traceBase := p.trace.Path
	if filepath.Ext(traceBase) == ".gputrace" {
		traceBase = traceBase[:len(traceBase)-9] // Remove .gputrace
	}
	kdebugPaths = append(kdebugPaths, traceBase+".trace")

	var events []*KDebugEvent
	var lastErr error

	for _, path := range kdebugPaths {
		e, err := p.parseKDebugFile(path)
		if err == nil && len(e) > 0 {
			events = append(events, e...)
		} else if err != nil {
			lastErr = err
		}
	}

	if len(events) == 0 && lastErr != nil {
		return nil, fmt.Errorf("no kdebug events found: %w", lastErr)
	}

	return events, nil
}

// parseKDebugFile parses a kdebug trace file.
func (p *KDebugParser) parseKDebugFile(path string) ([]*KDebugEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []*KDebugEvent

	// kdebug events are typically 32 bytes each:
	// - 8 bytes: timestamp
	// - 8 bytes: thread ID
	// - 4 bytes: debug ID
	// - 4 bytes: CPU number
	// - 32 bytes: 4x8 byte arguments
	const eventSize = 8 + 8 + 4 + 4 + 32

	buf := make([]byte, eventSize)

	for {
		n, err := io.ReadFull(f, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return events, err
		}
		if n != eventSize {
			break
		}

		event := &KDebugEvent{
			Timestamp: binary.LittleEndian.Uint64(buf[0:8]),
			ThreadID:  binary.LittleEndian.Uint64(buf[8:16]),
			DebugID:   binary.LittleEndian.Uint32(buf[16:20]),
			CPUNum:    binary.LittleEndian.Uint32(buf[20:24]),
		}

		// Parse arguments
		for i := 0; i < 4; i++ {
			offset := 24 + (i * 8)
			event.Args[i] = binary.LittleEndian.Uint64(buf[offset : offset+8])
		}

		// Only keep GPU-related events
		if p.isGPUEvent(event) {
			events = append(events, event)
		}
	}

	return events, nil
}

// isGPUEvent checks if a kdebug event is GPU-related.
func (p *KDebugParser) isGPUEvent(event *KDebugEvent) bool {
	class := (event.DebugID >> 24) & 0xFF
	return class == KDebugClassGPU
}

// GetEventClass extracts the event class from debug ID.
func (event *KDebugEvent) GetEventClass() uint8 {
	return uint8((event.DebugID >> 24) & 0xFF)
}

// GetEventSubclass extracts the event subclass from debug ID.
func (event *KDebugEvent) GetEventSubclass() uint8 {
	return uint8((event.DebugID >> 16) & 0xFF)
}

// IsGPUSubmission checks if this is a GPU command submission event.
func (event *KDebugEvent) IsGPUSubmission() bool {
	return event.GetEventClass() == KDebugClassGPU &&
		event.GetEventSubclass() == KDebugGPUSubmission
}

// IsGPUExecutionStart checks if this is a GPU execution start event.
func (event *KDebugEvent) IsGPUExecutionStart() bool {
	return event.GetEventClass() == KDebugClassGPU &&
		event.GetEventSubclass() == KDebugGPUExecutionStart
}

// IsGPUExecutionEnd checks if this is a GPU execution end event.
func (event *KDebugEvent) IsGPUExecutionEnd() bool {
	return event.GetEventClass() == KDebugClassGPU &&
		event.GetEventSubclass() == KDebugGPUExecutionEnd
}

// GPUExecutionInterval represents a GPU execution interval from kdebug events.
type GPUExecutionInterval struct {
	SubmissionEvent *KDebugEvent
	StartEvent      *KDebugEvent
	EndEvent        *KDebugEvent
	CommandBufferID uint64
	EncoderID       uint64
}

type gpuExecutionKey struct {
	commandBufferID uint64
	encoderID       uint64
}

// Duration returns the GPU execution duration in nanoseconds.
func (interval *GPUExecutionInterval) Duration() uint64 {
	if interval.StartEvent == nil || interval.EndEvent == nil {
		return 0
	}
	return interval.EndEvent.Timestamp - interval.StartEvent.Timestamp
}

// CorrelateGPUExecution correlates submission, start, and end events into intervals.
func CorrelateGPUExecution(events []*KDebugEvent) []*GPUExecutionInterval {
	// Build maps for quick lookup
	submissions := make(map[uint64]*KDebugEvent)     // command buffer ID -> submission event
	starts := make(map[gpuExecutionKey]*KDebugEvent) // command buffer and encoder -> start event
	ends := make(map[gpuExecutionKey]*KDebugEvent)   // command buffer and encoder -> end event

	// First pass: categorize events
	for _, event := range events {
		// Command buffer ID is typically in Args[0]
		cbID := event.Args[0]
		key := gpuExecutionKey{
			commandBufferID: cbID,
			encoderID:       event.Args[1],
		}

		if event.IsGPUSubmission() {
			submissions[cbID] = event
		} else if event.IsGPUExecutionStart() {
			starts[key] = event
		} else if event.IsGPUExecutionEnd() {
			ends[key] = event
		}
	}

	// Second pass: build intervals
	var intervals []*GPUExecutionInterval

	// Match end events with starts and submissions
	for key, endEvent := range ends {
		interval := &GPUExecutionInterval{
			CommandBufferID: key.commandBufferID,
			EncoderID:       key.encoderID,
			EndEvent:        endEvent,
		}

		if startEvent, ok := starts[key]; ok {
			interval.StartEvent = startEvent
		}

		if submissionEvent, ok := submissions[key.commandBufferID]; ok {
			interval.SubmissionEvent = submissionEvent
		}

		intervals = append(intervals, interval)
	}

	sort.Slice(intervals, func(i, j int) bool {
		iTs := intervalTimestamp(intervals[i])
		jTs := intervalTimestamp(intervals[j])
		if iTs != jTs {
			return iTs < jTs
		}
		if intervals[i].CommandBufferID != intervals[j].CommandBufferID {
			return intervals[i].CommandBufferID < intervals[j].CommandBufferID
		}
		return intervals[i].EncoderID < intervals[j].EncoderID
	})

	return intervals
}

func intervalTimestamp(interval *GPUExecutionInterval) uint64 {
	if interval.StartEvent != nil {
		return interval.StartEvent.Timestamp
	}
	if interval.EndEvent != nil {
		return interval.EndEvent.Timestamp
	}
	return 0
}
