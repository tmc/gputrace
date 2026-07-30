package trace

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/gputrace/internal/profilerraw"
)

// CommandBuffer represents a Metal command buffer captured in the trace.
type CommandBuffer struct {
	// Index in the trace (0-based)
	Index int

	// Label is the command buffer label, when present in the capture.
	Label string

	// Timestamp when the command buffer was committed
	Timestamp uint64

	// UUID uniquely identifying this command buffer
	UUID string

	// Offset in the capture file where this CUUU record appears
	Offset int64
}

// ComputeEncoder represents a Metal compute command encoder in the trace.
type ComputeEncoder struct {
	// Index in the trace (0-based)
	Index int

	// Address/ID of the encoder
	Address uint64

	// Label/name of the encoder (from CS record)
	Label string

	// Offset in the capture file where this CS record appears
	Offset int64
}

// DispatchCall represents a compute kernel dispatch call in the trace.
type DispatchCall struct {
	// Index in the trace (0-based)
	Index int

	// Offset in the capture file where this dispatch marker appears
	Offset int64
}

// XDICIndex represents the parsed xdic index file.
type XDICIndex struct {
	DeviceAddress string
	Offset        int64
}

// ParseCommandBuffers extracts all command buffers from the trace by finding CUUU markers.
// CUUU markers indicate Metal Command buffer records.
func (t *Trace) ParseCommandBuffers() ([]*CommandBuffer, error) {
	data, err := t.readCaptureFile()
	if err != nil {
		return nil, fmt.Errorf("read capture file: %w", err)
	}

	var commandBuffers []*CommandBuffer
	offset := int64(0)
	index := 0

	// Search for "CUUU" markers in the file
	marker := []byte("CUUU")

	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}

		absolutePos := offset + int64(pos)

		// Read timestamp (8 bytes after CUUU marker)
		if absolutePos+12 <= int64(len(data)) {
			timestamp := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])

			cb := &CommandBuffer{
				Index:     index,
				Timestamp: timestamp,
				Offset:    absolutePos,
			}
			commandBuffers = append(commandBuffers, cb)
			index++
		}

		offset = absolutePos + 4
	}

	for i, cb := range commandBuffers {
		end := int64(len(data))
		if i+1 < len(commandBuffers) {
			end = commandBuffers[i+1].Offset
		}
		cb.Label = firstCSLabel(data[cb.Offset:end])
	}

	return commandBuffers, nil
}

func firstCSLabel(data []byte) string {
	csMarker := []byte("CS\x00\x00")
	pos := bytes.Index(data, csMarker)
	if pos == -1 || pos+12 > len(data) {
		return ""
	}

	labelStart := pos + 12
	labelEnd := labelStart
	for labelEnd < len(data) && data[labelEnd] != 0 && labelEnd-labelStart < 128 {
		labelEnd++
	}
	if labelEnd == labelStart {
		return ""
	}

	label := data[labelStart:labelEnd]
	for _, b := range label {
		if b < 32 || b > 126 {
			return ""
		}
	}
	return string(label)
}

// ParseIndex parses the xdic index file to get device resources mapping.
func (t *Trace) ParseIndex() (*XDICIndex, error) {
	indexPath := filepath.Join(t.Path, "index")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read index file: %w", err)
	}

	if len(data) < 4 || string(data[:4]) != "xdic" {
		return nil, fmt.Errorf("invalid index file: missing xdic magic")
	}

	// Parse device address and offset
	// Format is somewhat documented in the trace format docs
	index := &XDICIndex{}

	// Look for device address pattern
	// This is a simplified parser - real format may be more complex
	if len(data) >= 20 {
		index.Offset = int64(binary.LittleEndian.Uint64(data[12:20]))
	}

	return index, nil
}

// CountCommandBuffers returns the number of command buffers in the trace.
func (t *Trace) CountCommandBuffers() (int, error) {
	cbs, err := t.ParseCommandBuffers()
	if err != nil {
		return 0, err
	}
	return len(cbs), nil
}

// ParseComputeEncoders extracts all compute command encoders from the trace.
// Scans the capture file and device-resources for CS (Command Submission) records.
func (t *Trace) ParseComputeEncoders() ([]*ComputeEncoder, error) {
	var encoders []*ComputeEncoder

	// Helper to scan a data slice for CS records
	scanCS := func(data []byte) {
		csMarker := []byte("CS\x00\x00")
		offset := 0

		for {
			pos := bytes.Index(data[offset:], csMarker)
			if pos == -1 {
				break
			}
			absolutePos := offset + pos

			if absolutePos+12 <= len(data) {
				// Read CS address (encoder address)
				addr := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])

				// Read label
				labelStart := absolutePos + 12
				labelEnd := labelStart
				for labelEnd < len(data) && data[labelEnd] != 0 {
					labelEnd++
				}
				label := string(data[labelStart:labelEnd])

				// Accept all CS records as potential encoders
				// This includes "Multiply" type labels which act as both encoder and kernel name proxy
				if len(label) > 0 {
					encoder := &ComputeEncoder{
						Index:   len(encoders),
						Address: addr,
						Label:   label,
						Offset:  int64(absolutePos),
					}
					encoders = append(encoders, encoder)
				}
			}
			offset = absolutePos + 4
		}
	}

	// Scan capture data
	if len(t.CaptureData) > 0 {
		scanCS(t.CaptureData)
	}

	// If no encoders found in capture, also scan device-resources
	// Some trace formats store CS records only in device-resources
	if len(encoders) == 0 {
		for _, data := range t.DeviceResources {
			scanCS(data)
		}
	}

	return encoders, nil
}

// isActualFunctionName returns true if the name looks like an actual kernel function
// rather than an encoder label or command buffer label.
// Actual function names typically have underscores (e.g., "simple_add", "matmul_kernel")
// and start with lowercase letters.
// Encoder labels like "Encoder_5_complex_math" or "MultipleEncoders_6" are filtered out.
func isActualFunctionName(name string) bool {
	if len(name) == 0 {
		return false
	}

	// Must have at least one underscore
	if !strings.ContainsRune(name, '_') {
		return false
	}

	// Must start with lowercase letter (filters out "Encoder_X_..." and "MultipleEncoders_X")
	firstChar := name[0]
	if firstChar < 'a' || firstChar > 'z' {
		return false
	}

	return true
}

// ComputeEncoderCount describes an authoritative compute-encoder count.
type ComputeEncoderCount struct {
	Count     int
	Available bool
	Source    string
}

const (
	// ComputeEncoderSourceStreamData identifies Xcode profiler encoder metadata.
	ComputeEncoderSourceStreamData = "profiler streamData encoderInfoData"
	// ComputeEncoderSourceUnavailable explains why raw capture records are not counted.
	ComputeEncoderSourceUnavailable = "unavailable: raw capture lacks command-buffer-scoped encoder lifecycle evidence"
)

// ErrComputeEncoderCountUnavailable reports that the trace lacks an
// authoritative command-buffer-scoped or profiler encoder count.
var ErrComputeEncoderCountUnavailable = errors.New("compute encoder count unavailable")

// InspectComputeEncoderCount returns the best authoritative compute-encoder count.
//
// Cuw records describe buffer writes or updates. Their addresses are not encoder
// identities and must not be counted as compute encoders. CS records similarly
// describe observed submissions, not encoder lifetimes. Raw counts remain
// unavailable until the capture parser can identify encoder creation and end
// events within command-buffer boundaries.
func (t *Trace) InspectComputeEncoderCount() ComputeEncoderCount {
	if n := t.countEncodersFromStreamData(); n > 0 {
		return ComputeEncoderCount{
			Count:     n,
			Available: true,
			Source:    ComputeEncoderSourceStreamData,
		}
	}
	return ComputeEncoderCount{Source: ComputeEncoderSourceUnavailable}
}

// CountComputeEncoders returns the authoritative compute-encoder count.
//
// It returns ErrComputeEncoderCountUnavailable when the trace has no
// authoritative source. Call InspectComputeEncoderCount for provenance.
func (t *Trace) CountComputeEncoders() (int, error) {
	count := t.InspectComputeEncoderCount()
	if !count.Available {
		return 0, ErrComputeEncoderCountUnavailable
	}
	return count.Count, nil
}

// countEncodersFromStreamData counts encoders from the streamData plist's encoderInfoData.
// This works for profiler-only and Python traces where MTSP records are insufficient.
func (t *Trace) countEncodersFromStreamData() int {
	profilerDir := t.findGPUProfilerDir()
	if profilerDir == "" {
		return 0
	}

	streamDataPath := filepath.Join(profilerDir, "streamData")
	data, err := os.ReadFile(streamDataPath)
	if err != nil {
		return 0
	}

	// Parse NSKeyedArchiver plist
	var plistData map[string]interface{}
	if _, err := plist.Unmarshal(data, &plistData); err != nil {
		return 0
	}

	objects, ok := plistData["$objects"].([]interface{})
	if !ok || len(objects) < 2 {
		return 0
	}

	// Find the root object and look for encoderInfoData
	for _, obj := range objects {
		m, ok := obj.(map[string]interface{})
		if !ok {
			continue
		}
		encoderInfoUID, ok := m["encoderInfoData"]
		if !ok {
			continue
		}

		encoderInfoSize := 40
		if size, ok := plistNumberToInt(m["encoderInfoSize"]); ok && size > 0 {
			encoderInfoSize = int(size)
		}

		// Resolve the UID to get the data
		var idx int
		switch v := encoderInfoUID.(type) {
		case plist.UID:
			idx = int(v)
		case uint64:
			idx = int(v)
		case int64:
			idx = int(v)
		case int:
			idx = v
		default:
			continue
		}

		if idx >= len(objects) {
			continue
		}

		dataObj, ok := objects[idx].(map[string]interface{})
		if !ok {
			continue
		}

		nsData, ok := dataObj["NS.data"].([]byte)
		if !ok || len(nsData) < encoderInfoSize {
			continue
		}

		return len(nsData) / encoderInfoSize
	}

	return 0
}

func plistNumberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// findGPUProfilerDir returns the path to the .gpuprofiler_raw directory, or empty string.
func (t *Trace) findGPUProfilerDir() string {
	return profilerraw.FindDir(t.Path)
}

// ParseDispatchCalls extracts all compute kernel dispatch calls from the trace.
func (t *Trace) ParseDispatchCalls() ([]*DispatchCall, error) {
	data, err := t.readCaptureFile()
	if err != nil {
		return nil, fmt.Errorf("read capture file: %w", err)
	}

	// Use ParseDispatchInRegion on the entire capture file
	dispatchThreads, err := t.ParseDispatchInRegion(data, 0)
	if err != nil {
		return nil, err
	}

	// Convert DispatchThreads to DispatchCall
	var dispatches []*DispatchCall
	for i, dt := range dispatchThreads {
		dispatches = append(dispatches, &DispatchCall{
			Index:  i,
			Offset: dt.Offset,
		})
	}

	return dispatches, nil
}

// CountDispatchCalls returns the number of dispatch calls in the trace.
func (t *Trace) CountDispatchCalls() (int, error) {
	dispatches, err := t.ParseDispatchCalls()
	if err != nil {
		return 0, err
	}
	return len(dispatches), nil
}

// FormatCommandBufferSummary writes a human-readable summary of command buffers.
func (t *Trace) FormatCommandBufferSummary(w io.Writer) error {
	cbs, err := t.ParseCommandBuffers()
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Command Buffers: %d\n", len(cbs))
	for _, cb := range cbs {
		fmt.Fprintf(w, "  CB %d: timestamp=%d offset=%d\n", cb.Index, cb.Timestamp, cb.Offset)
	}

	return nil
}

// DispatchThreads represents dispatch thread configuration.
type DispatchThreads struct {
	// Thread dimensions
	ThreadsX, ThreadsY, ThreadsZ uint64

	// Threads per threadgroup dimensions
	ThreadsPerGroupX, ThreadsPerGroupY, ThreadsPerGroupZ uint64

	// Offset in capture file
	Offset int64
}

// ParseDispatchInRegion parses dispatch calls within a command buffer region.
func (t *Trace) ParseDispatchInRegion(data []byte, baseOffset int64) ([]DispatchThreads, error) {
	var dispatches []DispatchThreads
	dispatchMarker := []byte("ul@3")

	offset := 0
	for {
		pos := bytes.Index(data[offset:], dispatchMarker)
		if pos == -1 {
			break
		}

		absolutePos := offset + pos

		// Dispatch structure (discovered by reverse engineering):
		// +0x00: "ul@3" marker (4 bytes)
		// +0x04: variable data
		// +0x11: threadsX (uint64, 8 bytes)
		// +0x19: threadsY (uint64, 8 bytes)
		// +0x21: threadsZ (uint64, 8 bytes)
		// +0x29: threadsPerGroupX (uint64, 8 bytes)
		// +0x31: threadsPerGroupY (uint64, 8 bytes)
		// +0x39: threadsPerGroupZ (uint64, 8 bytes)

		if absolutePos+0x41 <= len(data) {
			threadsX := binary.LittleEndian.Uint64(data[absolutePos+0x11 : absolutePos+0x19])
			threadsY := binary.LittleEndian.Uint64(data[absolutePos+0x19 : absolutePos+0x21])
			threadsZ := binary.LittleEndian.Uint64(data[absolutePos+0x21 : absolutePos+0x29])

			threadsPerGroupX := binary.LittleEndian.Uint64(data[absolutePos+0x29 : absolutePos+0x31])
			threadsPerGroupY := binary.LittleEndian.Uint64(data[absolutePos+0x31 : absolutePos+0x39])
			threadsPerGroupZ := binary.LittleEndian.Uint64(data[absolutePos+0x39 : absolutePos+0x41])

			dispatches = append(dispatches, DispatchThreads{
				ThreadsX:         threadsX,
				ThreadsY:         threadsY,
				ThreadsZ:         threadsZ,
				ThreadsPerGroupX: threadsPerGroupX,
				ThreadsPerGroupY: threadsPerGroupY,
				ThreadsPerGroupZ: threadsPerGroupZ,
				Offset:           baseOffset + int64(absolutePos),
			})
		}

		offset += pos + 4
	}

	return dispatches, nil
}
