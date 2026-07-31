package command

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/gputrace/internal/trace"
)

// DetailedCommandBuffer represents a parsed command buffer with all API calls.
type DetailedCommandBuffer struct {
	*CommandBuffer

	// QueueAddress is the address of the command queue
	QueueAddress uint64

	// API calls within this command buffer.
	Calls []APICall

	// Encoders within this command buffer, when explicit encoder records exist.
	Encoders []*ComputeEncoder

	// Dispatches within this command buffer.
	Dispatches []DispatchThreads
}

// APICall represents a parsed Metal command record.
type APICall struct {
	// RecordType is the MTSP record type, such as Ct.
	RecordType string

	// CommandFlags are the record flags.
	CommandFlags uint32

	// ObjectAddr is the primary object address. For Ct records this is the
	// pipeline state address.
	ObjectAddr uint64

	// TargetAddr is retained for older callers. For Ct records this is the low
	// 32 bits of FunctionAddr.
	TargetAddr uint32

	// PipelineAddr is the Metal pipeline state address.
	PipelineAddr uint64

	// FunctionAddr is the Metal function address associated with PipelineAddr.
	FunctionAddr uint64

	// BindingCount is the number of resource bindings listed in the Ct record.
	BindingCount uint32

	// BufferBindings are the resource binding addresses from the Ct record.
	BufferBindings []uint64

	// Offset in capture file
	Offset int64
}

// DispatchThreads represents a dispatchThreads or dispatchThreadgroups call.
type DispatchThreads = trace.DispatchThreads

// ParseDetailedCommandBuffer extracts all API calls from a specific command buffer.
//
// It reads and rescans the whole capture file on every call. Callers walking
// every command buffer should use ParseDetailedCommandBuffers instead, which
// does that work once.
func ParseDetailedCommandBuffer(t *trace.Trace, cbIndex int) (*DetailedCommandBuffer, error) {
	data, commandBuffers, err := loadCapture(t)
	if err != nil {
		return nil, err
	}
	return parseDetailedCommandBuffer(t, data, commandBuffers, cbIndex)
}

// Capture holds the capture file and its command-buffer index so a caller
// walking every command buffer reads and scans the file once instead of once
// per buffer. Detailed reports the same value and error as
// ParseDetailedCommandBuffer, so callers keep their per-buffer error handling.
type Capture struct {
	trace          *trace.Trace
	data           []byte
	commandBuffers []*trace.CommandBuffer
}

// OpenCapture reads the capture file and parses the command-buffer index.
func OpenCapture(t *trace.Trace) (*Capture, error) {
	data, commandBuffers, err := loadCapture(t)
	if err != nil {
		return nil, err
	}
	return &Capture{trace: t, data: data, commandBuffers: commandBuffers}, nil
}

// CommandBuffers returns the parsed command-buffer index.
func (c *Capture) CommandBuffers() []*trace.CommandBuffer { return c.commandBuffers }

// Detailed extracts all API calls from the command buffer at cbIndex.
func (c *Capture) Detailed(cbIndex int) (*DetailedCommandBuffer, error) {
	return parseDetailedCommandBuffer(c.trace, c.data, c.commandBuffers, cbIndex)
}

// loadCapture reads the capture file and the command-buffer index together,
// since every detailed parse needs both.
func loadCapture(t *trace.Trace) ([]byte, []*trace.CommandBuffer, error) {
	data, err := os.ReadFile(filepath.Join(t.Path, "capture"))
	if err != nil {
		return nil, nil, fmt.Errorf("read capture file: %w", err)
	}
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return nil, nil, err
	}
	return data, commandBuffers, nil
}

func parseDetailedCommandBuffer(t *trace.Trace, data []byte, commandBuffers []*trace.CommandBuffer, cbIndex int) (*DetailedCommandBuffer, error) {
	if cbIndex < 0 || cbIndex >= len(commandBuffers) {
		return nil, fmt.Errorf("invalid command buffer index: %d (have %d)", cbIndex, len(commandBuffers))
	}

	cb := commandBuffers[cbIndex]

	// Determine the region of this command buffer
	var cbStart, cbEnd int64
	cbStart = cb.Offset

	if cbIndex+1 < len(commandBuffers) {
		cbEnd = commandBuffers[cbIndex+1].Offset
	} else {
		cbEnd = int64(len(data))
	}

	cbData := data[cbStart:cbEnd]

	// Parse Queue Address from C records
	// First C record after CUUU has queue address at +0x04
	cMarker := []byte("C\x00\x00\x00")
	var queueAddr uint64

	// Scan first few records in the buffer
	offset := 0
	pos := bytes.Index(cbData[offset:], cMarker)
	if pos != -1 {
		absolutePos := offset + pos
		if absolutePos+12 <= len(cbData) {
			queueAddr = binary.LittleEndian.Uint64(cbData[absolutePos+4 : absolutePos+12])
		}
	}

	// Parse API calls (Ct records)
	calls, err := parseAPICallsInRegion(cbData, cbStart)
	if err != nil {
		return nil, fmt.Errorf("parse API calls: %w", err)
	}

	// Parse encoders in this command buffer
	encoders, err := parseEncodersInRegion(cbData, cbStart)
	if err != nil {
		return nil, fmt.Errorf("parse encoders: %w", err)
	}

	dispatches := t.ParseDispatchInRegion(cbData, cbStart)

	return &DetailedCommandBuffer{
		CommandBuffer: cb,
		QueueAddress:  queueAddr,
		Calls:         calls,
		Encoders:      encoders,
		Dispatches:    dispatches,
	}, nil
}

func parseAPICallsInRegion(data []byte, baseOffset int64) ([]APICall, error) {
	var calls []APICall

	ctMarker := []byte("Ct\x00\x00")
	offset := 0
	for {
		pos := bytes.Index(data[offset:], ctMarker)
		if pos == -1 {
			break
		}
		markerOffset := offset + pos
		recordOffset := markerOffset - 0x24
		offset = markerOffset + len(ctMarker)

		if recordOffset < 0 || recordOffset+0x40 > len(data) {
			continue
		}
		recordSize := int(binary.LittleEndian.Uint32(data[recordOffset : recordOffset+4]))
		if recordSize < 0x40 || recordOffset+recordSize > len(data) {
			continue
		}

		rec := trace.MTSPRecord{
			Type:   trace.RecordTypeCt,
			Offset: recordOffset,
			Size:   recordSize,
			Data:   data[recordOffset : recordOffset+recordSize],
		}
		ct, err := rec.ParseCtRecord()
		if err != nil {
			return nil, err
		}
		calls = append(calls, APICall{
			RecordType:     rec.Type,
			CommandFlags:   ct.CommandFlags,
			ObjectAddr:     ct.PipelineAddr,
			TargetAddr:     uint32(ct.FunctionAddr),
			PipelineAddr:   ct.PipelineAddr,
			FunctionAddr:   ct.FunctionAddr,
			BindingCount:   ct.BindingCount,
			BufferBindings: append([]uint64(nil), ct.BufferBindings...),
			Offset:         baseOffset + int64(rec.Offset),
		})
	}

	return calls, nil
}

func parseEncodersInRegion(data []byte, baseOffset int64) ([]*ComputeEncoder, error) {
	var encoders []*ComputeEncoder

	// CS record structure:
	// +0x00: size (4 bytes) - typically 0x08
	// +0x04: "CS" magic (2 bytes) + padding (2 bytes)
	// +0x08: address (8 bytes)
	// +0x10: label string (null-terminated)

	for i := 0; i < len(data)-20; i++ {
		// Look for CS record marker
		if data[i] == 0x43 && data[i+1] == 0x53 {
			// Extract address (8 bytes after CS marker)
			addressStart := i + 4
			if addressStart+8 > len(data) {
				continue
			}
			address := binary.LittleEndian.Uint64(data[addressStart : addressStart+8])

			// Extract label (starts 12 bytes after CS marker)
			labelStart := i + 12
			if labelStart >= len(data) {
				continue
			}

			// Find null terminator for label
			labelEnd := labelStart
			for labelEnd < len(data) && data[labelEnd] != 0 && labelEnd-labelStart < 128 {
				labelEnd++
			}

			label := ""
			if labelEnd > labelStart {
				labelBytes := data[labelStart:labelEnd]
				// Check if it looks like a valid label (printable characters)
				if isPrintableBytes(labelBytes) {
					label = string(labelBytes)
				}
			}

			encoders = append(encoders, &ComputeEncoder{
				Index:   len(encoders),
				Address: address,
				Label:   label,
				Offset:  baseOffset + int64(i),
			})
		}
	}

	return encoders, nil
}

// ParseDispatchInRegion parses dispatch calls within a command buffer region.

// DumpCommandBuffer writes a detailed command buffer dump similar to Instruments output.
func DumpCommandBuffer(t *trace.Trace, w io.Writer, cbIndex int) error {
	dcb, err := ParseDetailedCommandBuffer(t, cbIndex)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n=== Command Buffer #%d ===\n", cbIndex)
	fmt.Fprintf(w, "UUID: %s\n", dcb.UUID)
	fmt.Fprintf(w, "Timestamp: %d\n", dcb.Timestamp)
	if len(dcb.Encoders) > 0 {
		fmt.Fprintf(w, "Address: 0x%x\n", dcb.Encoders[0].Address) // CB address from first encoder
	}
	fmt.Fprintf(w, "\n")

	// Format API calls
	callIdx := 524 // Start numbering like the example (adjust as needed)
	pipelineMap := t.BuildPipelineFunctionMap()

	for _, encoder := range dcb.Encoders {
		fmt.Fprintf(w, "#%d 0x%x = computeCommandEncoder\n", callIdx, encoder.Address)
		callIdx++
	}

	// Print calls grouped by encoder
	for _, call := range dcb.Calls {
		switch call.RecordType {
		case trace.RecordTypeCt:
			name := pipelineMap[call.PipelineAddr]
			if name != "" {
				fmt.Fprintf(w, "#%d [setComputePipelineState:0x%x (%s) function:0x%x bindings:%d flags:0x%x]\n",
					callIdx, call.PipelineAddr, name, call.FunctionAddr, call.BindingCount, call.CommandFlags)
			} else {
				fmt.Fprintf(w, "#%d [setComputePipelineState:0x%x function:0x%x bindings:%d flags:0x%x]\n",
					callIdx, call.PipelineAddr, call.FunctionAddr, call.BindingCount, call.CommandFlags)
			}
			if len(call.BufferBindings) > 0 {
				fmt.Fprintf(w, "    Bindings: %x\n", call.BufferBindings)
			}
		default:
			fmt.Fprintf(w, "#%d [%s record: 0x%x -> 0x%x]\n", callIdx, call.RecordType, call.ObjectAddr, call.TargetAddr)
		}
		callIdx++
	}

	// Print dispatch calls
	for i, dispatch := range dcb.Dispatches {
		fmt.Fprintf(w, "    Dispatch #%d: threads:{%d, %d, %d} threadsPerGroup:{%d, %d, %d}\n",
			i+1,
			dispatch.ThreadsX, dispatch.ThreadsY, dispatch.ThreadsZ,
			dispatch.ThreadsPerGroupX, dispatch.ThreadsPerGroupY, dispatch.ThreadsPerGroupZ)
	}

	fmt.Fprintf(w, "#%d [commit]\n", callIdx)

	return nil
}

// isPrintableBytes checks if a byte slice contains only printable ASCII characters.
func isPrintableBytes(b []byte) bool {
	for _, c := range b {
		if c < 32 || c > 126 {
			return false
		}
	}
	return len(b) > 0
}
