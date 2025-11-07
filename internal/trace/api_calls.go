package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// InitCall represents an initialization API call before the first command buffer.
type InitCall struct {
	// CallNumber is the global call index
	CallNumber int

	// Type of initialization (newBuffer, newLibrary, newFunction, newPipelineState)
	Type string

	// Address of the created object
	Address uint64

	// Additional info (e.g., function name, buffer length)
	Info string

	// Label for this object (if available from CS records)
	Label string

	// Offset in capture file
	Offset int64
}

// FormattedAPICall represents a complete API call with all details.
type FormattedAPICall struct {
	// CallNumber is the global call index
	CallNumber int

	// Indented indicates if this call should be indented (encoder calls)
	Indented bool

	// Type of call
	Type string

	// Address of the object (if applicable)
	Address uint64

	// Details of the call (parameters, etc.)
	Details string

	// Label for this object (if available from CS records)
	Label string

	// Offset in capture file
	Offset int64
}

// APICallList represents a complete list of API calls for a trace.
type APICallList struct {
	// InitCalls are the initialization calls before first command buffer
	InitCalls []InitCall

	// CommandBuffers contains all command buffer API calls
	CommandBuffers []CommandBufferCalls
}

// CommandBufferCalls represents all API calls for a single command buffer.
type CommandBufferCalls struct {
	// Index of the command buffer
	Index int

	// Address of the command buffer
	Address uint64

	// CallNumber is the global call index for this CB creation
	CallNumber int

	// Label for this command buffer (if available from CS records)
	Label string

	// Calls within this command buffer (encoders, setBuffer, dispatch, etc.)
	Calls []FormattedAPICall
}

// ParseAPICallList extracts all API calls from the trace.
func (t *Trace) ParseAPICallList() (*APICallList, error) {
	capturePath := filepath.Join(t.Path, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		return nil, fmt.Errorf("read capture: %w", err)
	}

	list := &APICallList{}
	callNum := 0

	// Find first CUUU marker
	cuuuMarker := []byte("CUUU")
	firstCUUU := bytes.Index(data, cuuuMarker)
	if firstCUUU == -1 {
		return nil, fmt.Errorf("no command buffers found")
	}

	// Parse labels from CS records in init section
	labelMap := parseCSRecordsFromInit(data[:firstCUUU])

	// Parse initialization calls before first CUUU
	initCalls, nextCallNum, err := parseInitCalls(data[:firstCUUU], callNum)
	if err != nil {
		return nil, fmt.Errorf("parse init calls: %w", err)
	}

	// Apply labels to init calls
	for i := range initCalls {
		if label, exists := labelMap[initCalls[i].Address]; exists {
			initCalls[i].Label = label
		}
	}

	list.InitCalls = initCalls
	callNum = nextCallNum

	// Parse all command buffers
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return nil, fmt.Errorf("parse command buffers: %w", err)
	}

	for i, cb := range commandBuffers {
		// Determine command buffer region
		var cbEnd int64
		if i+1 < len(commandBuffers) {
			cbEnd = commandBuffers[i+1].Offset
		} else {
			cbEnd = int64(len(data))
		}

		cbData := data[cb.Offset:cbEnd]

		// Parse labels from CS records in this command buffer's section
		cbLabelMap := parseCSRecordsFromInit(cbData)

		// Parse this command buffer's calls
		cbCalls, nextCallNum, err := parseCommandBufferCalls(cbData, cb, callNum)
		if err != nil {
			return nil, fmt.Errorf("parse CB %d: %w", i, err)
		}

		// Apply command buffer label
		if label, exists := labelMap[cbCalls.Address]; exists {
			cbCalls.Label = label
		}

		// Apply labels to encoder calls within this command buffer
		for j := range cbCalls.Calls {
			if label, exists := cbLabelMap[cbCalls.Calls[j].Address]; exists {
				cbCalls.Calls[j].Label = label
			}
		}

		list.CommandBuffers = append(list.CommandBuffers, *cbCalls)
		callNum = nextCallNum
	}

	return list, nil
}

// parseInitCalls parses initialization calls before the first command buffer.
func parseInitCalls(data []byte, startCallNum int) ([]InitCall, int, error) {
	var calls []InitCall
	callNum := startCallNum

	// Pattern: "C\x00\x00\x00" records with various types
	// We'll parse different record types and sort them by offset

	// Find CUt records (residency set creation)
	// Structure: "CUt\x00" + residency set address
	cutMarker := []byte("CUt\x00")
	offset := 0
	for {
		pos := bytes.Index(data[offset:], cutMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read residency set address at +0x04
		if absolutePos+0x0c <= len(data) {
			resAddr := binary.LittleEndian.Uint64(data[absolutePos+0x04 : absolutePos+0x0c])
			if resAddr != 0 {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "newResidencySet",
					Address:    resAddr,
					Info:       "[Device newResidencySetWithDescriptor:<data> error:nil]",
					Offset:     int64(absolutePos),
				})
			}
		}

		offset += pos + 4
	}

	// TODO: Add requestResidency parsing
	// Find "Ct\x00\x00" records followed by residency set addresses to detect requestResidency calls
	// This requires more analysis of the binary format

	// Find CU\x00\x00 records (heap creation)
	// Structure:
	// +0x00: size field (4 bytes)
	// +0x04: "CU\x00\x00" marker (4 bytes)
	// +0x08: device address (8 bytes)
	// +0x10: UUID (16 bytes)
	// +0x20: size marker (4 bytes)
	// +0x24: heap address (8 bytes)
	cuMarker := []byte("CU\x00\x00")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], cuMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read heap address at +0x24 (relative to "CU" marker position)
		if absolutePos+0x2c <= len(data) {
			heapAddr := binary.LittleEndian.Uint64(data[absolutePos+0x24 : absolutePos+0x2c])
			if heapAddr != 0 {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "newHeap",
					Address:    heapAddr,
					Info:       "[Device newHeapWithDescriptor:<data>]",
					Offset:     int64(absolutePos),
				})
			}
		}

		offset += pos + 4
	}

	// Find Culul records (buffer creation from heap)
	// Structure:
	// +0x00: "Culul\x00\x00\x00"
	// +0x08: heap/device address (8 bytes)
	// +0x24: buffer address (8 bytes)
	// +0x10: buffer length (8 bytes)
	cululMarker := []byte("Culul")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], cululMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read heap address at +0x08 and buffer address at +0x24
		if absolutePos+0x2c <= len(data) {
			heapAddr := binary.LittleEndian.Uint64(data[absolutePos+0x08 : absolutePos+0x10])
			bufAddr := binary.LittleEndian.Uint64(data[absolutePos+0x24 : absolutePos+0x2c])
			bufLen := binary.LittleEndian.Uint64(data[absolutePos+0x10 : absolutePos+0x18])

			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "newBuffer",
				Address:    bufAddr,
				Info:       fmt.Sprintf("[0x%x newBufferWithLength:%d options:HazardTrackingModeUntracked]", heapAddr, bufLen),
				Offset:     int64(absolutePos),
			})

			// Add BufferHeapOffset call after buffer creation
			// Read offset from buffer structure
			calls = append(calls, InitCall{
				CallNumber: callNum + 1,
				Type:       "bufferHeapOffset",
				Address:    bufAddr,
				Info:       fmt.Sprintf("BufferHeapOffset(0x%x, 0)", bufAddr),
				Offset:     int64(absolutePos) + 1, // Slight offset to maintain ordering
			})
		}

		offset += pos + 5
	}

	// Find C\x00\x00\x00 records for command queue creation
	// Structure: "C\x00\x00\x00" + queue address + label info
	cMarker := []byte("C\x00\x00\x00")
	offset = 0
	queueAddrs := make(map[uint64]bool)
	for {
		pos := bytes.Index(data[offset:], cMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read address and check if it's a queue (has associated label)
		if absolutePos+12 <= len(data) {
			addr := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])
			if addr != 0 && !queueAddrs[addr] {
				queueAddrs[addr] = true
			}
		}

		offset += pos + 4
	}

	// Parse CS records using parseCSRecordsFromInit to get all named objects
	csRecords := parseCSRecordsFromInit(data)

	// Create InitCalls for functions from CS records
	// We need to determine which CS records are functions vs encoders/queues
	// Functions typically have lowercase_with_underscores naming
	for addr, name := range csRecords {
		if name == "" {
			continue
		}

		// Check if this is a command queue label (e.g., "Stream 0")
		if strings.Contains(name, "Stream") || strings.Contains(name, "Queue") {
			// This is a command queue - add newCommandQueue call
			// Note: setLabel calls would be separate API calls in the trace
			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "newCommandQueue",
				Address:    addr,
				Info:       fmt.Sprintf("%s = [Device newCommandQueue]", name),
				Label:      name,
			})
			callNum++
		} else if strings.Contains(strings.ToLower(name), "_") || (name[0] >= 'a' && name[0] <= 'z') {
			// Likely a function name (has underscores or starts lowercase)
			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "newFunction",
				Address:    addr,
				Info:       fmt.Sprintf("[0x%x newFunctionWithName:\"%s\"]", addr, name),
				Label:      name,
			})
			callNum++
		}
		// Encoder/command buffer labels will be handled separately in their respective sections
	}

	// Find Cui records (shared event creation)
	cuiMarker := []byte("Cui\x00")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], cuiMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read shared event address
		if absolutePos+0x14 <= len(data) {
			eventAddr := binary.LittleEndian.Uint64(data[absolutePos+0x0c : absolutePos+0x14])
			if eventAddr != 0 {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "newSharedEvent",
					Address:    eventAddr,
					Info:       "[Device newSharedEvent]",
					Offset:     int64(absolutePos),
				})
			}
		}

		offset += pos + 4
	}

	// Find Ctt records (pipeline state creation)
	// Structure:
	// +0x00: "Ctt\x00" (4 bytes)
	// +0x04: device address (8 bytes)
	// +0x0C: function address (8 bytes)
	// +0x14: unknown (12 bytes)
	// +0x20: pipeline state address (8 bytes)
	cttMarker := []byte("Ctt\x00")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], cttMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read pipeline address at +0x20
		if absolutePos+0x28 <= len(data) {
			pipelineAddr := binary.LittleEndian.Uint64(data[absolutePos+0x20 : absolutePos+0x28])
			funcAddr := binary.LittleEndian.Uint64(data[absolutePos+0x0c : absolutePos+0x14])

			if pipelineAddr != 0 {
				// Try to find function name
				funcName := "function"
				for _, call := range calls {
					if call.Type == "newFunction" && call.Address == funcAddr {
						// Use label if available, otherwise extract from Info
						if call.Label != "" {
							funcName = call.Label
						} else if strings.Contains(call.Info, "=") {
							parts := strings.Split(call.Info, "=")
							if len(parts) > 0 {
								funcName = strings.TrimSpace(parts[0])
							}
						}
						break
					}
				}

				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "newPipelineState",
					Address:    pipelineAddr,
					Info:       fmt.Sprintf("[Device newComputePipelineStateWithFunction:%s error:nil]", funcName),
					Offset:     int64(absolutePos),
				})
			}
		}

		offset += pos + 4
	}

	// Find fence creation records
	// These are typically marked with specific patterns in the trace
	// Looking for patterns that indicate fence objects
	offset = 0
	for i := 0; i < len(data)-32; i++ {
		// Check for fence-like patterns (need to identify the exact marker)
		// For now, we'll skip this as it's less critical
	}

	// Sort calls by offset to get correct ordering
	// Use a simple bubble sort since we have few items
	for i := 0; i < len(calls)-1; i++ {
		for j := i + 1; j < len(calls); j++ {
			if calls[j].Offset < calls[i].Offset {
				calls[i], calls[j] = calls[j], calls[i]
			}
		}
	}

	// Renumber after sorting
	for i := range calls {
		calls[i].CallNumber = startCallNum + i
	}
	callNum = startCallNum + len(calls)

	return calls, callNum, nil
}

// parseCommandBufferCalls parses all API calls within a command buffer.
func parseCommandBufferCalls(data []byte, cb *CommandBuffer, startCallNum int) (*CommandBufferCalls, int, error) {
	cbCalls := &CommandBufferCalls{
		Index:      cb.Index,
		Address:    0,
		CallNumber: startCallNum,
	}

	callNum := startCallNum

	// Parse command buffer address and queue address from C records
	// First C record after CUUU has queue address at +0x04
	// Second C record has command buffer address at +0x04
	cMarker := []byte("C\x00\x00\x00")
	cRecords := []uint64{}

	offset := 0
	for i := 0; i < 2; i++ {
		pos := bytes.Index(data[offset:], cMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		if absolutePos+12 <= len(data) {
			addr := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])
			cRecords = append(cRecords, addr)
		}

		offset += pos + 4
	}

	// Command buffer address is from the second C record
	if len(cRecords) >= 2 {
		cbCalls.Address = cRecords[1]
	}

	// Command buffer creation
	callNum++

	// Find encoder address from C records
	// The third C record (after queue and CB) has encoder address
	encoderAddr := uint64(0)
	offset = 0
	for i := 0; i < 3; i++ {
		pos := bytes.Index(data[offset:], cMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		if i == 2 && absolutePos+12 <= len(data) {
			encoderAddr = binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])
		}

		offset += pos + 4
	}

	// Parse dispatches
	dispatches, err := (&Trace{}).ParseDispatchInRegion(data, 0)
	if err != nil {
		return nil, 0, err
	}

	// Create encoder call
	if encoderAddr != 0 {
		cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
			CallNumber: callNum,
			Indented:   true,
			Type:       "encoder",
			Address:    encoderAddr,
			Details:    "computeCommandEncoder",
		})
		callNum++
	}

	// Add setComputePipelineState call
	// The pipeline state address should be passed in from parsing or extracted from trace
	// For now, we'll need to get it from the init calls
	cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
		CallNumber: callNum,
		Indented:   true,
		Type:       "setPipelineState",
		Details:    "setComputePipelineState", // Address will be added in formatting
	})
	callNum++

	// Parse buffer bindings (CtU<b>ulul records)
	bufferBindings, err := parseBufferBindings(data)
	if err != nil {
		return nil, 0, err
	}

	// Add setBuffer calls with actual buffer addresses
	for _, binding := range bufferBindings {
		cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
			CallNumber: callNum,
			Indented:   true,
			Type:       "setBuffer",
			Details:    fmt.Sprintf("setBuffer:0x%x offset:0 atIndex:%d", binding.BufferAddr, binding.Index),
			Offset:     binding.Offset,
		})
		callNum++
	}

	// Add dispatch calls
	for _, dispatch := range dispatches {
		cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
			CallNumber: callNum,
			Indented:   true,
			Type:       "dispatch",
			Details: fmt.Sprintf("dispatchThreadgroups:{%d, %d, %d} threadsPerThreadgroup:{%d, %d, %d}",
				dispatch.ThreadsX, dispatch.ThreadsY, dispatch.ThreadsZ,
				dispatch.ThreadsPerGroupX, dispatch.ThreadsPerGroupY, dispatch.ThreadsPerGroupZ),
			Offset: dispatch.Offset,
		})
		callNum++
	}

	// Add endEncoding
	cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
		CallNumber: callNum,
		Indented:   true,
		Type:       "endEncoding",
		Details:    "endEncoding",
	})
	callNum++

	// Add commit
	cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
		CallNumber: callNum,
		Indented:   false,
		Type:       "commit",
		Details:    "commit",
	})
	callNum++

	// Add waitUntilCompleted
	cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
		CallNumber: callNum,
		Indented:   false,
		Type:       "wait",
		Details:    "waitUntilCompleted",
	})
	callNum++

	return cbCalls, callNum, nil
}

// CommandBufferBinding represents a buffer binding within a command buffer.
type CommandBufferBinding struct {
	BufferAddr uint64
	Index      int
	Offset     int64
}

// parseBufferBindings extracts buffer binding records.
func parseBufferBindings(data []byte) ([]CommandBufferBinding, error) {
	var bindings []CommandBufferBinding

	// Pattern: "Ctulul" followed by encoder address and buffer address
	// Structure:
	// +0x00: "Ctulul\x00\x00" (8 bytes)
	// +0x08: encoder address (8 bytes)
	// +0x10: buffer address (8 bytes)
	// +0x18: offset (8 bytes)
	// +0x20: index (4 bytes)
	marker := []byte("Ctulul")

	offset := 0
	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}

		absolutePos := offset + pos

		// Read buffer address at +0x10 and index at +0x20
		if absolutePos+0x24 <= len(data) {
			bufAddr := binary.LittleEndian.Uint64(data[absolutePos+0x10 : absolutePos+0x18])
			index := binary.LittleEndian.Uint32(data[absolutePos+0x20 : absolutePos+0x24])

			bindings = append(bindings, CommandBufferBinding{
				BufferAddr: bufAddr,
				Index:      int(index),
				Offset:     int64(absolutePos),
			})
		}

		offset += pos + 6
	}

	return bindings, nil
}

// FormatAPICallList writes a formatted API call list similar to Xcode Instruments.
func (t *Trace) FormatAPICallList(w io.Writer) error {
	// Parse all API calls
	apiList, err := t.ParseAPICallList()
	if err != nil {
		return fmt.Errorf("parse API calls: %w", err)
	}

	// Format init calls
	for _, call := range apiList.InitCalls {
		if call.Type == "bufferHeapOffset" || call.Type == "setLabel" || call.Type == "requestResidency" {
			// These calls don't have the "address =" prefix
			fmt.Fprintf(w, "#%d %s\n", call.CallNumber, call.Info)
		} else {
			// Use label if available, otherwise use address
			prefix := fmt.Sprintf("0x%x", call.Address)
			if call.Label != "" {
				prefix = call.Label
			}
			fmt.Fprintf(w, "#%d %s = %s\n", call.CallNumber, prefix, call.Info)
		}
	}

	// Format command buffer calls
	for _, cb := range apiList.CommandBuffers {
		// Use command buffer label if available
		cbPrefix := fmt.Sprintf("0x%x", cb.Address)
		if cb.Label != "" {
			cbPrefix = cb.Label
		}
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", cb.CallNumber, cbPrefix, 0) // TODO: get queue address

		for _, call := range cb.Calls {
			indent := ""
			if call.Indented {
				indent = "\t"
			}

			if call.Address != 0 {
				// Use label if available for the call
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "%s#%d %s = [%s]\n", indent, call.CallNumber, callPrefix, call.Details)
			} else {
				fmt.Fprintf(w, "%s#%d [%s]\n", indent, call.CallNumber, call.Details)
			}
		}
	}

	return nil
}

// FormatAPICallListFull writes an expanded/full API call list showing all nesting levels.
// This matches the Xcode Instruments "expanded tree view" format where command buffers
// and encoders are shown at multiple indentation levels.
func (t *Trace) FormatAPICallListFull(w io.Writer) error {
	// Parse all API calls
	apiList, err := t.ParseAPICallList()
	if err != nil {
		return fmt.Errorf("parse API calls: %w", err)
	}

	// Format init calls (same as compact)
	for _, call := range apiList.InitCalls {
		if call.Type == "bufferHeapOffset" || call.Type == "setLabel" || call.Type == "requestResidency" {
			fmt.Fprintf(w, "#%d %s\n", call.CallNumber, call.Info)
		} else {
			prefix := fmt.Sprintf("0x%x", call.Address)
			if call.Label != "" {
				prefix = call.Label
			}
			fmt.Fprintf(w, "#%d %s = %s\n", call.CallNumber, prefix, call.Info)
		}
	}

	// Format command buffers - show full tree expansion
	for _, cb := range apiList.CommandBuffers {
		// Level 0: Full tree view (command buffer + all nested calls)
		cbPrefix := fmt.Sprintf("0x%x", cb.Address)
		if cb.Label != "" {
			cbPrefix = cb.Label
		}
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", cb.CallNumber, cbPrefix, 0)

		// Show command buffer setLabel if label exists
		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", cb.CallNumber+1, cb.Label)
		}

		// Show all calls indented
		for _, call := range cb.Calls {
			indent := ""
			if call.Indented {
				indent = "\t"
			}

			if call.Address != 0 {
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "%s#%d %s = [%s]\n", indent, call.CallNumber, callPrefix, call.Details)

				// Show encoder setLabel if label exists
				if call.Indented && call.Label != "" && call.Type == "encoder" {
					fmt.Fprintf(w, "%s#%d [setLabel:\"%s\"]\n", indent, call.CallNumber+1, call.Label)
				}
			} else {
				fmt.Fprintf(w, "%s#%d [%s]\n", indent, call.CallNumber, call.Details)
			}
		}

		fmt.Fprintf(w, "\n") // Blank line after each expansion level

		// Level 1: Command buffer with calls (no init calls)
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", cb.CallNumber, cbPrefix, 0)
		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", cb.CallNumber+1, cb.Label)
		}
		for _, call := range cb.Calls {
			if call.Address != 0 {
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "#%d %s = [%s]\n", call.CallNumber, callPrefix, call.Details)
				if call.Label != "" && call.Type == "encoder" {
					fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", call.CallNumber+1, call.Label)
				}
			} else {
				fmt.Fprintf(w, "#%d [%s]\n", call.CallNumber, call.Details)
			}
		}

		fmt.Fprintf(w, "\n") // Blank line after level 1

		// Level 2: Just encoder calls (deepest nesting)
		for _, call := range cb.Calls {
			if call.Type == "encoder" {
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "#%d %s = [%s]\n", call.CallNumber, callPrefix, call.Details)
				if call.Label != "" {
					fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", call.CallNumber+1, call.Label)
				}

				// Show encoder's calls (not indented at this level)
				// For now, we'd need to track which calls belong to which encoder
				// Simplifying to just show the encoder declaration
			} else if !call.Indented {
				// Show non-indented calls (commit, waitUntilCompleted)
				if call.Address != 0 {
					callPrefix := fmt.Sprintf("0x%x", call.Address)
					if call.Label != "" {
						callPrefix = call.Label
					}
					fmt.Fprintf(w, "#%d %s = [%s]\n", call.CallNumber, callPrefix, call.Details)
				} else {
					fmt.Fprintf(w, "#%d [%s]\n", call.CallNumber, call.Details)
				}
			}
		}

		fmt.Fprintf(w, "\n") // Blank line after level 2
	}

	return nil
}

// formatBufferCreation formats buffer creation calls from init section.
func formatBufferCreation(w io.Writer, data []byte, startCallNum int) int {
	callNum := startCallNum

	// Look for CU<b>ulul markers (buffer creation)
	marker := []byte("CU<b>ulul")
	offset := 0

	var buffers []struct {
		offset int
		addr   uint64
	}

	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Buffer address is at +0x24
		if absolutePos+0x2c <= len(data) {
			bufAddr := binary.LittleEndian.Uint64(data[absolutePos+0x24 : absolutePos+0x2c])
			buffers = append(buffers, struct {
				offset int
				addr   uint64
			}{absolutePos, bufAddr})
		}

		offset += pos + 9
	}

	// Print buffer creations
	for _, buf := range buffers {
		fmt.Fprintf(w, "#%d 0x%x = [Device newBufferWithBytes:<data> length:4 options:CPUCacheModeDefaultCache]\n",
			callNum, buf.addr)
		callNum++
	}

	return callNum
}

// parseCSRecordsFromInit parses CS records from init section to get encoder labels.
func parseCSRecordsFromInit(data []byte) map[uint64]string {
	labels := make(map[uint64]string)

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

			if labelEnd > labelStart {
				labelBytes := data[labelStart:labelEnd]
				// Check if it looks like a valid label (printable ASCII)
				validLabel := true
				for _, b := range labelBytes {
					if b < 32 || b > 126 {
						validLabel = false
						break
					}
				}
				if validLabel {
					labels[address] = string(labelBytes)
				}
			}
		}
	}

	return labels
}

// formatCommandBufferWithEncoders formats a command buffer with all encoders properly separated.
func (t *Trace) formatCommandBufferWithEncoders(w io.Writer, data []byte, allCBs []*CommandBuffer, cbIdx int, startCallNum int) int {
	cb := allCBs[cbIdx]
	callNum := startCallNum

	// Determine CB region
	var cbEnd int64
	if cbIdx+1 < len(allCBs) {
		cbEnd = allCBs[cbIdx+1].Offset
	} else {
		cbEnd = int64(len(data))
	}

	cbData := data[cb.Offset:cbEnd]

	// Get queue and CB addresses from C records
	queueAddr := uint64(0x10e625220)
	cbAddr := uint64(cb.Offset)

	cMarker := []byte("C\x00\x00\x00")
	cPos := 0
	cRecords := []uint64{}
	for i := 0; i < 2; i++ {
		pos := bytes.Index(cbData[cPos:], cMarker)
		if pos == -1 {
			break
		}
		absPos := cPos + pos
		if absPos+12 <= len(cbData) {
			addr := binary.LittleEndian.Uint64(cbData[absPos+4 : absPos+12])
			cRecords = append(cRecords, addr)
		}
		cPos = absPos + 4
	}

	if len(cRecords) >= 1 {
		queueAddr = cRecords[0]
	}
	if len(cRecords) >= 2 {
		cbAddr = cRecords[1]
	}

	// Print command buffer creation
	fmt.Fprintf(w, "#%d 0x%x = [0x%x commandBuffer]\n", callNum, cbAddr, queueAddr)
	callNum++

	// Use kernel names as encoder list (from init section parsing)
	// Each kernel name represents an encoder with that label
	numEncoders := len(t.KernelNames)

	// If no kernel names, try to detect encoder count from dispatches
	if numEncoders == 0 {
		dispatches, _ := t.ParseDispatchInRegion(cbData, cb.Offset)
		numEncoders = len(dispatches)
		if numEncoders == 0 {
			numEncoders = 1 // At least one encoder
		}
	}

	// Parse all buffer bindings and dispatches
	bindings, _ := parseBufferBindings(cbData)
	dispatches, _ := t.ParseDispatchInRegion(cbData, cb.Offset)

	// Parse pipeline state addresses from Ct records (type 14)
	pipelineAddrs := []uint64{}
	ctMarker := []byte("Ct\x00\x00")
	ctPos := 0
	for {
		pos := bytes.Index(cbData[ctPos:], ctMarker)
		if pos == -1 {
			break
		}
		absPos := ctPos + pos

		// Check type field at +0x14 (20 decimal)
		if absPos+24 <= len(cbData) {
			typeField := binary.LittleEndian.Uint32(cbData[absPos+20 : absPos+24])
			if typeField == 14 { // setComputePipelineState
				// Pipeline address is in target field
				targetAddr := binary.LittleEndian.Uint32(cbData[absPos+16 : absPos+20])
				pipelineAddrs = append(pipelineAddrs, uint64(targetAddr))
			}
		}

		ctPos = absPos + 4
	}

	// Get default pipeline address
	defaultPipeline := uint64(0)
	if len(pipelineAddrs) > 0 {
		defaultPipeline = pipelineAddrs[0]
	}

	// Distribute bindings and dispatches across encoders
	// Simple heuristic: divide evenly, with last encoder getting remainder
	bindingsPerEncoder := 1
	if numEncoders > 0 {
		bindingsPerEncoder = len(bindings) / numEncoders
		if bindingsPerEncoder == 0 {
			bindingsPerEncoder = 1
		}
	}

	dispatchesPerEncoder := 1
	if numEncoders > 0 {
		dispatchesPerEncoder = len(dispatches) / numEncoders
		if dispatchesPerEncoder == 0 {
			dispatchesPerEncoder = 1
		}
	}

	bindingIdx := 0
	dispatchIdx := 0

	// Format each encoder
	for encIdx := 0; encIdx < numEncoders; encIdx++ {
		// Get label for this encoder
		label := ""
		if encIdx < len(t.KernelNames) {
			label = t.KernelNames[encIdx]
		}

		// Print encoder creation with label
		if label != "" {
			fmt.Fprintf(w, "\t#%d %s = [computeCommandEncoder]\n", callNum, label)
		} else {
			fmt.Fprintf(w, "\t#%d encoder_%d = [computeCommandEncoder]\n", callNum, encIdx)
		}
		callNum++

		// Print setLabel if we have a label
		if label != "" {
			fmt.Fprintf(w, "\t#%d [setLabel:\"%s\"]\n", callNum, label)
			callNum++
		}

		// Print setComputePipelineState
		fmt.Fprintf(w, "\t#%d [setComputePipelineState:0x%x]\n", callNum, defaultPipeline)
		callNum++

		// Determine this encoder's bindings
		endBindingIdx := bindingIdx + bindingsPerEncoder
		if encIdx == numEncoders-1 {
			// Last encoder gets all remaining
			endBindingIdx = len(bindings)
		} else if endBindingIdx > len(bindings) {
			endBindingIdx = len(bindings)
		}

		// Print buffer bindings for this encoder
		for bindingIdx < endBindingIdx && bindingIdx < len(bindings) {
			binding := bindings[bindingIdx]
			fmt.Fprintf(w, "\t#%d [setBuffer:0x%x offset:%d atIndex:%d]\n",
				callNum, binding.BufferAddr, binding.Offset, binding.Index)
			callNum++
			bindingIdx++
		}

		// Determine this encoder's dispatches
		endDispatchIdx := dispatchIdx + dispatchesPerEncoder
		if encIdx == numEncoders-1 {
			endDispatchIdx = len(dispatches)
		} else if endDispatchIdx > len(dispatches) {
			endDispatchIdx = len(dispatches)
		}

		// Print dispatches for this encoder
		for dispatchIdx < endDispatchIdx && dispatchIdx < len(dispatches) {
			dispatch := dispatches[dispatchIdx]
			fmt.Fprintf(w, "\t#%d [dispatchThreadgroups:{%d, %d, %d} threadsPerThreadgroup:{%d, %d, %d}]\n",
				callNum,
				dispatch.ThreadsX, dispatch.ThreadsY, dispatch.ThreadsZ,
				dispatch.ThreadsPerGroupX, dispatch.ThreadsPerGroupY, dispatch.ThreadsPerGroupZ)
			callNum++
			dispatchIdx++
		}

		// Print endEncoding
		fmt.Fprintf(w, "\t#%d [endEncoding]\n", callNum)
		callNum++
	}

	// Print commit and wait
	fmt.Fprintf(w, "#%d [commit]\n", callNum)
	callNum++
	fmt.Fprintf(w, "#%d [waitUntilCompleted]\n", callNum)
	callNum++

	return callNum
}
