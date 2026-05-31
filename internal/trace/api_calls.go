package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InitCall represents an initialization API call before the first command buffer.
type InitCall struct {
	CallNumber int    `json:"call_number"`
	Type       string `json:"type"`
	Address    uint64 `json:"address"`
	Info       string `json:"info"`
	Label      string `json:"label,omitempty"`
	Offset     int64  `json:"offset"`
}

// FormattedAPICall represents a complete API call with all details.
type FormattedAPICall struct {
	CallNumber int    `json:"call_number"`
	Indented   bool   `json:"indented,omitempty"`
	Type       string `json:"type"`
	Address    uint64 `json:"address,omitempty"`
	Details    string `json:"details"`
	Label      string `json:"label,omitempty"`
	Offset     int64  `json:"offset"`
}

// APICallList represents a complete list of API calls for a trace.
type APICallList struct {
	InitCalls      []InitCall           `json:"init_calls"`
	CommandBuffers []CommandBufferCalls `json:"command_buffers"`
}

// CommandBufferCalls represents all API calls for a single command buffer.
type CommandBufferCalls struct {
	Index        int                `json:"index"`
	Address      uint64             `json:"address"`
	QueueAddress uint64             `json:"queue_address"`
	CallNumber   int                `json:"call_number"`
	Label        string             `json:"label,omitempty"`
	Calls        []FormattedAPICall `json:"calls"`
}

// ParseAPICallList extracts all API calls from the trace.
func (t *Trace) ParseAPICallList() (*APICallList, error) {
	capturePath := filepath.Join(t.Path, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		return nil, fmt.Errorf("read capture: %w", err)
	}

	// Build pipeline→function mapping for annotating setComputePipelineState calls
	pipelineMap := t.BuildPipelineFunctionMap()

	list := &APICallList{}
	callNum := 0

	// Find first CUUU marker
	cuuuMarker := []byte("CUUU")
	firstCUUU := bytes.Index(data, cuuuMarker)
	if firstCUUU == -1 {
		return nil, fmt.Errorf("no command buffers found")
	}

	// Parse labels from CS records in init section
	csRecords, labelMap := parseCSRecordsFromInit(data[:firstCUUU])

	// Parse initialization calls before first CUUU
	initCalls, _, err := parseInitCalls(data[:firstCUUU], callNum, csRecords, labelMap)
	if err != nil {
		return nil, fmt.Errorf("parse init calls: %w", err)
	}

	// Apply labels to init calls that don't already have labels
	// (Function calls from CS records already have correct labels set)
	for i := range initCalls {
		if initCalls[i].Label == "" {
			if label, exists := labelMap[initCalls[i].Address]; exists {
				initCalls[i].Label = label
			}
		}
	}

	// Init calls are already sorted by offset in parseInitCalls
	// Xcode output follows the file offset order, not type-based grouping

	// Renumber calls after sorting
	for i := range initCalls {
		initCalls[i].CallNumber = i
	}

	list.InitCalls = initCalls
	callNum = len(initCalls)

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
		_, cbLabelMap := parseCSRecordsFromInit(cbData)

		// Parse this command buffer's calls
		cbCalls, nextCallNum, err := parseCommandBufferCalls(cbData, cb, callNum, list.InitCalls, pipelineMap)
		if err != nil {
			return nil, fmt.Errorf("parse CB %d: %w", i, err)
		}

		// Apply command buffer label from both init section and command buffer section
		if label, exists := labelMap[cbCalls.Address]; exists {
			cbCalls.Label = label
		}
		if label, exists := cbLabelMap[cbCalls.Address]; exists {
			cbCalls.Label = label
		}

		// Apply labels to encoder calls within this command buffer
		// Only apply if the call doesn't already have a label (encoder calls already have labels from CS records)
		for j := range cbCalls.Calls {
			if cbCalls.Calls[j].Label == "" {
				if label, exists := cbLabelMap[cbCalls.Calls[j].Address]; exists {
					cbCalls.Calls[j].Label = label
				}
			}
		}

		list.CommandBuffers = append(list.CommandBuffers, *cbCalls)
		callNum = nextCallNum
	}

	return list, nil
}

// parseInitCalls parses initialization calls before the first command buffer.
func parseInitCalls(data []byte, startCallNum int, csRecords []FunctionRecord, labelMap map[uint64]string) ([]InitCall, int, error) {
	var calls []InitCall
	callNum := startCallNum

	if csRecords == nil {
		parsedRecords, parsedLabels := parseCSRecordsFromInit(data)
		csRecords = parsedRecords
		if labelMap == nil {
			labelMap = parsedLabels
		} else {
			for addr, label := range parsedLabels {
				if _, exists := labelMap[addr]; !exists {
					labelMap[addr] = label
				}
			}
		}
	}

	// Pattern: "C\x00\x00\x00" records with various types
	// We'll parse different record types and sort them by offset
	queueLabel, hasSingleQueue := singleCommandQueueLabel(csRecords)

	// Find CUt records (residency set creation)
	// Structure: "CUt\x00" + residency set address
	cutMarker := []byte("CUt\x00")
	residencySetAddrs := make(map[uint64]bool)
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
				residencySetAddrs[resAddr] = true
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

	// Find requestResidency calls. These use Ct records whose first address is a
	// residency set created earlier in the init section.
	ctMarker := []byte("Ct\x00\x00")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], ctMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		if absolutePos+0x0c <= len(data) {
			resAddr := binary.LittleEndian.Uint64(data[absolutePos+0x04 : absolutePos+0x0c])
			if residencySetAddrs[resAddr] {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "requestResidency",
					Address:    resAddr,
					Info:       fmt.Sprintf("[0x%x requestResidency]", resAddr),
					Offset:     int64(absolutePos),
				})
			}
		}

		offset += pos + 4
	}

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

	cMarker := []byte("C\x00\x00\x00")
	if hasSingleQueue {
		offset = 0
		for {
			pos := bytes.Index(data[offset:], cMarker)
			if pos == -1 {
				break
			}
			absolutePos := offset + pos

			if resAddr, ok := parseAddResidencySetAddress(data, absolutePos); ok && residencySetAddrs[resAddr] {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "addResidencySet",
					Address:    resAddr,
					Info:       fmt.Sprintf("[%s addResidencySet:0x%x]", queueLabel, resAddr),
					Label:      queueLabel,
					Offset:     int64(absolutePos),
				})
				callNum++
			}

			offset += pos + 4
		}
	}

	// Find Culul records (buffer creation from heap)
	// Structure:
	// +0x00: "Culul\x00\x00\x00"
	// +0x08: heap/device address (8 bytes)
	// +0x10: buffer length (8 bytes)
	// +0x24: buffer address (8 bytes)
	// +0x80: "Cuw\x00" companion record
	// +0x84: buffer address repeated by the companion record (8 bytes)
	// +0x8c: heap offset from the companion record (4 bytes)
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
			bufLen := binary.LittleEndian.Uint64(data[absolutePos+0x10 : absolutePos+0x18])
			bufAddr := binary.LittleEndian.Uint64(data[absolutePos+0x24 : absolutePos+0x2c])

			// Buffer created from heap uses HazardTrackingModeUntracked option
			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "newBuffer",
				Address:    bufAddr,
				Info:       fmt.Sprintf("[0x%x newBufferWithLength:%d options:HazardTrackingModeUntracked]", heapAddr, bufLen),
				Offset:     int64(absolutePos),
			})

			if heapOffset, ok := parseCululHeapOffset(data, absolutePos, bufAddr); ok {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "bufferHeapOffset",
					Address:    bufAddr,
					Info:       fmt.Sprintf("BufferHeapOffset(0x%x, %d)", bufAddr, heapOffset),
					Offset:     int64(absolutePos) + 1, // Slightly after newBuffer to maintain order
				})
			}
		}

		offset += pos + 5
	}

	// Find C\x00\x00\x00 records for command queue creation
	// Structure: "C\x00\x00\x00" + queue address + label info
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

	// Create InitCalls for functions from CS records list
	// We iterate over the list (not map) to preserve all entries including duplicates
	// Functions typically have lowercase_with_underscores naming
	for _, record := range csRecords {
		name := record.Label
		addr := record.CSAddress

		if name == "" {
			continue
		}

		// Check if this is a command queue label (e.g., "Stream 0")
		if isCommandQueueLabel(name) {
			// This is a command queue - add newCommandQueue call
			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "newCommandQueue",
				Address:    addr,
				Info:       fmt.Sprintf("%s = [Device newCommandQueue]", name),
				Label:      name,
				Offset:     record.Offset,
			})
			callNum++
			calls = append(calls, InitCall{
				CallNumber: callNum,
				Type:       "setLabel",
				Address:    addr,
				Info:       fmt.Sprintf("[%s setLabel:\"%s\"]", name, name),
				Label:      name,
				Offset:     record.Offset + 1,
			})
			callNum++
		} else if isLikelyInitFunctionLabel(name) {
			// Likely a function name (has underscores or starts lowercase)
			// Only create init call if this looks like a library address (top 32 bits >= 0x7)
			// This filters out the runtime function address (0x101...) and keeps only the CS record address (0x704...)
			if (addr >> 32) >= 0x7 {
				calls = append(calls, InitCall{
					CallNumber: callNum,
					Type:       "newFunction",
					Address:    addr,
					Info:       fmt.Sprintf("[0x%x newFunctionWithName:\"%s\"]", addr, name),
					Label:      name,
					Offset:     record.Offset,
				})
				callNum++
			}
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
				// Try to find function name from labelMap first, then from existing calls
				funcName := "function"
				if label, exists := labelMap[funcAddr]; exists {
					funcName = label
				} else {
					// Fallback: search in already-parsed calls
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

func singleCommandQueueLabel(records []FunctionRecord) (string, bool) {
	var (
		label string
		addr  uint64
		found bool
	)
	for _, record := range records {
		if !isCommandQueueLabel(record.Label) {
			continue
		}
		if found {
			return "", false
		}
		label = record.Label
		addr = record.CSAddress
		found = true
	}
	if !found || label == "" || addr == 0 {
		return "", false
	}
	return label, true
}

func isCommandQueueLabel(label string) bool {
	return strings.Contains(label, "Stream") || strings.Contains(label, "Queue")
}

func parseAddResidencySetAddress(data []byte, markerPos int) (uint64, bool) {
	const (
		recordSizeOffset = 0x24
		commandOffset    = 0x20
		countOffset      = 0x04
		addrOffset       = 0x04
		field1Offset     = 0x0c
		field2Offset     = 0x10
		recordSize       = 0x58
		commandFlags     = 0xffffc13d
		markerCount      = 0x0a
		field1           = 0x04
		field2           = 0x08
	)

	if markerPos < recordSizeOffset || markerPos+field2Offset+4 > len(data) {
		return 0, false
	}
	if binary.LittleEndian.Uint32(data[markerPos-recordSizeOffset:markerPos-commandOffset]) != recordSize {
		return 0, false
	}
	if binary.LittleEndian.Uint32(data[markerPos-commandOffset:markerPos-commandOffset+4]) != commandFlags {
		return 0, false
	}
	if binary.LittleEndian.Uint32(data[markerPos-countOffset:markerPos]) != markerCount {
		return 0, false
	}
	if binary.LittleEndian.Uint32(data[markerPos+field1Offset:markerPos+field1Offset+4]) != field1 {
		return 0, false
	}
	if binary.LittleEndian.Uint32(data[markerPos+field2Offset:markerPos+field2Offset+4]) != field2 {
		return 0, false
	}
	addr := binary.LittleEndian.Uint64(data[markerPos+addrOffset : markerPos+addrOffset+8])
	return addr, addr != 0
}

func parseCululHeapOffset(data []byte, cululPos int, bufAddr uint64) (uint64, bool) {
	const (
		cuwOffset         = 0x80
		cuwAddrOffset     = cuwOffset + 0x04
		cuwHeapOffset     = cuwOffset + 0x0c
		cuwHeapOffsetSize = 4
	)

	if cululPos < 0 || cululPos+cuwHeapOffset+cuwHeapOffsetSize > len(data) {
		return 0, false
	}
	if !bytes.Equal(data[cululPos+cuwOffset:cululPos+cuwOffset+4], []byte("Cuw\x00")) {
		return 0, false
	}
	cuwAddr := binary.LittleEndian.Uint64(data[cululPos+cuwAddrOffset : cululPos+cuwAddrOffset+8])
	if cuwAddr != bufAddr {
		return 0, false
	}
	return uint64(binary.LittleEndian.Uint32(data[cululPos+cuwHeapOffset : cululPos+cuwHeapOffset+cuwHeapOffsetSize])), true
}

func isLikelyInitFunctionLabel(name string) bool {
	if name == "" {
		return false
	}
	switch strings.ToLower(name) {
	case "fences":
		return false
	}
	return strings.Contains(strings.ToLower(name), "_") || (name[0] >= 'a' && name[0] <= 'z')
}

// EncoderSection represents a compute encoder and its associated calls.
type EncoderSection struct {
	Label        string
	Address      uint64
	PipelineAddr uint64
	StartOffset  int64
	EndOffset    int64 // Offset where this encoder ends (next encoder or end of CB)
}

// parseCommandBufferCalls parses all API calls within a command buffer.
func parseCommandBufferCalls(data []byte, cb *CommandBuffer, startCallNum int, initCalls []InitCall, pipelineMap PipelineFunctionMap) (*CommandBufferCalls, int, error) {
	cbCalls := &CommandBufferCalls{
		Index:      cb.Index,
		Address:    0,
		CallNumber: startCallNum,
		Label:      cb.Label,
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

	// Queue address is from the first C record, CB address from the second
	if len(cRecords) >= 1 {
		cbCalls.QueueAddress = cRecords[0]
	}
	if len(cRecords) >= 2 {
		cbCalls.Address = cRecords[1]
	}

	// Command buffer creation
	callNum++

	// Find all CS records within the command buffer to identify encoders
	// CS records mark encoder boundaries and contain labels
	// The first CS record is the command buffer label, subsequent ones are encoders
	csMarker := []byte("CS\x00\x00")
	var allCSRecords []EncoderSection

	offset = 0
	for {
		pos := bytes.Index(data[offset:], csMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Read CS address at +0x04
		if absolutePos+12 > len(data) {
			break
		}
		csAddr := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])

		// Read label (null-terminated string starting at +0x0c)
		labelStart := absolutePos + 12
		labelEnd := labelStart
		for labelEnd < len(data) && data[labelEnd] != 0 {
			labelEnd++
		}

		if labelEnd > len(data) {
			break
		}

		label := string(data[labelStart:labelEnd])

		allCSRecords = append(allCSRecords, EncoderSection{
			Label:       label,
			Address:     csAddr,
			StartOffset: int64(absolutePos),
		})

		offset += pos + 4
	}

	// The first CS record is the command buffer label
	// All subsequent CS records are encoders
	var encoders []EncoderSection
	if len(allCSRecords) > 1 {
		if cbCalls.Label == "" {
			cbCalls.Label = allCSRecords[0].Label
		}
		encoders = allCSRecords[1:]
	} else if len(allCSRecords) == 1 && cbCalls.Label == "" {
		cbCalls.Label = allCSRecords[0].Label
	}

	// Sort encoders by start offset to ensure correct ordering
	sort.Slice(encoders, func(i, j int) bool {
		return encoders[i].StartOffset < encoders[j].StartOffset
	})

	// Set end offsets for each encoder
	for i := range encoders {
		if i < len(encoders)-1 {
			encoders[i].EndOffset = encoders[i+1].StartOffset
		} else {
			encoders[i].EndOffset = int64(len(data))
		}
	}

	// Parse Ct records to find pipeline state bindings for each encoder
	// Ct record structure:
	// +0x00: "Ct\x00\x00" (4 bytes)
	// +0x04: encoder address (8 bytes)
	// +0x0c: pipeline state address (8 bytes)
	ctMarker := []byte("Ct\x00\x00")
	offset = 0
	for {
		pos := bytes.Index(data[offset:], ctMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		if absolutePos+20 <= len(data) {
			encoderAddr := binary.LittleEndian.Uint64(data[absolutePos+4 : absolutePos+12])
			pipelineAddr := binary.LittleEndian.Uint64(data[absolutePos+12 : absolutePos+20])

			// Match this pipeline state to the encoder with this address
			for i := range encoders {
				if encoders[i].Address == encoderAddr && encoders[i].PipelineAddr == 0 {
					encoders[i].PipelineAddr = pipelineAddr
					break
				}
			}
		}

		offset += pos + 4
	}

	// Parse all buffer bindings and dispatches first
	allBufferBindings, err := parseBufferBindings(data)
	if err != nil {
		return nil, 0, err
	}

	allDispatches, err := (&Trace{}).ParseDispatchInRegion(data, 0)
	if err != nil {
		return nil, 0, err
	}

	// Generate calls for each encoder
	for _, encoder := range encoders {
		// Create encoder call with label
		encoderPrefix := encoder.Label
		if encoderPrefix == "" {
			encoderPrefix = fmt.Sprintf("0x%x", encoder.Address)
		}

		cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
			CallNumber: callNum,
			Indented:   true,
			Type:       "encoder",
			Address:    encoder.Address,
			Details:    "computeCommandEncoder",
			Label:      encoderPrefix,
		})
		callNum++

		// Add setLabel call
		if encoder.Label != "" {
			cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
				CallNumber: callNum,
				Indented:   true,
				Type:       "setLabel",
				Details:    fmt.Sprintf("setLabel:\"%s\"", encoder.Label),
			})
			callNum++
		}

		// Add setComputePipelineState call
		if encoder.PipelineAddr != 0 {
			// Look up function name from pipeline→function mapping
			pipelineDetails := fmt.Sprintf("setComputePipelineState:0x%x", encoder.PipelineAddr)
			if funcName, exists := pipelineMap[encoder.PipelineAddr]; exists {
				pipelineDetails = fmt.Sprintf("setComputePipelineState:0x%x (%s)", encoder.PipelineAddr, funcName)
			}
			cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
				CallNumber: callNum,
				Indented:   true,
				Type:       "setPipelineState",
				Details:    pipelineDetails,
			})
			callNum++
		}

		// Add buffer bindings that fall within this encoder's range
		for _, binding := range allBufferBindings {
			if binding.Offset >= encoder.StartOffset && binding.Offset < encoder.EndOffset {
				cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
					CallNumber: callNum,
					Indented:   true,
					Type:       "setBuffer",
					Details:    fmt.Sprintf("setBuffer:0x%x offset:0 atIndex:%d", binding.BufferAddr, binding.Index),
					Offset:     binding.Offset,
				})
				callNum++
			}
		}

		// Add dispatches that fall within this encoder's range
		for _, dispatch := range allDispatches {
			if dispatch.Offset >= encoder.StartOffset && dispatch.Offset < encoder.EndOffset {
				cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
					CallNumber: callNum,
					Indented:   true,
					Type:       "dispatch",
					Details: fmt.Sprintf("dispatchThreads:{%d, %d, %d} threadsPerThreadgroup:{%d, %d, %d}",
						dispatch.ThreadsX, dispatch.ThreadsY, dispatch.ThreadsZ,
						dispatch.ThreadsPerGroupX, dispatch.ThreadsPerGroupY, dispatch.ThreadsPerGroupZ),
					Offset: dispatch.Offset,
				})
				callNum++
			}
		}

		// Add endEncoding for this encoder
		cbCalls.Calls = append(cbCalls.Calls, FormattedAPICall{
			CallNumber: callNum,
			Indented:   true,
			Type:       "endEncoding",
			Details:    "endEncoding",
		})
		callNum++
	}

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

	// Renumber calls as we format (some types are filtered out)
	displayCallNum := 0

	// Format init calls
	for _, call := range apiList.InitCalls {
		// Skip bufferHeapOffset and newSharedEvent in compact format to match Xcode
		if call.Type == "bufferHeapOffset" || call.Type == "newSharedEvent" {
			continue
		}

		if call.Type == "setLabel" || call.Type == "requestResidency" || call.Type == "addResidencySet" {
			// These calls don't have the "address =" prefix
			fmt.Fprintf(w, "#%d %s\n", displayCallNum, call.Info)
		} else {
			// Use label if available, otherwise use address
			prefix := fmt.Sprintf("0x%x", call.Address)
			if call.Label != "" {
				prefix = call.Label
			}
			fmt.Fprintf(w, "#%d %s = %s\n", displayCallNum, prefix, call.Info)
		}
		displayCallNum++
	}

	// Format command buffer calls
	for _, cb := range apiList.CommandBuffers {
		// Use command buffer label if available
		cbPrefix := fmt.Sprintf("0x%x", cb.Address)
		if cb.Label != "" {
			cbPrefix = cb.Label
		}
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", displayCallNum, cbPrefix, cb.QueueAddress)
		displayCallNum++

		// Add setLabel call if command buffer has a label
		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", displayCallNum, cb.Label)
			displayCallNum++
		}

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
				fmt.Fprintf(w, "%s#%d %s = [%s]\n", indent, displayCallNum, callPrefix, call.Details)
				displayCallNum++
			} else {
				fmt.Fprintf(w, "%s#%d [%s]\n", indent, displayCallNum, call.Details)
				displayCallNum++
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

	// Renumber calls as we format (some types are filtered out)
	displayCallNum := 0

	// Format init calls (same as compact)
	for _, call := range apiList.InitCalls {
		// Skip certain types to match Xcode output
		if call.Type == "bufferHeapOffset" || call.Type == "newSharedEvent" {
			continue
		}

		if call.Type == "setLabel" || call.Type == "requestResidency" || call.Type == "addResidencySet" {
			fmt.Fprintf(w, "#%d %s\n", displayCallNum, call.Info)
		} else {
			prefix := fmt.Sprintf("0x%x", call.Address)
			if call.Label != "" {
				prefix = call.Label
			}
			fmt.Fprintf(w, "#%d %s = %s\n", displayCallNum, prefix, call.Info)
		}
		displayCallNum++
	}

	// Format command buffers - show full tree expansion
	for _, cb := range apiList.CommandBuffers {
		// Save the starting call number for this command buffer
		cbStartNum := displayCallNum

		// Level 0: Full tree view (command buffer + all nested calls)
		cbPrefix := fmt.Sprintf("0x%x", cb.Address)
		if cb.Label != "" {
			cbPrefix = cb.Label
		}
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", displayCallNum, cbPrefix, cb.QueueAddress)
		displayCallNum++

		// Show command buffer setLabel if label exists
		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", displayCallNum, cb.Label)
			displayCallNum++
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
				fmt.Fprintf(w, "%s#%d %s = [%s]\n", indent, displayCallNum, callPrefix, call.Details)
			} else {
				fmt.Fprintf(w, "%s#%d [%s]\n", indent, displayCallNum, call.Details)
			}
			displayCallNum++
		}

		fmt.Fprintf(w, "\n") // Blank line after each expansion level

		// Level 1: Command buffer with calls (no init calls)
		// Show CB + first encoder only (up to its endEncoding)
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", cbStartNum, cbPrefix, cb.QueueAddress)
		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", cbStartNum+1, cb.Label)
		}

		cbCallStart := cbStartNum
		if cb.Label != "" {
			cbCallStart += 2
		} else {
			cbCallStart++
		}

		// Only show calls up to and including the first encoder's endEncoding
		firstEncoderEnd := len(cb.Calls)
		encoderCount := 0
		for i, call := range cb.Calls {
			if call.Type == "encoder" {
				encoderCount++
			}
			if call.Type == "endEncoding" {
				encoderCount--
				if encoderCount == 0 {
					firstEncoderEnd = i + 1
					break
				}
			}
		}

		for i := 0; i < firstEncoderEnd; i++ {
			call := cb.Calls[i]
			callNum := cbCallStart + i
			if call.Address != 0 {
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "#%d %s = [%s]\n", callNum, callPrefix, call.Details)
			} else {
				fmt.Fprintf(w, "#%d [%s]\n", callNum, call.Details)
			}
		}

		fmt.Fprintf(w, "\n") // Blank line after level 1

		// Level 2+: Expand each encoder separately with its nested calls
		// Group calls by encoder
		var encoders []struct {
			encoderIndex int
			calls        []int // indices into cb.Calls
		}
		currentEncoder := -1
		for i, call := range cb.Calls {
			if call.Type == "encoder" {
				encoders = append(encoders, struct {
					encoderIndex int
					calls        []int
				}{encoderIndex: i, calls: []int{i}})
				currentEncoder = len(encoders) - 1
			} else if currentEncoder >= 0 && call.Indented {
				// This call belongs to the current encoder
				encoders[currentEncoder].calls = append(encoders[currentEncoder].calls, i)
			}
		}

		// Output each encoder expansion level (without blank lines between)
		for _, encoder := range encoders {
			for _, callIdx := range encoder.calls {
				call := cb.Calls[callIdx]
				callNum := cbCallStart + callIdx

				if call.Address != 0 {
					callPrefix := fmt.Sprintf("0x%x", call.Address)
					if call.Label != "" {
						callPrefix = call.Label
					}
					fmt.Fprintf(w, "#%d %s = [%s]\n", callNum, callPrefix, call.Details)
				} else {
					fmt.Fprintf(w, "#%d [%s]\n", callNum, call.Details)
				}
			}
		}

		// Level 3: Show last encoder again with non-indented calls
		if len(encoders) > 0 {
			fmt.Fprintf(w, "\n") // Blank line before final expansion
			lastEncoder := encoders[len(encoders)-1]
			for _, callIdx := range lastEncoder.calls {
				call := cb.Calls[callIdx]
				callNum := cbCallStart + callIdx

				if call.Address != 0 {
					callPrefix := fmt.Sprintf("0x%x", call.Address)
					if call.Label != "" {
						callPrefix = call.Label
					}
					fmt.Fprintf(w, "#%d %s = [%s]\n", callNum, callPrefix, call.Details)
				} else {
					fmt.Fprintf(w, "#%d [%s]\n", callNum, call.Details)
				}
			}
		}

		// Show non-indented calls (commit, waitUntilCompleted) at the end
		for i, call := range cb.Calls {
			if !call.Indented {
				callNum := cbCallStart + i
				if call.Address != 0 {
					callPrefix := fmt.Sprintf("0x%x", call.Address)
					if call.Label != "" {
						callPrefix = call.Label
					}
					fmt.Fprintf(w, "#%d %s = [%s]\n", callNum, callPrefix, call.Details)
				} else {
					fmt.Fprintf(w, "#%d [%s]\n", callNum, call.Details)
				}
			}
		}
		// No trailing newline after the last command buffer
	}

	return nil
}

// FunctionRecord represents a parsed CS record with function label information.
type FunctionRecord struct {
	CSAddress   uint64 // The CS record address
	FuncAddress uint64 // The runtime function address (if found)
	Label       string // The label/name
	Offset      int64  // Offset in the capture file
}

// parseCSRecordsFromInit parses CS records from init section to get encoder labels.
// Returns both a list of records (for creating multiple entries with same address)
// and a map for quick lookup by address.
func parseCSRecordsFromInit(data []byte) ([]FunctionRecord, map[uint64]string) {
	var records []FunctionRecord
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
					label := string(labelBytes)
					labels[address] = label

					// Create a function record entry
					record := FunctionRecord{
						CSAddress: address,
						Label:     label,
						Offset:    int64(i),
					}

					// Also try to read function address after the type marker
					// Pattern: label + null + padding + "t\x00\x00\x00" + function_address (8 bytes)
					// Only add it if it looks like a function name (has underscores or lowercase start)
					if strings.Contains(strings.ToLower(label), "_") || (len(label) > 0 && label[0] >= 'a' && label[0] <= 'z') {
						// Search for "t\x00\x00\x00" type marker after label
						typeMarker := []byte{'t', 0, 0, 0}
						searchStart := labelEnd + 1
						searchEnd := i + 0x30 // Search within reasonable range
						if searchEnd > len(data) {
							searchEnd = len(data)
						}
						if searchStart >= searchEnd {
							continue
						}

						typeMarkerPos := bytes.Index(data[searchStart:searchEnd], typeMarker)
						if typeMarkerPos != -1 {
							// Function address is 4 bytes after type marker
							funcAddrOffset := searchStart + typeMarkerPos + 4
							if funcAddrOffset+8 <= len(data) {
								funcAddr := binary.LittleEndian.Uint64(data[funcAddrOffset : funcAddrOffset+8])
								if funcAddr != 0 && funcAddr != address {
									record.FuncAddress = funcAddr
									labels[funcAddr] = label
								}
							}
						}
					}

					records = append(records, record)
				}
			}
		}
	}

	return records, labels
}
