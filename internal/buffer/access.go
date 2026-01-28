package buffer

import (
	"fmt"
	"sort"

	"github.com/tmc/gputrace/internal/trace"
)

// BufferAccessPattern represents the access pattern for a single buffer.
type BufferAccessPattern struct {
	BufferAddress uint64   // Buffer memory address
	BufferName    string   // Buffer name (e.g., "MTLBuffer-12-0")
	AccessCount   int      // Number of times buffer is accessed
	EncoderRefs   []string // List of encoder labels that access this buffer
	IsReadOnly    bool     // True if buffer is only read, never written
	IsWriteOnly   bool     // True if buffer is only written, never read
	IsReadWrite   bool     // True if buffer is both read and written
	FirstAccess   int      // Index of first encoder to access buffer
	LastAccess    int      // Index of last encoder to access buffer
	IsAliased     bool     // True if buffer shares address with other buffers
	AliasedWith   []string // Names of buffers sharing the same address
}

// BufferAccessAnalysis contains comprehensive buffer access pattern analysis.
type BufferAccessAnalysis struct {
	Patterns       map[uint64]*BufferAccessPattern // Map from buffer address to access pattern
	UnusedBuffers  []string                        // List of buffer names that are never accessed
	AliasedBuffers map[uint64][]string             // Map from address to list of aliased buffer names
	TotalBuffers   int                             // Total number of buffers in trace
	AccessedCount  int                             // Number of buffers that are accessed
	UnusedCount    int                             // Number of buffers that are never accessed
}

// AnalyzeBufferAccess performs comprehensive buffer access pattern analysis.
//
// This function analyzes Ctulul records to track how buffers are accessed:
// - Which encoders access which buffers
// - Read-only vs read-write vs write-only buffers
// - Buffer reuse frequency across encoders
// - Memory aliasing (multiple buffer names for same address)
// - Unused buffers (allocated but never accessed)
func AnalyzeBufferAccess(t *trace.Trace) (*BufferAccessAnalysis, error) {
	analysis := &BufferAccessAnalysis{
		Patterns:       make(map[uint64]*BufferAccessPattern),
		AliasedBuffers: make(map[uint64][]string),
	}

	// Build address-to-name mapping from capture file (CtU<b>ulul records)
	addrToNames, err := buildBufferAddressMapping(t)
	if err != nil {
		return nil, fmt.Errorf("build address mapping: %w", err)
	}

	// Get encoder labels for correlation
	encoderLabels := t.EncoderLabels
	if len(encoderLabels) == 0 {
		encoderLabels = t.KernelNames
	}

	// Parse Ctulul records (buffer bindings) from capture file
	// These are the actual buffer binding calls
	bindings, err := parseAllBufferBindings(t)
	if err != nil {
		return nil, fmt.Errorf("parse buffer bindings: %w", err)
	}

	// Map bindings to encoders
	// Simple heuristic: distribute bindings across encoders based on count
	if len(encoderLabels) > 0 && len(bindings) > 0 {
		bindingsPerEncoder := len(bindings) / len(encoderLabels)
		if bindingsPerEncoder == 0 {
			bindingsPerEncoder = 1
		}

		encoderIdx := 0
		for i, binding := range bindings {
			// Move to next encoder after bindingsPerEncoder bindings
			if i > 0 && i%bindingsPerEncoder == 0 && encoderIdx < len(encoderLabels)-1 {
				encoderIdx++
			}

			// Get encoder label
			encoderLabel := "unknown"
			if encoderIdx < len(encoderLabels) {
				encoderLabel = encoderLabels[encoderIdx]
			}

			// Track buffer access
			pattern := analysis.getOrCreatePattern(binding.BufferAddr, addrToNames)
			pattern.AccessCount++
			pattern.EncoderRefs = append(pattern.EncoderRefs, encoderLabel)

			if pattern.FirstAccess == 0 || encoderIdx < pattern.FirstAccess {
				pattern.FirstAccess = encoderIdx
			}
			if encoderIdx > pattern.LastAccess {
				pattern.LastAccess = encoderIdx
			}
		}
	}

	// Detect aliasing (multiple names for same address)
	addressToNames := make(map[uint64][]string)
	for addr, names := range addrToNames {
		if len(names) > 1 {
			analysis.AliasedBuffers[addr] = names
			addressToNames[addr] = names

			// Mark patterns as aliased
			if pattern, ok := analysis.Patterns[addr]; ok {
				pattern.IsAliased = true
				pattern.AliasedWith = names
			}
		}
	}

	// Detect unused buffers (in mapping but not in patterns)
	for addr, names := range addrToNames {
		if _, accessed := analysis.Patterns[addr]; !accessed {
			analysis.UnusedBuffers = append(analysis.UnusedBuffers, names...)
		}
	}

	// Calculate statistics
	// TotalBuffers = all unique buffer addresses we found (accessed + unused)
	// This includes both named buffers (from CtU<b>ulul) and unnamed buffers (from Ctulul bindings)
	analysis.TotalBuffers = len(analysis.Patterns) + len(analysis.UnusedBuffers)
	analysis.AccessedCount = len(analysis.Patterns)
	analysis.UnusedCount = len(analysis.UnusedBuffers)

	// Infer read/write patterns based on access patterns and buffer names
	// Note: Explicit MTLResourceUsage flags are not stored in MTSP capture format.
	// We use heuristics based on:
	// 1. Buffer naming patterns (e.g., "output", "result" suggest write)
	// 2. Access frequency (single access often indicates output)
	// 3. Position in encoder sequence (last accessed buffers often are outputs)
	for _, pattern := range analysis.Patterns {
		name := pattern.BufferName
		accessCount := pattern.AccessCount
		encoderCount := len(uniqueStrings(pattern.EncoderRefs))

		// Heuristics for read-only buffers:
		// - Accessed many times by multiple encoders (likely shared input)
		// - Name contains "input", "const", "weight", "param"
		isLikelyReadOnly := accessCount > 3 && encoderCount > 2

		// Heuristics for write-only buffers:
		// - Accessed only once (output that's consumed externally)
		// - Name contains "output", "result", "out"
		isLikelyWriteOnly := accessCount == 1 && encoderCount == 1

		// Apply heuristics
		if isLikelyReadOnly {
			pattern.IsReadOnly = true
			pattern.IsWriteOnly = false
			pattern.IsReadWrite = false
		} else if isLikelyWriteOnly {
			pattern.IsReadOnly = false
			pattern.IsWriteOnly = true
			pattern.IsReadWrite = false
		} else {
			// Default: assume read-write for most buffers
			// This is conservative and ensures correctness for dependency tracking
			pattern.IsReadOnly = false
			pattern.IsWriteOnly = false
			pattern.IsReadWrite = true
		}

		// Override based on naming patterns if present
		_ = name // Future: implement name-based heuristics
	}

	return analysis, nil
}

// getOrCreatePattern gets or creates a BufferAccessPattern for the given address.
func (analysis *BufferAccessAnalysis) getOrCreatePattern(addr uint64, addrToNames map[uint64][]string) *BufferAccessPattern {
	if pattern, ok := analysis.Patterns[addr]; ok {
		return pattern
	}

	// Create new pattern
	pattern := &BufferAccessPattern{
		BufferAddress: addr,
		EncoderRefs:   make([]string, 0),
	}

	// Find buffer name
	if names, ok := addrToNames[addr]; ok && len(names) > 0 {
		pattern.BufferName = names[0] // Use first name
	}

	analysis.Patterns[addr] = pattern
	return pattern
}

// BufferAccessBinding represents a single buffer binding from a Ctulul record.
type BufferAccessBinding struct {
	BufferAddr uint64
	Index      int
	Offset     int64
}

// parseAllBufferBindings parses all Ctulul records to extract buffer bindings.
func parseAllBufferBindings(t *trace.Trace) ([]BufferAccessBinding, error) {
	var bindings []BufferAccessBinding

	// Pattern: "Ctulul" (6 bytes)
	marker := []byte("Ctulul")
	data := t.CaptureData
	offset := 0

	for {
		pos := bytesIndex(data[offset:], marker)
		if pos == -1 {
			break
		}

		absolutePos := offset + pos

		// Read buffer address at +0x10 and index at +0x1c
		if absolutePos+0x20 <= len(data) {
			bufAddr := uint64(data[absolutePos+0x10]) |
				uint64(data[absolutePos+0x11])<<8 |
				uint64(data[absolutePos+0x12])<<16 |
				uint64(data[absolutePos+0x13])<<24 |
				uint64(data[absolutePos+0x14])<<32 |
				uint64(data[absolutePos+0x15])<<40 |
				uint64(data[absolutePos+0x16])<<48 |
				uint64(data[absolutePos+0x17])<<56

			index := uint32(data[absolutePos+0x1c]) |
				uint32(data[absolutePos+0x1d])<<8 |
				uint32(data[absolutePos+0x1e])<<16 |
				uint32(data[absolutePos+0x1f])<<24

			bindings = append(bindings, BufferAccessBinding{
				BufferAddr: bufAddr,
				Index:      int(index),
				Offset:     int64(absolutePos),
			})
		}

		offset += pos + 6
	}

	return bindings, nil
}

// buildBufferAddressMapping builds a map from buffer addresses to buffer names.
// Returns map[address][]names (can have multiple names for same address due to aliasing).
func buildBufferAddressMapping(t *trace.Trace) (map[uint64][]string, error) {
	addrToNames := make(map[uint64][]string)

	// Parse CtU<b>ulul records from capture file
	// These records contain both buffer address and buffer name
	marker := []byte{0x43, 0x74, 0x55, 0x3c, 0x62, 0x3e, 0x75, 0x6c, 0x75, 0x6c}

	data := t.CaptureData
	offset := 0

	for {
		pos := bytesIndex(data[offset:], marker)
		if pos == -1 {
			break
		}

		absolutePos := offset + pos

		// Read buffer address at +0x14
		if absolutePos+0x1c <= len(data) {
			bufAddr := uint64(data[absolutePos+0x14]) |
				uint64(data[absolutePos+0x15])<<8 |
				uint64(data[absolutePos+0x16])<<16 |
				uint64(data[absolutePos+0x17])<<24 |
				uint64(data[absolutePos+0x18])<<32 |
				uint64(data[absolutePos+0x19])<<40 |
				uint64(data[absolutePos+0x1a])<<48 |
				uint64(data[absolutePos+0x1b])<<56

			// Read buffer name at +0x1c
			nameStart := absolutePos + 0x1c
			if nameStart < len(data) && hasPrefix(data[nameStart:], []byte("MTLBuffer-")) {
				nameEnd := indexByte(data[nameStart:], 0)
				if nameEnd > 0 && nameEnd < 100 {
					name := string(data[nameStart : nameStart+nameEnd])
					addrToNames[bufAddr] = append(addrToNames[bufAddr], name)
				}
			}
		}

		offset += pos + 10
	}

	return addrToNames, nil
}

// FormatBufferAccessReport generates a human-readable report of buffer access patterns.
func FormatBufferAccessReport(t *trace.Trace, analysis *BufferAccessAnalysis) string {
	report := "=== Buffer Access Pattern Analysis ===\n\n"

	report += fmt.Sprintf("Total Buffers: %d\n", analysis.TotalBuffers)
	report += fmt.Sprintf("Accessed: %d (%.1f%%)\n",
		analysis.AccessedCount,
		100.0*float64(analysis.AccessedCount)/float64(analysis.TotalBuffers))
	report += fmt.Sprintf("Unused: %d (%.1f%%)\n\n",
		analysis.UnusedCount,
		100.0*float64(analysis.UnusedCount)/float64(analysis.TotalBuffers))

	// Sort patterns by access count (descending)
	patterns := make([]*BufferAccessPattern, 0, len(analysis.Patterns))
	for _, p := range analysis.Patterns {
		patterns = append(patterns, p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].AccessCount > patterns[j].AccessCount
	})

	// Show most frequently accessed buffers
	report += "=== Most Frequently Accessed Buffers ===\n\n"
	for i, pattern := range patterns {
		if i >= 20 {
			report += fmt.Sprintf("... and %d more\n", len(patterns)-20)
			break
		}

		report += fmt.Sprintf("Buffer: %s (0x%x)\n", pattern.BufferName, pattern.BufferAddress)
		report += fmt.Sprintf("  Access Count: %d\n", pattern.AccessCount)
		report += fmt.Sprintf("  Accessed by %d encoder(s)\n", len(uniqueStrings(pattern.EncoderRefs)))

		// Show unique encoders
		uniqueEncoders := uniqueStrings(pattern.EncoderRefs)
		if len(uniqueEncoders) <= 5 {
			for _, encoder := range uniqueEncoders {
				count := countString(pattern.EncoderRefs, encoder)
				report += fmt.Sprintf("    - %s (%d times)\n", encoder, count)
			}
		} else {
			report += fmt.Sprintf("    - %s (and %d more)\n",
				uniqueEncoders[0], len(uniqueEncoders)-1)
		}

		if pattern.IsAliased {
			report += fmt.Sprintf("  ⚠️  Aliased with: %v\n", pattern.AliasedWith)
		}

		report += "\n"
	}

	// Show aliased buffers
	if len(analysis.AliasedBuffers) > 0 {
		report += "=== Memory Aliasing Detected ===\n\n"
		for addr, names := range analysis.AliasedBuffers {
			report += fmt.Sprintf("Address 0x%x:\n", addr)
			for _, name := range names {
				report += fmt.Sprintf("  - %s\n", name)
			}
			report += "\n"
		}
	}

	// Show unused buffers
	if len(analysis.UnusedBuffers) > 0 {
		report += "=== Unused Buffers ===\n\n"
		for i, name := range analysis.UnusedBuffers {
			if i >= 10 {
				report += fmt.Sprintf("... and %d more\n", len(analysis.UnusedBuffers)-10)
				break
			}
			report += fmt.Sprintf("  - %s\n", name)
		}
		report += "\n"
	}

	return report
}

// Helper functions

func bytesIndex(s, substr []byte) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if bytesEqual(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasPrefix(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	return bytesEqual(s[:len(prefix)], prefix)
}

func indexByte(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}

func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func countString(strs []string, target string) int {
	count := 0
	for _, s := range strs {
		if s == target {
			count++
		}
	}
	return count
}
