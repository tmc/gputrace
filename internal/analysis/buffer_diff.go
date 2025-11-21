package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// BufferSizeInfo contains buffer metadata from a trace.
type BufferSizeInfo struct {
	// Map of buffer address to metadata
	Buffers map[uint64]*BufferMetadata

	// Summary statistics
	TotalBuffers     int
	TotalMemoryBytes uint64
	TotalMemoryMB    float64
}

// BufferMetadata contains metadata about a single buffer.
type BufferMetadata struct {
	Address     uint64
	Size        uint64 // Estimated or actual size
	AccessCount int    // Number of times accessed
	EncoderIDs  []int  // Which encoders used this buffer
	FirstSeen   int    // Record index of first access
	LastSeen    int    // Record index of last access
}

// BufferDiff represents differences between two trace buffers.
type BufferDiff struct {
	// Buffer changes
	AddedBuffers   map[uint64]*BufferMetadata // Buffers in trace2 only
	RemovedBuffers map[uint64]*BufferMetadata // Buffers in trace1 only
	CommonBuffers  map[uint64]*BufferMetadata // Buffers in both traces
	ChangedBuffers map[uint64]*BufferChange   // Buffers with different sizes/usage

	// Summary statistics
	TotalAdded        int
	TotalRemoved      int
	TotalChanged      int
	TotalCommon       int
	MemoryDeltaBytes  int64 // Positive = more memory in trace2
	MemoryDeltaMB     float64
	Trace1MemoryBytes uint64
	Trace2MemoryBytes uint64
}

// BufferChange represents a change in buffer metadata between traces.
type BufferChange struct {
	Address          uint64
	SizeChange       int64 // Positive = larger in trace2
	AccessCountDelta int   // Change in access count
	Trace1           *BufferMetadata
	Trace2           *BufferMetadata
}

// ExtractBufferSizes extracts buffer information from a trace.
func ExtractBufferSizes(t *trace.Trace) (*BufferSizeInfo, error) {
	info := &BufferSizeInfo{
		Buffers: make(map[uint64]*BufferMetadata),
	}

	// Parse MTSP records
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	// Track current encoder
	encoderID := 0

	// Process each record to extract buffer information
	for recordIdx, record := range records {
		switch record.Type {
		case trace.RecordTypeCS:
			// New compute encoder
			encoderID++

		case trace.RecordTypeCt:
			// Parse Ct record to get buffer bindings
			ct, err := record.ParseCtRecord()
			if err != nil {
				continue
			}

			// Track each buffer
			for _, bufferAddr := range ct.BufferBindings {
				if bufferAddr == 0 {
					continue
				}

				// Get or create buffer metadata
				metadata, exists := info.Buffers[bufferAddr]
				if !exists {
					metadata = &BufferMetadata{
						Address:    bufferAddr,
						FirstSeen:  recordIdx,
						EncoderIDs: []int{},
					}
					info.Buffers[bufferAddr] = metadata
				}

				// Update metadata
				metadata.AccessCount++
				metadata.LastSeen = recordIdx

				// Track encoder access
				if !containsInt(metadata.EncoderIDs, encoderID) {
					metadata.EncoderIDs = append(metadata.EncoderIDs, encoderID)
				}
			}
		}
	}

	// Compute summary statistics
	info.TotalBuffers = len(info.Buffers)

	// Estimate total memory (we don't have actual sizes, so this is approximate)
	// In a real implementation, this would parse buffer metadata files
	for _, buf := range info.Buffers {
		// Estimate: use a heuristic based on access patterns
		// This is just a placeholder - real sizes would come from trace metadata
		if buf.Size == 0 {
			// Heuristic: buffers accessed more frequently might be larger
			// This is a rough estimate for demonstration
			buf.Size = uint64(buf.AccessCount * 1024) // Very rough estimate
		}
		info.TotalMemoryBytes += buf.Size
	}

	info.TotalMemoryMB = float64(info.TotalMemoryBytes) / (1024 * 1024)

	return info, nil
}

// CompareBuffers compares buffer usage between two traces.
func CompareBuffers(info1, info2 *BufferSizeInfo) *BufferDiff {
	diff := &BufferDiff{
		AddedBuffers:   make(map[uint64]*BufferMetadata),
		RemovedBuffers: make(map[uint64]*BufferMetadata),
		CommonBuffers:  make(map[uint64]*BufferMetadata),
		ChangedBuffers: make(map[uint64]*BufferChange),
	}

	// Find added, removed, and common buffers
	for addr, buf1 := range info1.Buffers {
		if buf2, exists := info2.Buffers[addr]; exists {
			// Buffer exists in both traces
			diff.CommonBuffers[addr] = buf1

			// Check for changes
			if buf1.Size != buf2.Size || buf1.AccessCount != buf2.AccessCount {
				diff.ChangedBuffers[addr] = &BufferChange{
					Address:          addr,
					SizeChange:       int64(buf2.Size) - int64(buf1.Size),
					AccessCountDelta: buf2.AccessCount - buf1.AccessCount,
					Trace1:           buf1,
					Trace2:           buf2,
				}
				diff.TotalChanged++
			}
		} else {
			// Buffer only in trace1 (removed)
			diff.RemovedBuffers[addr] = buf1
			diff.TotalRemoved++
		}
	}

	// Find buffers only in trace2 (added)
	for addr, buf2 := range info2.Buffers {
		if _, exists := info1.Buffers[addr]; !exists {
			diff.AddedBuffers[addr] = buf2
			diff.TotalAdded++
		}
	}

	diff.TotalCommon = len(diff.CommonBuffers)

	// Calculate memory delta
	diff.Trace1MemoryBytes = info1.TotalMemoryBytes
	diff.Trace2MemoryBytes = info2.TotalMemoryBytes
	diff.MemoryDeltaBytes = int64(info2.TotalMemoryBytes) - int64(info1.TotalMemoryBytes)
	diff.MemoryDeltaMB = float64(diff.MemoryDeltaBytes) / (1024 * 1024)

	return diff
}

// FormatBufferDiff generates a human-readable diff report.
func FormatBufferDiff(diff *BufferDiff, trace1Path, trace2Path string) string {
	var out strings.Builder

	out.WriteString("=== Buffer Diff Analysis ===\n\n")

	// Trace paths
	out.WriteString(fmt.Sprintf("Trace 1: %s\n", trace1Path))
	out.WriteString(fmt.Sprintf("Trace 2: %s\n\n", trace2Path))

	// Summary statistics
	out.WriteString("Summary:\n")
	out.WriteString(fmt.Sprintf("  Common Buffers:   %d\n", diff.TotalCommon))
	out.WriteString(fmt.Sprintf("  Added Buffers:    %d (new in trace2)\n", diff.TotalAdded))
	out.WriteString(fmt.Sprintf("  Removed Buffers:  %d (removed from trace1)\n", diff.TotalRemoved))
	out.WriteString(fmt.Sprintf("  Changed Buffers:  %d (size or usage changed)\n", diff.TotalChanged))
	out.WriteString("\n")

	// Memory statistics
	out.WriteString("Memory Usage:\n")
	out.WriteString(fmt.Sprintf("  Trace 1: %.2f MB (%d bytes)\n",
		float64(diff.Trace1MemoryBytes)/(1024*1024), diff.Trace1MemoryBytes))
	out.WriteString(fmt.Sprintf("  Trace 2: %.2f MB (%d bytes)\n",
		float64(diff.Trace2MemoryBytes)/(1024*1024), diff.Trace2MemoryBytes))

	deltaSign := ""
	if diff.MemoryDeltaBytes > 0 {
		deltaSign = "+"
	}
	out.WriteString(fmt.Sprintf("  Delta:   %s%.2f MB (%s%d bytes)\n",
		deltaSign, diff.MemoryDeltaMB, deltaSign, diff.MemoryDeltaBytes))
	out.WriteString("\n")

	// Show added buffers (if any)
	if diff.TotalAdded > 0 {
		out.WriteString(fmt.Sprintf("Added Buffers (%d new in trace2):\n", diff.TotalAdded))

		// Sort by access count (most accessed first)
		var addedList []*BufferMetadata
		for _, buf := range diff.AddedBuffers {
			addedList = append(addedList, buf)
		}
		sort.Slice(addedList, func(i, j int) bool {
			return addedList[i].AccessCount > addedList[j].AccessCount
		})

		// Show top 10
		limit := 10
		if len(addedList) < limit {
			limit = len(addedList)
		}
		for i := 0; i < limit; i++ {
			buf := addedList[i]
			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %d bytes, %d accesses, %d encoders\n",
				i+1, buf.Address, buf.Size, buf.AccessCount, len(buf.EncoderIDs)))
		}
		if len(addedList) > limit {
			out.WriteString(fmt.Sprintf("  ... and %d more added buffers\n", len(addedList)-limit))
		}
		out.WriteString("\n")
	}

	// Show removed buffers (if any)
	if diff.TotalRemoved > 0 {
		out.WriteString(fmt.Sprintf("Removed Buffers (%d removed from trace1):\n", diff.TotalRemoved))

		// Sort by access count
		var removedList []*BufferMetadata
		for _, buf := range diff.RemovedBuffers {
			removedList = append(removedList, buf)
		}
		sort.Slice(removedList, func(i, j int) bool {
			return removedList[i].AccessCount > removedList[j].AccessCount
		})

		// Show top 10
		limit := 10
		if len(removedList) < limit {
			limit = len(removedList)
		}
		for i := 0; i < limit; i++ {
			buf := removedList[i]
			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %d bytes, %d accesses, %d encoders\n",
				i+1, buf.Address, buf.Size, buf.AccessCount, len(buf.EncoderIDs)))
		}
		if len(removedList) > limit {
			out.WriteString(fmt.Sprintf("  ... and %d more removed buffers\n", len(removedList)-limit))
		}
		out.WriteString("\n")
	}

	// Show changed buffers (if any)
	if diff.TotalChanged > 0 {
		out.WriteString(fmt.Sprintf("Changed Buffers (%d with size or usage changes):\n", diff.TotalChanged))

		// Sort by absolute size change
		var changedList []*BufferChange
		for _, change := range diff.ChangedBuffers {
			changedList = append(changedList, change)
		}
		sort.Slice(changedList, func(i, j int) bool {
			absI := abs(changedList[i].SizeChange)
			absJ := abs(changedList[j].SizeChange)
			return absI > absJ
		})

		// Show top 10
		limit := 10
		if len(changedList) < limit {
			limit = len(changedList)
		}
		for i := 0; i < limit; i++ {
			change := changedList[i]
			sizeSign := ""
			if change.SizeChange > 0 {
				sizeSign = "+"
			}
			accessSign := ""
			if change.AccessCountDelta > 0 {
				accessSign = "+"
			}

			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %s%d bytes, %s%d accesses\n",
				i+1, change.Address, sizeSign, change.SizeChange,
				accessSign, change.AccessCountDelta))
		}
		if len(changedList) > limit {
			out.WriteString(fmt.Sprintf("  ... and %d more changed buffers\n", len(changedList)-limit))
		}
		out.WriteString("\n")
	}

	// Optimization insights
	out.WriteString("Insights:\n")
	if diff.MemoryDeltaBytes > 0 {
		out.WriteString(fmt.Sprintf("  • Memory usage increased by %.2f MB\n", diff.MemoryDeltaMB))
		if diff.TotalAdded > 0 {
			out.WriteString(fmt.Sprintf("    %d new buffers contribute to the increase\n", diff.TotalAdded))
		}
		if diff.TotalChanged > 0 {
			out.WriteString(fmt.Sprintf("    %d buffers changed in size or usage\n", diff.TotalChanged))
		}
	} else if diff.MemoryDeltaBytes < 0 {
		out.WriteString(fmt.Sprintf("  • Memory usage decreased by %.2f MB (optimization!)\n", -diff.MemoryDeltaMB))
		if diff.TotalRemoved > 0 {
			out.WriteString(fmt.Sprintf("    %d buffers were eliminated\n", diff.TotalRemoved))
		}
	} else {
		out.WriteString("  • Memory usage unchanged\n")
	}

	if diff.TotalAdded == 0 && diff.TotalRemoved == 0 && diff.TotalChanged == 0 {
		out.WriteString("  • No buffer changes detected - traces are identical\n")
	}

	return out.String()
}

// Helper function to get absolute value
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
