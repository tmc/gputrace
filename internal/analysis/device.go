package analysis

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases
type Trace = trace.Trace

// PipelineStateObject represents a Metal compute pipeline state object (PSO).
type PipelineStateObject struct {
	Address         uint64    // PSO address in memory
	FunctionAddr    uint64    // Associated function address
	FunctionName    string    // Kernel function name
	ThreadGroupSize [3]uint32 // Maximum threadgroup size (x, y, z)
}

// DeviceFunction represents a Metal function (kernel).
type DeviceFunction struct {
	Address     uint64 // Function address
	Name        string // Function name
	LibraryPath string // Path to metallib containing function
}

// DeviceBuffer represents a Metal buffer resource.
type DeviceBuffer struct {
	Address uint64 // Buffer address
	Name    string // Buffer name (e.g., "MTLBuffer-1000-0")
	Size    uint64 // Buffer size in bytes
}

// DeviceResources contains parsed information from device-resources files.
type DeviceResources struct {
	PSOs      []PipelineStateObject
	Functions []DeviceFunction
	Buffers   []DeviceBuffer

	// Index maps for fast lookups
	psoByAddr      map[uint64]*PipelineStateObject
	functionByAddr map[uint64]*DeviceFunction
	bufferByAddr   map[uint64]*DeviceBuffer
}

// ParseDeviceResources parses all device-resources-* files from the trace
// and extracts PSO information from the capture file.
func ParseDeviceResources(t *trace.Trace) (*DeviceResources, error) {
	dr := &DeviceResources{
		psoByAddr:      make(map[uint64]*PipelineStateObject),
		functionByAddr: make(map[uint64]*DeviceFunction),
		bufferByAddr:   make(map[uint64]*DeviceBuffer),
	}

	// Parse each device resources file (for function definitions)
	for deviceAddr, data := range t.DeviceResources {
		if err := dr.parseDeviceResourcesData(data, deviceAddr); err != nil {
			return nil, fmt.Errorf("parse device %s: %w", deviceAddr, err)
		}
	}

	// Parse PSOs from the capture file (contains Ct records with PSO references)
	if err := dr.parsePSOsFromCapture(t); err != nil {
		return nil, fmt.Errorf("parse PSOs from capture: %w", err)
	}

	return dr, nil
}

// parseDeviceResourcesData parses a single device-resources MTSP file.
func (dr *DeviceResources) parseDeviceResourcesData(data []byte, deviceAddr string) error {
	// Verify MTSP header
	if len(data) < 16 || string(data[0:4]) != trace.MagicMTSP {
		return fmt.Errorf("invalid MTSP magic")
	}

	// Parse functions section
	dr.parseFunctionsSection(data)

	// Parse buffers section
	dr.parseBuffersSection(data)

	return nil
}

// parseFunctionsSection extracts function metadata from device resources.
// Functions are stored with CS (string) records containing the function name
// followed by function metadata including addresses.
func (dr *DeviceResources) parseFunctionsSection(data []byte) {
	// Look for "functions" marker which precedes function definitions
	functionsIdx := bytes.Index(data, []byte("functions\x00"))
	if functionsIdx == -1 {
		return
	}

	// Scan for CS records after the functions marker
	for i := functionsIdx; i < len(data)-64; i++ {
		// Look for CS marker (0x43 0x53)
		if data[i] != 0x43 || data[i+1] != 0x53 {
			continue
		}

		// Extract function address (8 bytes after CS marker)
		funcAddrStart := i + 4
		if funcAddrStart+8 > len(data) {
			continue
		}
		funcAddr := binary.LittleEndian.Uint64(data[funcAddrStart : funcAddrStart+8])

		// Extract function name (typically 12 bytes after CS marker)
		nameStart := i + 12
		if nameStart >= len(data) {
			continue
		}

		// Find null terminator
		nameEnd := nameStart
		for nameEnd < len(data) && data[nameEnd] != 0 && nameEnd-nameStart < 256 {
			nameEnd++
		}

		if nameEnd <= nameStart || nameEnd-nameStart < 3 {
			continue
		}

		name := string(data[nameStart:nameEnd])

		// Filter out non-function names
		if !isPrintable(name) || name == "functions" || name == "function" {
			continue
		}

		// Create function entry
		function := DeviceFunction{
			Address: funcAddr,
			Name:    name,
		}

		dr.Functions = append(dr.Functions, function)
		dr.functionByAddr[funcAddr] = &dr.Functions[len(dr.Functions)-1]
	}
}

// parsePSOsFromCapture extracts PSO information from Ct records in the capture file.
func (dr *DeviceResources) parsePSOsFromCapture(t *Trace) error {
	// Parse MTSP records from capture
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return fmt.Errorf("parse MTSP records: %w", err)
	}

	// Track unique PSO addresses to avoid duplicates
	seenPSOs := make(map[uint64]bool)

	// Extract PSOs from Ct records
	for _, record := range records {
		if record.Type != trace.RecordTypeCt {
			continue
		}

		// Parse Ct record to get PSO and function addresses
		ct, err := record.ParseCtRecord()
		if err != nil {
			continue // Skip malformed records
		}

		// Skip if we've already seen this PSO
		if seenPSOs[ct.PipelineAddr] {
			continue
		}
		seenPSOs[ct.PipelineAddr] = true

		// Look up function name
		functionName := ""
		if fn, ok := dr.functionByAddr[ct.FunctionAddr]; ok {
			functionName = fn.Name
		}

		// Create PSO entry
		// Note: Threadgroup sizes are not directly available in Ct records
		// They would need to be extracted from dispatch commands or PSO metadata
		pso := PipelineStateObject{
			Address:         ct.PipelineAddr,
			FunctionAddr:    ct.FunctionAddr,
			FunctionName:    functionName,
			ThreadGroupSize: [3]uint32{0, 0, 0}, // TODO: Extract from dispatch records
		}

		dr.PSOs = append(dr.PSOs, pso)
		dr.psoByAddr[ct.PipelineAddr] = &dr.PSOs[len(dr.PSOs)-1]
	}

	return nil
}

// isPrintable checks if a string contains only printable ASCII characters.
func isPrintable(s string) bool {
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	return len(s) > 0
}

// parseBuffersSection extracts buffer metadata from device resources.
func (dr *DeviceResources) parseBuffersSection(data []byte) {
	// Look for buffer name patterns: "MTLBuffer-XXXX-Y"
	for i := 0; i < len(data)-64; i++ {
		// Look for "MTLBuffer-" prefix
		if i+10 >= len(data) || string(data[i:i+10]) != "MTLBuffer-" {
			continue
		}

		// Extract buffer name
		nameStart := i
		nameEnd := i + 10
		for nameEnd < len(data) && data[nameEnd] != 0 && nameEnd-nameStart < 64 {
			nameEnd++
		}

		if nameEnd <= nameStart {
			continue
		}

		name := string(data[nameStart:nameEnd])

		// Try to extract buffer address (often 8-12 bytes before name)
		var bufAddr uint64
		if i >= 20 {
			// Try multiple potential address locations
			bufAddr = binary.LittleEndian.Uint64(data[i-12 : i-4])
		}

		// Try to extract buffer size (often follows after name + padding)
		var bufSize uint64
		if nameEnd+16 <= len(data) {
			bufSize = binary.LittleEndian.Uint64(data[nameEnd+8 : nameEnd+16])
		}

		buffer := DeviceBuffer{
			Address: bufAddr,
			Name:    name,
			Size:    bufSize,
		}

		dr.Buffers = append(dr.Buffers, buffer)
		if bufAddr != 0 {
			dr.bufferByAddr[bufAddr] = &dr.Buffers[len(dr.Buffers)-1]
		}
	}
}

// AddressResolver provides lookup methods for device resources by address.
type AddressResolver struct {
	deviceResources *DeviceResources
}

// NewAddressResolver creates a new address resolver from device resources.
func NewAddressResolver(dr *DeviceResources) *AddressResolver {
	return &AddressResolver{
		deviceResources: dr,
	}
}

// ResolvePSO looks up a PSO by address.
func (ar *AddressResolver) ResolvePSO(addr uint64) (*PipelineStateObject, bool) {
	pso, ok := ar.deviceResources.psoByAddr[addr]
	return pso, ok
}

// ResolveFunction looks up a function by address.
func (ar *AddressResolver) ResolveFunction(addr uint64) (*DeviceFunction, bool) {
	fn, ok := ar.deviceResources.functionByAddr[addr]
	return fn, ok
}

// ResolveBuffer looks up a buffer by address.
func (ar *AddressResolver) ResolveBuffer(addr uint64) (*DeviceBuffer, bool) {
	buf, ok := ar.deviceResources.bufferByAddr[addr]
	return buf, ok
}

// ResolveFunctionName returns the function name for an address, or empty string.
func (ar *AddressResolver) ResolveFunctionName(addr uint64) string {
	if fn, ok := ar.ResolveFunction(addr); ok {
		return fn.Name
	}
	return ""
}

// ResolveBufferName returns the buffer name for an address, or empty string.
func (ar *AddressResolver) ResolveBufferName(addr uint64) string {
	if buf, ok := ar.ResolveBuffer(addr); ok {
		return buf.Name
	}
	return ""
}

// FormatPSOReport generates a human-readable report of all PSOs.
func (dr *DeviceResources) FormatPSOReport() string {
	report := "=== Pipeline State Objects (PSOs) ===\n\n"
	report += fmt.Sprintf("Total PSOs: %d\n", len(dr.PSOs))
	report += fmt.Sprintf("Total Functions: %d\n", len(dr.Functions))
	report += fmt.Sprintf("Total Buffers: %d\n\n", len(dr.Buffers))

	if len(dr.Functions) > 0 {
		report += "Functions:\n"
		for i, fn := range dr.Functions {
			if i >= 50 { // Limit output
				report += fmt.Sprintf("... and %d more functions\n\n", len(dr.Functions)-50)
				break
			}
			report += fmt.Sprintf("  [%3d] addr=0x%016x name=%q\n", i, fn.Address, fn.Name)
		}
		report += "\n"
	}

	if len(dr.PSOs) > 0 {
		report += "Pipeline State Objects:\n"
		for i, pso := range dr.PSOs {
			if i >= 50 { // Limit output
				report += fmt.Sprintf("... and %d more PSOs\n\n", len(dr.PSOs)-50)
				break
			}

			threadInfo := ""
			if pso.ThreadGroupSize[0] > 0 {
				threadInfo = fmt.Sprintf(" threads=(%d,%d,%d)",
					pso.ThreadGroupSize[0], pso.ThreadGroupSize[1], pso.ThreadGroupSize[2])
			}

			funcInfo := ""
			if pso.FunctionName != "" {
				funcInfo = fmt.Sprintf(" func=%q", pso.FunctionName)
			} else if pso.FunctionAddr != 0 {
				funcInfo = fmt.Sprintf(" func_addr=0x%x", pso.FunctionAddr)
			}

			report += fmt.Sprintf("  [%3d] addr=0x%016x%s%s\n",
				i, pso.Address, funcInfo, threadInfo)
		}
	}

	return report
}
