package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// BufferSizeInfo contains buffer metadata from a trace.
type BufferSizeInfo struct {
	// Map of buffer address to metadata
	Buffers map[uint64]*BufferMetadata

	// Summary statistics
	TotalBuffers       int
	TotalMemoryBytes   uint64
	TotalMemoryMB      float64
	KnownSizeBuffers   int
	UnknownSizeBuffers int
}

// BufferMetadata contains metadata about a single buffer.
type BufferMetadata struct {
	Address     uint64
	Size        uint64 // Actual size when SizeKnown is true
	SizeKnown   bool
	SizeSource  string
	AccessCount int   // Number of times accessed
	EncoderIDs  []int // Which encoders used this buffer
	FirstSeen   int   // Record index of first access
	LastSeen    int   // Record index of last access
}

// BufferDiff represents differences between two trace buffers.
type BufferDiff struct {
	// Buffer changes
	AddedBuffers   map[uint64]*BufferMetadata // Buffers in trace2 only
	RemovedBuffers map[uint64]*BufferMetadata // Buffers in trace1 only
	CommonBuffers  map[uint64]*BufferMetadata // Buffers in both traces
	ChangedBuffers map[uint64]*BufferChange   // Buffers with different sizes/usage
	SizeBins       []BufferSizeBinDelta       // Cross-capture size-class changes

	// Summary statistics
	TotalAdded               int
	TotalRemoved             int
	TotalChanged             int
	TotalCommon              int
	MemoryDeltaBytes         int64 // Positive = more memory in trace2
	MemoryDeltaMB            float64
	Trace1MemoryBytes        uint64
	Trace2MemoryBytes        uint64
	Trace1KnownSizeBuffers   int
	Trace1UnknownSizeBuffers int
	Trace2KnownSizeBuffers   int
	Trace2UnknownSizeBuffers int
	SizeMetadataComplete     bool
}

// BufferChange represents a change in buffer metadata between traces.
type BufferChange struct {
	Address          uint64
	SizeChange       int64 // Positive = larger in trace2
	SizeChangeKnown  bool
	AccessCountDelta int // Change in access count
	Trace1           *BufferMetadata
	Trace2           *BufferMetadata
}

type bufferSizeMetadata struct {
	Size   uint64
	Source string
}

const bufferSizeSourceTraceMetadata = "trace metadata"

// BufferSizeBinDelta is the change for one buffer size class.
type BufferSizeBinDelta struct {
	Size        uint64
	Trace1Count int
	Trace2Count int
	CountDelta  int
	ByteDelta   int64
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

	bufferSizes := extractTraceBufferSizeMetadata(t, records)

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
					if sizeMetadata, ok := bufferSizes[bufferAddr]; ok {
						metadata.Size = sizeMetadata.Size
						metadata.SizeKnown = true
						metadata.SizeSource = sizeMetadata.Source
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

	for _, buf := range info.Buffers {
		if buf.SizeKnown {
			info.KnownSizeBuffers++
			info.TotalMemoryBytes += buf.Size
		} else {
			info.UnknownSizeBuffers++
		}
	}

	info.TotalMemoryMB = float64(info.TotalMemoryBytes) / (1024 * 1024)

	return info, nil
}

func extractTraceBufferSizeMetadata(t *trace.Trace, records []trace.MTSPRecord) map[uint64]bufferSizeMetadata {
	nameSizes := loadMTLBufferFileSizes(t.Path)
	if len(nameSizes) == 0 {
		return nil
	}

	bufferSizes := make(map[uint64]bufferSizeMetadata)
	for _, record := range records {
		if record.Type != trace.RecordTypeCtU {
			continue
		}

		ctu, err := record.ParseCtURecord()
		if err != nil || ctu.Address == 0 || ctu.Name == "" {
			continue
		}

		size, ok := mtlBufferSizeForName(ctu.Name, nameSizes)
		if !ok {
			continue
		}

		bufferSizes[ctu.Address] = bufferSizeMetadata{
			Size:   size,
			Source: bufferSizeSourceTraceMetadata,
		}
	}

	return bufferSizes
}

func loadMTLBufferFileSizes(tracePath string) map[string]uint64 {
	if tracePath == "" {
		return nil
	}

	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return nil
	}

	sizes := make(map[string]uint64)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "MTLBuffer-") {
			continue
		}

		info, err := os.Stat(filepath.Join(tracePath, name))
		if err != nil || info.IsDir() {
			continue
		}

		sizes[name] = uint64(info.Size())
	}

	return sizes
}

func mtlBufferSizeForName(name string, sizes map[string]uint64) (uint64, bool) {
	if size, ok := sizes[name]; ok {
		return size, true
	}

	baseName := baseMTLBufferName(name)
	if baseName == "" {
		return 0, false
	}

	size, ok := sizes[baseName]
	return size, ok
}

func baseMTLBufferName(name string) string {
	if !strings.HasPrefix(name, "MTLBuffer-") {
		return ""
	}

	parts := strings.Split(name, "-")
	if len(parts) < 3 || parts[1] == "" {
		return ""
	}

	return fmt.Sprintf("MTLBuffer-%s-0", parts[1])
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
			sizeChanged := buf1.SizeKnown && buf2.SizeKnown && buf1.Size != buf2.Size
			accessCountChanged := buf1.AccessCount != buf2.AccessCount
			if sizeChanged || accessCountChanged {
				diff.ChangedBuffers[addr] = &BufferChange{
					Address:          addr,
					SizeChange:       int64(buf2.Size) - int64(buf1.Size),
					SizeChangeKnown:  buf1.SizeKnown && buf2.SizeKnown,
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
	diff.Trace1KnownSizeBuffers = info1.KnownSizeBuffers
	diff.Trace1UnknownSizeBuffers = info1.UnknownSizeBuffers
	diff.Trace2KnownSizeBuffers = info2.KnownSizeBuffers
	diff.Trace2UnknownSizeBuffers = info2.UnknownSizeBuffers
	diff.SizeMetadataComplete = info1.UnknownSizeBuffers == 0 && info2.UnknownSizeBuffers == 0
	diff.SizeBins = compareBufferSizeBins(info1, info2)

	return diff
}

func compareBufferSizeBins(info1, info2 *BufferSizeInfo) []BufferSizeBinDelta {
	counts1 := bufferSizeCounts(info1)
	counts2 := bufferSizeCounts(info2)
	seen := make(map[uint64]struct{}, len(counts1)+len(counts2))
	for size := range counts1 {
		seen[size] = struct{}{}
	}
	for size := range counts2 {
		seen[size] = struct{}{}
	}

	bins := make([]BufferSizeBinDelta, 0, len(seen))
	for size := range seen {
		count1 := counts1[size]
		count2 := counts2[size]
		delta := count2 - count1
		if delta == 0 {
			continue
		}
		bins = append(bins, BufferSizeBinDelta{
			Size:        size,
			Trace1Count: count1,
			Trace2Count: count2,
			CountDelta:  delta,
			ByteDelta:   int64(delta) * int64(size),
		})
	}

	sort.Slice(bins, func(i, j int) bool {
		absI := abs(bins[i].ByteDelta)
		absJ := abs(bins[j].ByteDelta)
		if absI != absJ {
			return absI > absJ
		}
		return bins[i].Size > bins[j].Size
	})
	return bins
}

func bufferSizeCounts(info *BufferSizeInfo) map[uint64]int {
	counts := make(map[uint64]int)
	for _, buf := range info.Buffers {
		counts[buf.Size]++
	}
	return counts
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

	out.WriteString("Buffer Size Metadata:\n")
	out.WriteString(fmt.Sprintf("  Trace 1: %d known, %d unknown\n",
		diff.Trace1KnownSizeBuffers, diff.Trace1UnknownSizeBuffers))
	out.WriteString(fmt.Sprintf("  Trace 2: %d known, %d unknown\n",
		diff.Trace2KnownSizeBuffers, diff.Trace2UnknownSizeBuffers))
	if !diff.SizeMetadataComplete {
		out.WriteString("  Status: incomplete; memory totals include only buffers with trace metadata\n")
	}
	out.WriteString("\n")

	// Memory statistics
	if diff.SizeMetadataComplete {
		out.WriteString("Memory Usage:\n")
	} else {
		out.WriteString("Known Memory Usage:\n")
	}
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

	// Size bins are more stable than buffer IDs across independently captured traces.
	if len(diff.SizeBins) > 0 {
		out.WriteString(fmt.Sprintf("Top Size-Class Deltas (%d changed size classes):\n", len(diff.SizeBins)))
		limit := 10
		if len(diff.SizeBins) < limit {
			limit = len(diff.SizeBins)
		}
		for i := 0; i < limit; i++ {
			bin := diff.SizeBins[i]
			countSign := ""
			if bin.CountDelta > 0 {
				countSign = "+"
			}
			byteSign := ""
			if bin.ByteDelta > 0 {
				byteSign = "+"
			}
			out.WriteString(fmt.Sprintf("  [%d] size %d bytes: %d -> %d (%s%d), %s%d bytes\n",
				i+1, bin.Size, bin.Trace1Count, bin.Trace2Count,
				countSign, bin.CountDelta,
				byteSign, bin.ByteDelta))
		}
		if len(diff.SizeBins) > limit {
			out.WriteString(fmt.Sprintf("  ... and %d more size classes\n", len(diff.SizeBins)-limit))
		}
		out.WriteString("\n")
	}

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
			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %s, %d accesses, %d encoders\n",
				i+1, buf.Address, formatBufferSize(buf), buf.AccessCount, len(buf.EncoderIDs)))
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
			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %s, %d accesses, %d encoders\n",
				i+1, buf.Address, formatBufferSize(buf), buf.AccessCount, len(buf.EncoderIDs)))
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
			if changedList[i].SizeChangeKnown != changedList[j].SizeChangeKnown {
				return changedList[i].SizeChangeKnown
			}
			absI := abs(changedList[i].SizeChange)
			absJ := abs(changedList[j].SizeChange)
			if absI != absJ {
				return absI > absJ
			}
			return absInt(changedList[i].AccessCountDelta) > absInt(changedList[j].AccessCountDelta)
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

			out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %s, %s%d accesses\n",
				i+1, change.Address, formatSizeChange(change, sizeSign),
				accessSign, change.AccessCountDelta))
		}
		if len(changedList) > limit {
			out.WriteString(fmt.Sprintf("  ... and %d more changed buffers\n", len(changedList)-limit))
		}
		out.WriteString("\n")
	}

	// Optimization insights
	out.WriteString("Insights:\n")
	if !diff.SizeMetadataComplete {
		out.WriteString("  • Buffer size metadata is incomplete; memory-change insights are unavailable\n")
	} else if diff.MemoryDeltaBytes > 0 {
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

func formatBufferSize(buf *BufferMetadata) string {
	if buf == nil || !buf.SizeKnown {
		return "size unknown"
	}

	return fmt.Sprintf("%d bytes", buf.Size)
}

func formatSizeChange(change *BufferChange, sign string) string {
	if change == nil || !change.SizeChangeKnown {
		return "size unknown"
	}

	return fmt.Sprintf("%s%d bytes", sign, change.SizeChange)
}

// Helper function to get absolute value
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
