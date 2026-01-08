package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// DependencyGraph represents the data flow between operations.
type DependencyGraph struct {
	Nodes []DependencyNode
	Edges []DependencyEdge
}

type DependencyNode struct {
	ID    int
	Label string
}

// HazardType represents the type of memory hazard causing a dependency.
type HazardType int

const (
	// HazardRAW is Read After Write - reader depends on writer completing.
	HazardRAW HazardType = iota
	// HazardWAW is Write After Write - second write must wait for first write.
	HazardWAW
	// HazardWAR is Write After Read - write must wait for read to complete.
	HazardWAR
)

func (h HazardType) String() string {
	switch h {
	case HazardRAW:
		return "RAW"
	case HazardWAW:
		return "WAW"
	case HazardWAR:
		return "WAR"
	default:
		return "Unknown"
	}
}

type DependencyEdge struct {
	From   int
	To     int
	Buffer string     // Name of the buffer causing dependency
	Hazard HazardType // Type of memory hazard
}

// DependencyEvent represents a trace event relevant to dependencies.
type DependencyEvent struct {
	Offset  int64
	Type    EventType
	Label   string           // For CS
	Address uint64           // For Bind/Use
	Name    string           // For Bind
	Usage   MTLResourceUsage // Resource usage flags (Read, Write, Sample)
}

type EventType int

const (
	EventCS EventType = iota
	EventBind
	EventUse
)

// BuildDependencyGraph analyzes the trace to construct a dependency graph.
// Detects three types of memory hazards:
//   - RAW (Read After Write): reader depends on writer completing
//   - WAW (Write After Write): second write must wait for first write
//   - WAR (Write After Read): write must wait for read to complete
func (t *Trace) BuildDependencyGraph() (*DependencyGraph, error) {
	events, err := t.ParseDependencyEvents()
	if err != nil {
		return nil, err
	}

	graph := &DependencyGraph{}

	// Track buffer state
	lastWriter := make(map[uint64]int)            // Address -> Last Writer Node ID
	lastReaders := make(map[uint64]map[int]bool)  // Address -> Set of Reader Node IDs since last write
	bufferNames := make(map[uint64]string)        // Address -> Name

	currentNodeID := -1

	for _, ev := range events {
		switch ev.Type {
		case EventCS:
			// New Operation/Node
			currentNodeID = len(graph.Nodes)
			graph.Nodes = append(graph.Nodes, DependencyNode{
				ID:    currentNodeID,
				Label: ev.Label,
			})

		case EventBind:
			bufferNames[ev.Address] = ev.Name
			if currentNodeID == -1 {
				continue
			}

			// Handle Read access
			if ev.Usage.IsRead() {
				// RAW hazard: current node reads buffer that was previously written
				if writerID, ok := lastWriter[ev.Address]; ok && writerID != currentNodeID {
					graph.Edges = append(graph.Edges, DependencyEdge{
						From:   writerID,
						To:     currentNodeID,
						Buffer: ev.Name,
						Hazard: HazardRAW,
					})
				}
				// Track this node as a reader
				if lastReaders[ev.Address] == nil {
					lastReaders[ev.Address] = make(map[int]bool)
				}
				lastReaders[ev.Address][currentNodeID] = true
			}

			// Handle Write access
			if ev.Usage.IsWrite() {
				// WAW hazard: current node writes to buffer that was previously written
				if writerID, ok := lastWriter[ev.Address]; ok && writerID != currentNodeID {
					graph.Edges = append(graph.Edges, DependencyEdge{
						From:   writerID,
						To:     currentNodeID,
						Buffer: ev.Name,
						Hazard: HazardWAW,
					})
				}

				// WAR hazard: current node writes to buffer that has pending readers
				if readers, ok := lastReaders[ev.Address]; ok {
					for readerID := range readers {
						if readerID != currentNodeID {
							graph.Edges = append(graph.Edges, DependencyEdge{
								From:   readerID,
								To:     currentNodeID,
								Buffer: ev.Name,
								Hazard: HazardWAR,
							})
						}
					}
				}

				// Update writer and clear readers (new write invalidates old reads)
				lastWriter[ev.Address] = currentNodeID
				delete(lastReaders, ev.Address)
			}

		case EventUse:
			if currentNodeID == -1 {
				continue
			}
			// Treat Use as a Read - RAW hazard only
			if writerID, ok := lastWriter[ev.Address]; ok && writerID != currentNodeID {
				name := bufferNames[ev.Address]
				if name == "" {
					name = fmt.Sprintf("0x%x", ev.Address)
				}
				graph.Edges = append(graph.Edges, DependencyEdge{
					From:   writerID,
					To:     currentNodeID,
					Buffer: name,
					Hazard: HazardRAW,
				})
			}
			// Track as reader
			if lastReaders[ev.Address] == nil {
				lastReaders[ev.Address] = make(map[int]bool)
			}
			lastReaders[ev.Address][currentNodeID] = true
		}
	}

	// Deduplicate edges
	graph.Edges = deduplicateEdges(graph.Edges)

	return graph, nil
}

func deduplicateEdges(edges []DependencyEdge) []DependencyEdge {
	seen := make(map[string]bool)
	var unique []DependencyEdge
	for _, e := range edges {
		key := fmt.Sprintf("%d-%d", e.From, e.To)
		// We only care about unique From-To pairs for visualization,
		// but maybe we want to list all buffers?
		// Let's keep one edge per pair, but maybe concatenate buffer names?
		if !seen[key] {
			seen[key] = true
			unique = append(unique, e)
		}
	}
	return unique
}

// ParseDependencyEvents extracts relevant events from the capture file.
// It parses Ct records (compute dispatches with function addresses and buffer bindings)
// and resolves kernel names from DeviceLabels.
func (t *Trace) ParseDependencyEvents() ([]DependencyEvent, error) {
	var events []DependencyEvent

	data := t.CaptureData
	if len(data) == 0 {
		return nil, fmt.Errorf("no capture data")
	}

	// Markers for different record types
	ctMarker := []byte("Ct\x00\x00")        // Compute dispatch with function addr + buffer bindings
	ctBindMarker := []byte("CtU<b>ulul")    // Buffer definition with name
	ctUseMarker := []byte("Ctulul\x00")     // Buffer usage in dispatch

	// First pass: build buffer name map from CtU<b>ulul records
	bufferNames := make(map[uint64]string)
	offset := 0
	for offset < len(data)-40 {
		pos := bytes.Index(data[offset:], ctBindMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos
		base := absolutePos + 12 // After "CtU<b>ulul\x00\x00"

		if base+16 <= len(data) {
			bufferAddr := binary.LittleEndian.Uint64(data[base+8 : base+16])
			strStart := base + 16
			strEnd := strStart
			for strEnd < len(data) && data[strEnd] != 0 && strEnd-strStart < 64 {
				strEnd++
			}
			if strEnd > strStart {
				bufferNames[bufferAddr] = string(data[strStart:strEnd])
			}
		}
		offset = absolutePos + len(ctBindMarker)
	}

	// Second pass: parse Ct records (compute dispatches) to get kernel names and bindings
	// Ct records contain: function address (resolves to kernel name) + buffer binding array
	offset = 0
	for offset < len(data)-64 {
		pos := bytes.Index(data[offset:], ctMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Skip if this is part of Ctt, Ctulul, or CtU<b>ulul
		if absolutePos > 0 && data[absolutePos-1] == 'C' {
			offset = absolutePos + 4
			continue
		}
		// Check next byte isn't 't' (Ctt) or 'u' (Ctulul) or 'U' (CtU)
		if absolutePos+3 < len(data) {
			next := data[absolutePos+2]
			if next == 't' || next == 'u' || next == 'U' {
				offset = absolutePos + 4
				continue
			}
		}

		// Parse Ct record structure:
		// +0: "Ct\0\0" (4 bytes)
		// +4: pipeline_addr (8 bytes)
		// +12: function_addr (8 bytes)
		// +20: binding_count (4 bytes)
		// +24: stride (4 bytes)
		// +28: buffer_bindings (8 bytes each)
		base := absolutePos
		if base+28 > len(data) {
			offset = absolutePos + 4
			continue
		}

		funcAddr := binary.LittleEndian.Uint64(data[base+12 : base+20])
		bindingCount := binary.LittleEndian.Uint32(data[base+20 : base+24])
		stride := binary.LittleEndian.Uint32(data[base+24 : base+28])

		// Validate
		if stride != 8 || bindingCount > 100 {
			offset = absolutePos + 4
			continue
		}

		// Resolve kernel name from function address
		kernelName := t.DeviceLabels[funcAddr]
		if kernelName == "" {
			// Try to find in KernelNames by pattern matching (fallback)
			kernelName = fmt.Sprintf("kernel_0x%x", funcAddr)
		}

		// Emit CS event (kernel dispatch)
		events = append(events, DependencyEvent{
			Offset: int64(absolutePos),
			Type:   EventCS,
			Label:  kernelName,
		})

		// Parse buffer bindings and emit Bind events
		bindingsStart := base + 28
		for i := 0; i < int(bindingCount); i++ {
			bindOffset := bindingsStart + i*8
			if bindOffset+8 > len(data) {
				break
			}
			bufferAddr := binary.LittleEndian.Uint64(data[bindOffset : bindOffset+8])
			if bufferAddr == 0 {
				continue
			}

			name := bufferNames[bufferAddr]
			if name == "" {
				name = fmt.Sprintf("buf_0x%x", bufferAddr)
			}

			// Default to ReadWrite since explicit usage flags aren't in MTSP
			events = append(events, DependencyEvent{
				Offset:  int64(bindOffset),
				Type:    EventBind,
				Address: bufferAddr,
				Name:    name,
				Usage:   MTLResourceUsageRead | MTLResourceUsageWrite,
			})
		}

		offset = absolutePos + 28 + int(bindingCount)*8
	}

	// Third pass: parse Ctulul records for additional buffer usage
	offset = 0
	for offset < len(data)-32 {
		pos := bytes.Index(data[offset:], ctUseMarker)
		if pos == -1 {
			break
		}
		absolutePos := offset + pos

		// Skip if this is CtU<b>ulul
		if absolutePos >= 4 && bytes.Equal(data[absolutePos-4:absolutePos], []byte("<b>u")) {
			offset = absolutePos + len(ctUseMarker)
			continue
		}

		// Parse Ctulul structure:
		// +8: pipeline_addr (8 bytes)
		// +32: binding_count (4 bytes)
		// +36: stride (4 bytes)
		// +40: buffer_bindings
		base := absolutePos
		if base+40 > len(data) {
			offset = absolutePos + len(ctUseMarker)
			continue
		}

		bindingCount := binary.LittleEndian.Uint32(data[base+32 : base+36])
		stride := binary.LittleEndian.Uint32(data[base+36 : base+40])

		if stride != 8 || bindingCount > 100 {
			offset = absolutePos + len(ctUseMarker)
			continue
		}

		// Emit Use events for each binding
		bindingsStart := base + 40
		for i := 0; i < int(bindingCount); i++ {
			bindOffset := bindingsStart + i*8
			if bindOffset+8 > len(data) {
				break
			}
			bufferAddr := binary.LittleEndian.Uint64(data[bindOffset : bindOffset+8])
			if bufferAddr == 0 {
				continue
			}

			events = append(events, DependencyEvent{
				Offset:  int64(bindOffset),
				Type:    EventUse,
				Address: bufferAddr,
			})
		}

		offset = absolutePos + 40 + int(bindingCount)*8
	}

	return events, nil
}
