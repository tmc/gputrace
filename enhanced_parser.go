package gputrace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// EnhancedMetadata contains detailed information from GPU trace.
type EnhancedMetadata struct {
	CommandBuffers  []CommandBufferInfo
	Encoders        []EncoderInfo
	BufferBindings  []BufferBinding
	TextureBindings []TextureBinding
	TotalKernels    int
}

// CommandBufferInfo represents a Metal command buffer.
type CommandBufferInfo struct {
	Index     int
	Address   uint64
	Label     string
	Encoders  int
	StartTime uint64
	EndTime   uint64
}

// EncoderInfo represents a compute encoder.
type EncoderInfo struct {
	Index      int
	Label      string
	Dispatches []DispatchInfo
}

// DispatchInfo represents a single compute dispatch.
type DispatchInfo struct {
	KernelName  string
	ThreadGroup [3]uint32
	Threads     [3]uint32
	StartTime   uint64
	EndTime     uint64
}

// BufferBinding represents a bound buffer argument.
type BufferBinding struct {
	Name   string
	Size   uint64
	Offset uint64
	Index  int
}

// TextureBinding represents a bound texture argument.
type TextureBinding struct {
	Name   string
	Width  uint32
	Height uint32
	Index  int
}

// ExtractEnhancedMetadata extracts detailed structure from the GPU trace.
func (t *Trace) ExtractEnhancedMetadata() (*EnhancedMetadata, error) {
	meta := &EnhancedMetadata{}

	// Extract command buffers from capture data
	meta.CommandBuffers = t.extractCommandBuffers()

	// Extract encoder information
	meta.Encoders = t.extractEncoders()

	// Extract buffer bindings
	meta.BufferBindings = t.extractBufferBindings()

	// Extract texture bindings
	meta.TextureBindings = t.extractTextureBindings()

	// Count total kernels
	for _, enc := range meta.Encoders {
		meta.TotalKernels += len(enc.Dispatches)
	}

	return meta, nil
}

// extractCommandBuffers finds command buffer records in capture data.
func (t *Trace) extractCommandBuffers() []CommandBufferInfo {
	var cmdBuffers []CommandBufferInfo

	// Look for command buffer patterns (MTSP records with certain markers)
	data := t.CaptureData
	for i := 0; i < len(data)-100; i++ {
		// Look for "Culul" marker (command buffer marker observed in traces)
		if bytes.Contains(data[i:i+20], []byte("Culul")) {
			cmdBuf := CommandBufferInfo{
				Index: len(cmdBuffers),
			}

			// Try to extract address (8 bytes before the marker)
			if i >= 8 {
				cmdBuf.Address = binary.LittleEndian.Uint64(data[i-8 : i])
			}

			cmdBuffers = append(cmdBuffers, cmdBuf)
		}
	}

	return cmdBuffers
}

// extractEncoders finds compute encoder records.
func (t *Trace) extractEncoders() []EncoderInfo {
	var encoders []EncoderInfo

	// Use existing encoder labels
	for i, label := range t.EncoderLabels {
		enc := EncoderInfo{
			Index: i,
			Label: label,
		}
		encoders = append(encoders, enc)
	}

	// If no encoder labels, create synthetic entries based on kernel count
	if len(encoders) == 0 && len(t.KernelNames) > 0 {
		enc := EncoderInfo{
			Index: 0,
			Label: "ComputeEncoder",
		}
		encoders = append(encoders, enc)
	}

	return encoders
}

// extractBufferBindings finds MTLBuffer references.
func (t *Trace) extractBufferBindings() []BufferBinding {
	var bindings []BufferBinding

	// Search device resources for MTLBuffer entries
	// Pattern: "CU<b>ulul\0\0\0\0" followed by pointer then "MTLBuffer-XXXX-Y\0" then size (uint64)
	marker := []byte("CU<b>ulul")

	for _, data := range t.DeviceResources {
		for i := 0; i < len(data)-80; i++ {
			// Look for the CU<b>ulul marker
			if !bytes.Equal(data[i:i+len(marker)], marker) {
				continue
			}

			// Skip past marker (9 bytes) + nulls (3 bytes) + pointer (8 bytes) = 20 bytes
			bufferNameStart := i + 20
			if bufferNameStart >= len(data) {
				continue
			}

			// Check if this is an MTLBuffer entry
			if bufferNameStart+10 >= len(data) || !bytes.HasPrefix(data[bufferNameStart:], []byte("MTLBuffer-")) {
				continue
			}

			// Find null terminator for buffer name
			nameEnd := bytes.IndexByte(data[bufferNameStart:], 0)
			if nameEnd == -1 || nameEnd > 100 {
				continue
			}

			name := string(data[bufferNameStart : bufferNameStart+nameEnd])

			// Size pattern: name + null + 4 nulls + size (uint64)
			// Example: "MTLBuffer-1744-0\0\0\0\0\0" then size at +5
			sizeOffset := bufferNameStart + nameEnd + 5
			if sizeOffset+8 > len(data) {
				continue
			}

			// Read 8 bytes as size
			size := binary.LittleEndian.Uint64(data[sizeOffset : sizeOffset+8])

			// Sanity check: size should be reasonable (not a pointer)
			// MTL buffers are typically < 10GB, and > 0
			if size > 0 && size < 10*1024*1024*1024 {
				bindings = append(bindings, BufferBinding{
					Name:  name,
					Size:  size,
					Index: len(bindings),
				})
			}

			i += nameEnd + 50 // Skip past this entry
		}
	}

	return bindings
}

// extractTextureBindings finds texture references.
func (t *Trace) extractTextureBindings() []TextureBinding {
	var bindings []TextureBinding

	// Search for "textures" marker
	for _, data := range t.DeviceResources {
		idx := bytes.Index(data, []byte("textures"))
		if idx != -1 {
			// Found textures section, but format is complex
			// For now, just note that textures exist
			bindings = append(bindings, TextureBinding{
				Name:  "textures",
				Index: len(bindings),
			})
		}
	}

	return bindings
}

// AnalyzeTraceStructure provides a detailed analysis of the trace structure.
func (t *Trace) AnalyzeTraceStructure() string {
	meta, err := t.ExtractEnhancedMetadata()
	if err != nil {
		return fmt.Sprintf("Error analyzing trace: %v", err)
	}

	report := "=== GPU Trace Structure Analysis ===\n\n"

	// Metadata
	report += fmt.Sprintf("Trace Path: %s\n", t.Path)
	report += fmt.Sprintf("Capture Version: %d\n", t.Metadata.CaptureVersion)
	report += fmt.Sprintf("Graphics API: %d (1=Metal)\n", t.Metadata.GraphicsAPI)
	report += fmt.Sprintf("Device ID: %d\n\n", t.Metadata.DeviceID)

	// Command Buffers
	report += fmt.Sprintf("Command Buffers: %d\n", len(meta.CommandBuffers))
	for i, cb := range meta.CommandBuffers {
		if i < 5 { // Show first 5
			report += fmt.Sprintf("  [%d] Address: 0x%x\n", cb.Index, cb.Address)
		}
	}
	if len(meta.CommandBuffers) > 5 {
		report += fmt.Sprintf("  ... and %d more\n", len(meta.CommandBuffers)-5)
	}
	report += "\n"

	// Encoders
	report += fmt.Sprintf("Compute Encoders: %d\n", len(meta.Encoders))
	for _, enc := range meta.Encoders {
		report += fmt.Sprintf("  [%d] %s - %d dispatches\n", enc.Index, enc.Label, len(enc.Dispatches))
	}
	report += "\n"

	// Kernels
	report += fmt.Sprintf("GPU Kernels: %d\n", len(t.KernelNames))
	uniqueKernels := make(map[string]int)
	for _, name := range t.KernelNames {
		uniqueKernels[name]++
	}
	report += fmt.Sprintf("Unique Kernels: %d\n", len(uniqueKernels))

	// Show top kernels by frequency
	type kernelCount struct {
		name  string
		count int
	}
	var sorted []kernelCount
	for name, count := range uniqueKernels {
		sorted = append(sorted, kernelCount{name, count})
	}
	// Simple bubble sort for top 10
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	report += "\nTop Kernels by Frequency:\n"
	for i := 0; i < 10 && i < len(sorted); i++ {
		report += fmt.Sprintf("  %3dx %s\n", sorted[i].count, sorted[i].name)
	}
	report += "\n"

	// Buffers
	report += fmt.Sprintf("Buffer Bindings: %d\n", len(meta.BufferBindings))
	totalBufferSize := uint64(0)
	for _, buf := range meta.BufferBindings {
		totalBufferSize += buf.Size
	}
	report += fmt.Sprintf("Total Buffer Memory: %.2f MB\n", float64(totalBufferSize)/(1024*1024))
	if len(meta.BufferBindings) > 0 {
		report += "Sample Buffers:\n"
		for i, buf := range meta.BufferBindings {
			if i < 5 {
				report += fmt.Sprintf("  %s - %.2f MB\n", buf.Name, float64(buf.Size)/(1024*1024))
			}
		}
		if len(meta.BufferBindings) > 5 {
			report += fmt.Sprintf("  ... and %d more\n", len(meta.BufferBindings)-5)
		}
	}
	report += "\n"

	// Textures
	report += fmt.Sprintf("Texture Bindings: %d\n", len(meta.TextureBindings))

	// File sizes
	report += "\nTrace Files:\n"
	report += fmt.Sprintf("  Capture: %.2f MB\n", float64(len(t.CaptureData))/(1024*1024))
	totalResourceSize := 0
	for _, data := range t.DeviceResources {
		totalResourceSize += len(data)
	}
	report += fmt.Sprintf("  Device Resources: %.2f MB (%d files)\n",
		float64(totalResourceSize)/(1024*1024), len(t.DeviceResources))

	return report
}
