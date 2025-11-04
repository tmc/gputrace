package gputrace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReplayState holds the reconstructed Metal state for replay.
type ReplayState struct {
	// Device resources
	Device         any // MTLDevice (actual Metal device)
	CommandQueue   any // MTLCommandQueue

	// Restored resources (address -> actual resource)
	Buffers        map[uint64]any // address -> MTLBuffer
	Functions      map[uint64]any // address -> MTLFunction
	PipelineStates map[uint64]any // address -> MTLComputePipelineState

	// Resource metadata
	BufferSizes    map[uint64]uint64 // address -> size
	BufferNames    map[uint64]string // address -> name
	FunctionNames  map[uint64]string // address -> name

	// Trace reference
	Trace *Trace
}

// ReplayBufferInfo contains information about a buffer to restore.
type ReplayBufferInfo struct {
	Address  uint64
	Name     string
	Filename string
	Size     uint64
	Contents []byte
}

// FunctionInfo contains information about a function to restore.
type FunctionInfo struct {
	Address      uint64
	Name         string
	LibraryPath  string
}

// PipelineInfo contains information about a pipeline state to restore.
type PipelineInfo struct {
	Address      uint64
	FunctionAddr uint64
	FunctionName string
}

// NewReplayState creates a new replay state from a trace.
func NewReplayState(trace *Trace) *ReplayState {
	return &ReplayState{
		Buffers:        make(map[uint64]any),
		Functions:      make(map[uint64]any),
		PipelineStates: make(map[uint64]any),
		BufferSizes:    make(map[uint64]uint64),
		BufferNames:    make(map[uint64]string),
		FunctionNames:  make(map[uint64]string),
		Trace:          trace,
	}
}

// DiscoverBuffers scans the trace directory for all buffer files.
func (rs *ReplayState) DiscoverBuffers() ([]ReplayBufferInfo, error) {
	entries, err := os.ReadDir(rs.Trace.Path)
	if err != nil {
		return nil, fmt.Errorf("read trace directory: %w", err)
	}

	var buffers []ReplayBufferInfo

	for _, entry := range entries {
		name := entry.Name()

		// Look for MTLBuffer-* files (not symlinks)
		if !strings.HasPrefix(name, "MTLBuffer-") {
			continue
		}

		fullPath := filepath.Join(rs.Trace.Path, name)

		// Skip symlinks
		info, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		// Get buffer size from file size
		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		// Extract buffer address from filename
		// Format: MTLBuffer-<id>-<index> or MTLBuffer-<hex_addr>
		// For now, we'll use a placeholder address based on filename
		// In reality, we need to correlate with addresses in capture file

		bufInfo := ReplayBufferInfo{
			Name:     name,
			Filename: fullPath,
			Size:     uint64(stat.Size()),
			Address:  0, // Will be populated by correlating with capture data
		}

		// Read buffer contents
		contents, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read buffer %s: %w", name, err)
		}
		bufInfo.Contents = contents

		buffers = append(buffers, bufInfo)
	}

	return buffers, nil
}

// DiscoverFunctions extracts function information from device resources.
func (rs *ReplayState) DiscoverFunctions() ([]FunctionInfo, error) {
	deviceResources, err := rs.Trace.ParseDeviceResources()
	if err != nil {
		return nil, fmt.Errorf("parse device resources: %w", err)
	}

	var functions []FunctionInfo
	for _, fn := range deviceResources.Functions {
		funcInfo := FunctionInfo{
			Address:     fn.Address,
			Name:        fn.Name,
			LibraryPath: fn.LibraryPath,
		}
		functions = append(functions, funcInfo)
		rs.FunctionNames[fn.Address] = fn.Name
	}

	return functions, nil
}

// DiscoverPipelines extracts pipeline state information from the trace.
func (rs *ReplayState) DiscoverPipelines() ([]PipelineInfo, error) {
	deviceResources, err := rs.Trace.ParseDeviceResources()
	if err != nil {
		return nil, fmt.Errorf("parse device resources: %w", err)
	}

	var pipelines []PipelineInfo
	for _, pso := range deviceResources.PSOs {
		pipeInfo := PipelineInfo{
			Address:      pso.Address,
			FunctionAddr: pso.FunctionAddr,
			FunctionName: pso.FunctionName,
		}
		pipelines = append(pipelines, pipeInfo)
	}

	return pipelines, nil
}

// CorrelateBufferAddresses correlates buffer filenames with addresses from the capture file.
// This is necessary because buffer files are named by ID, but the capture file references them by address.
func (rs *ReplayState) CorrelateBufferAddresses(buffers []ReplayBufferInfo) ([]ReplayBufferInfo, error) {
	// Parse capture file for buffer address mappings
	captureData := rs.Trace.CaptureData
	if len(captureData) == 0 {
		return buffers, nil
	}

	// Build name -> address mapping from capture file
	// This uses the same binary marker pattern from buffer_timeline.go
	addrToName := make(map[uint64]string)
	marker := []byte{0x43, 0x74, 0x55, 0x3c, 0x62, 0x3e, 0x75, 0x6c, 0x75, 0x6c}
	offset := 0

	for {
		pos := strings.Index(string(captureData[offset:]), string(marker))
		if pos == -1 {
			break
		}

		absolutePos := offset + pos
		if absolutePos+0x24 <= len(captureData) {
			bufAddr := uint64(captureData[absolutePos+0x14]) |
				uint64(captureData[absolutePos+0x15])<<8 |
				uint64(captureData[absolutePos+0x16])<<16 |
				uint64(captureData[absolutePos+0x17])<<24 |
				uint64(captureData[absolutePos+0x18])<<32 |
				uint64(captureData[absolutePos+0x19])<<40 |
				uint64(captureData[absolutePos+0x1a])<<48 |
				uint64(captureData[absolutePos+0x1b])<<56

			nameStart := absolutePos + 0x1c
			if nameStart < len(captureData) {
				nameEnd := strings.IndexByte(string(captureData[nameStart:]), 0)
				if nameEnd > 0 && nameEnd < 100 {
					name := string(captureData[nameStart : nameStart+nameEnd])
					addrToName[bufAddr] = name
				}
			}
		}
		offset += pos + 10
	}

	// Create reverse mapping: name -> address
	nameToAddr := make(map[string]uint64)
	for addr, name := range addrToName {
		nameToAddr[name] = addr
	}

	// Correlate buffers with addresses
	for i := range buffers {
		if addr, ok := nameToAddr[buffers[i].Name]; ok {
			buffers[i].Address = addr
			rs.BufferSizes[addr] = buffers[i].Size
			rs.BufferNames[addr] = buffers[i].Name
		}
	}

	return buffers, nil
}

// RestoreState performs a dry-run analysis of what would be restored during replay.
// This doesn't actually create Metal objects (which requires CGo bindings).
func (rs *ReplayState) RestoreState() (*ReplayAnalysis, error) {
	analysis := &ReplayAnalysis{
		Buffers:   make([]ReplayBufferInfo, 0),
		Functions: make([]FunctionInfo, 0),
		Pipelines: make([]PipelineInfo, 0),
	}

	// Discover buffers
	buffers, err := rs.DiscoverBuffers()
	if err != nil {
		return nil, fmt.Errorf("discover buffers: %w", err)
	}

	// Correlate buffer addresses
	buffers, err = rs.CorrelateBufferAddresses(buffers)
	if err != nil {
		return nil, fmt.Errorf("correlate buffer addresses: %w", err)
	}
	analysis.Buffers = buffers

	// Discover functions
	functions, err := rs.DiscoverFunctions()
	if err != nil {
		// Non-fatal - continue without functions
		analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("discover functions: %v", err))
	} else {
		analysis.Functions = functions
	}

	// Discover pipelines
	pipelines, err := rs.DiscoverPipelines()
	if err != nil {
		// Non-fatal - continue without pipelines
		analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("discover pipelines: %v", err))
	} else {
		analysis.Pipelines = pipelines
	}

	// Calculate statistics
	analysis.TotalBufferSize = 0
	for _, buf := range analysis.Buffers {
		analysis.TotalBufferSize += buf.Size
	}
	analysis.BufferCount = len(analysis.Buffers)
	analysis.FunctionCount = len(analysis.Functions)
	analysis.PipelineCount = len(analysis.Pipelines)

	return analysis, nil
}

// ReplayAnalysis contains analysis of what would be restored during replay.
type ReplayAnalysis struct {
	Buffers   []ReplayBufferInfo
	Functions []FunctionInfo
	Pipelines []PipelineInfo

	// Statistics
	BufferCount     int
	FunctionCount   int
	PipelineCount   int
	TotalBufferSize uint64

	// Warnings and notes
	Warnings []string
	Notes    []string
}

// FormatAnalysis generates a human-readable report of the replay analysis.
func FormatReplayAnalysis(analysis *ReplayAnalysis) string {
	output := "=== Replay State Analysis ===\n\n"

	output += "Resource Summary:\n"
	output += fmt.Sprintf("  Buffers:   %d (%.2f MB total)\n",
		analysis.BufferCount,
		float64(analysis.TotalBufferSize)/(1024*1024))
	output += fmt.Sprintf("  Functions: %d\n", analysis.FunctionCount)
	output += fmt.Sprintf("  Pipelines: %d\n\n", analysis.PipelineCount)

	// Show buffers
	if len(analysis.Buffers) > 0 {
		output += "Buffers:\n"
		output += fmt.Sprintf("  %-30s %12s %18s\n", "Name", "Size", "Address")
		output += "  " + strings.Repeat("-", 65) + "\n"

		count := min(10, len(analysis.Buffers))
		for i := 0; i < count; i++ {
			buf := analysis.Buffers[i]
			addrStr := "unknown"
			if buf.Address != 0 {
				addrStr = fmt.Sprintf("0x%x", buf.Address)
			}
			output += fmt.Sprintf("  %-30s %12s %18s\n",
				truncateString(buf.Name, 30),
				formatBytes(buf.Size),
				addrStr)
		}
		if len(analysis.Buffers) > 10 {
			output += fmt.Sprintf("  ... and %d more\n", len(analysis.Buffers)-10)
		}
		output += "\n"
	}

	// Show functions
	if len(analysis.Functions) > 0 {
		output += "Functions:\n"
		output += fmt.Sprintf("  %-40s %18s\n", "Name", "Address")
		output += "  " + strings.Repeat("-", 60) + "\n"

		count := min(10, len(analysis.Functions))
		for i := 0; i < count; i++ {
			fn := analysis.Functions[i]
			output += fmt.Sprintf("  %-40s 0x%016x\n",
				truncateString(fn.Name, 40),
				fn.Address)
		}
		if len(analysis.Functions) > 10 {
			output += fmt.Sprintf("  ... and %d more\n", len(analysis.Functions)-10)
		}
		output += "\n"
	}

	// Show pipelines
	if len(analysis.Pipelines) > 0 {
		output += "Pipeline States:\n"
		output += fmt.Sprintf("  %-40s %18s\n", "Function", "PSO Address")
		output += "  " + strings.Repeat("-", 60) + "\n"

		count := min(10, len(analysis.Pipelines))
		for i := 0; i < count; i++ {
			pso := analysis.Pipelines[i]
			output += fmt.Sprintf("  %-40s 0x%016x\n",
				truncateString(pso.FunctionName, 40),
				pso.Address)
		}
		if len(analysis.Pipelines) > 10 {
			output += fmt.Sprintf("  ... and %d more\n", len(analysis.Pipelines)-10)
		}
		output += "\n"
	}

	// Show warnings
	if len(analysis.Warnings) > 0 {
		output += "Warnings:\n"
		for _, warning := range analysis.Warnings {
			output += fmt.Sprintf("  ! %s\n", warning)
		}
		output += "\n"
	}

	return output
}
