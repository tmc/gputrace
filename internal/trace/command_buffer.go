package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CommandBuffer represents a Metal command buffer captured in the trace.
type CommandBuffer struct {
	// Index in the trace (0-based)
	Index int

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
	capturePath := filepath.Join(t.Path, "capture")

	data, err := os.ReadFile(capturePath)
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

	return commandBuffers, nil
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
// Each kernel name corresponds to a compute encoder in the trace.
func (t *Trace) ParseComputeEncoders() ([]*ComputeEncoder, error) {
	var encoders []*ComputeEncoder

	// Use kernel names from trace metadata as encoder identifiers
	for i, name := range t.KernelNames {
		encoder := &ComputeEncoder{
			Index: i,
			Label: name,
		}
		encoders = append(encoders, encoder)
	}

	return encoders, nil
}

// CountComputeEncoders returns the number of compute encoders in the trace.
func (t *Trace) CountComputeEncoders() (int, error) {
	encoders, err := t.ParseComputeEncoders()
	if err != nil {
		return 0, err
	}
	return len(encoders), nil
}

// ParseDispatchCalls extracts all compute kernel dispatch calls from the trace.
func (t *Trace) ParseDispatchCalls() ([]*DispatchCall, error) {
	capturePath := filepath.Join(t.Path, "capture")

	data, err := os.ReadFile(capturePath)
	if err != nil {
		return nil, fmt.Errorf("read capture file: %w", err)
	}

	var dispatches []*DispatchCall
	offset := int64(0)
	index := 0

	// Look for dispatch markers - these are "Ct" records with type 01
	// Structure: "Ct" + type (1 byte) + data
	marker := []byte("Ct")

	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}

		absolutePos := offset + int64(pos)

		// Check if this is a dispatch record (type 01)
		if absolutePos+3 <= int64(len(data)) && data[absolutePos+2] == 0x01 {
			dispatch := &DispatchCall{
				Index:  index,
				Offset: absolutePos,
			}
			dispatches = append(dispatches, dispatch)
			index++
		}

		offset = absolutePos + 2
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
