package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// BufferTimelineAnalysis contains buffer lifecycle timeline analysis.
type BufferTimelineAnalysis struct {
	// Timeline events for each buffer
	BufferEvents map[uint64]*BufferLifecycle

	// Summary statistics
	TotalBuffers     int
	PeakMemoryBytes  uint64
	PeakMemoryMB     float64
	TotalAllocations int
	AverageLifetime  float64

	// Timeline bounds (in record indices)
	MinRecordIndex int
	MaxRecordIndex int
}

// BufferLifecycle tracks the lifecycle of a single buffer.
type BufferLifecycle struct {
	Address       uint64
	FirstSeen     int      // Record index of first access
	LastSeen      int      // Record index of last access
	AccessCount   int      // Number of times accessed
	EncoderIDs    []int    // Which encoders accessed this buffer
	AccessIndices []int    // Record indices where buffer was accessed
	IsActive      bool     // Currently active in timeline
	Size          uint64   // Buffer size if available
}

// BufferTimelineEvent represents a point in time event for a buffer.
type BufferTimelineEvent struct {
	RecordIndex int
	BufferAddr  uint64
	EncoderID   int
	EventType   string // "access", "first_access", "last_access"
}

// ExtractBufferTimeline analyzes buffer usage patterns over time from the trace.
func ExtractBufferTimeline(t *trace.Trace) (*BufferTimelineAnalysis, error) {
	analysis := &BufferTimelineAnalysis{
		BufferEvents: make(map[uint64]*BufferLifecycle),
	}

	// Parse MTSP records to extract buffer access timeline
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	// Track current encoder ID (increments on CS records)
	encoderID := 0

	// Process each record to build timeline
	for recordIdx, record := range records {
		// Update timeline bounds
		if analysis.MinRecordIndex == 0 || recordIdx < analysis.MinRecordIndex {
			analysis.MinRecordIndex = recordIdx
		}
		if recordIdx > analysis.MaxRecordIndex {
			analysis.MaxRecordIndex = recordIdx
		}

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

			// Track each buffer access
			for _, bufferAddr := range ct.BufferBindings {
				if bufferAddr == 0 {
					continue
				}

				// Get or create buffer lifecycle
				lifecycle, exists := analysis.BufferEvents[bufferAddr]
				if !exists {
					lifecycle = &BufferLifecycle{
						Address:       bufferAddr,
						FirstSeen:     recordIdx,
						EncoderIDs:    []int{},
						AccessIndices: []int{},
						IsActive:      true,
					}
					analysis.BufferEvents[bufferAddr] = lifecycle
					analysis.TotalAllocations++
				}

				// Update lifecycle
				lifecycle.AccessCount++
				lifecycle.LastSeen = recordIdx
				lifecycle.AccessIndices = append(lifecycle.AccessIndices, recordIdx)

				// Track encoder access
				if !containsInt(lifecycle.EncoderIDs, encoderID) {
					lifecycle.EncoderIDs = append(lifecycle.EncoderIDs, encoderID)
				}
			}

		case trace.RecordTypeCul, trace.RecordTypeCulul:
			// These records may also contain buffer references
			// For now, focus on Ct records which have structured buffer bindings
		}
	}

	// Compute summary statistics
	analysis.computeStatistics()

	return analysis, nil
}

// computeStatistics calculates summary statistics from timeline data.
func (analysis *BufferTimelineAnalysis) computeStatistics() {
	analysis.TotalBuffers = len(analysis.BufferEvents)

	if analysis.TotalBuffers == 0 {
		return
	}

	// Calculate average lifetime (in record indices)
	var totalLifetime int
	var maxConcurrentBytes uint64

	for _, lifecycle := range analysis.BufferEvents {
		lifetime := lifecycle.LastSeen - lifecycle.FirstSeen
		totalLifetime += lifetime

		// Estimate buffer size (this would ideally come from buffer metadata)
		// For now, we assume buffers are active and count their presence
		if lifecycle.Size > 0 {
			maxConcurrentBytes += lifecycle.Size
		}
	}

	if analysis.TotalBuffers > 0 {
		analysis.AverageLifetime = float64(totalLifetime) / float64(analysis.TotalBuffers)
	}

	// Peak memory is approximate without actual buffer sizes
	analysis.PeakMemoryBytes = maxConcurrentBytes
	analysis.PeakMemoryMB = float64(maxConcurrentBytes) / (1024 * 1024)
}

// FormatBufferTimelineASCII generates an ASCII timeline visualization.
func FormatBufferTimelineASCII(analysis *BufferTimelineAnalysis, width int) string {
	var out strings.Builder

	out.WriteString("=== Buffer Timeline ===\n\n")

	// Summary statistics
	out.WriteString("Summary:\n")
	out.WriteString(fmt.Sprintf("  Total Buffers:      %d\n", analysis.TotalBuffers))
	out.WriteString(fmt.Sprintf("  Total Allocations:  %d\n", analysis.TotalAllocations))
	out.WriteString(fmt.Sprintf("  Average Lifetime:   %.1f records\n", analysis.AverageLifetime))
	out.WriteString(fmt.Sprintf("  Timeline Range:     %d - %d records\n",
		analysis.MinRecordIndex, analysis.MaxRecordIndex))
	if analysis.PeakMemoryBytes > 0 {
		out.WriteString(fmt.Sprintf("  Peak Memory:        %.2f MB\n", analysis.PeakMemoryMB))
	}
	out.WriteString("\n")

	// Get buffers sorted by first access time
	var lifecycles []*BufferLifecycle
	for _, lifecycle := range analysis.BufferEvents {
		lifecycles = append(lifecycles, lifecycle)
	}
	sort.Slice(lifecycles, func(i, j int) bool {
		return lifecycles[i].FirstSeen < lifecycles[j].FirstSeen
	})

	// Limit display to top 20 buffers for readability
	displayLimit := 20
	if len(lifecycles) < displayLimit {
		displayLimit = len(lifecycles)
	}

	out.WriteString(fmt.Sprintf("Buffer Activity (showing %d of %d buffers):\n\n", displayLimit, len(lifecycles)))

	// Calculate timeline scale
	timelineRange := analysis.MaxRecordIndex - analysis.MinRecordIndex
	if timelineRange == 0 {
		timelineRange = 1
	}

	// Header with record index markers
	out.WriteString("Buffer Address      ")
	markerWidth := width - 20
	for i := 0; i <= 10; i++ {
		pos := (i * markerWidth) / 10
		out.WriteString(fmt.Sprintf("%*d", pos-len(fmt.Sprintf("%d", analysis.MinRecordIndex+i*(timelineRange/10))),
			analysis.MinRecordIndex+i*(timelineRange/10)))
	}
	out.WriteString("\n")

	// Draw timeline for each buffer
	for i := 0; i < displayLimit; i++ {
		lifecycle := lifecycles[i]

		// Buffer address
		out.WriteString(fmt.Sprintf("0x%016x ", lifecycle.Address))

		// Draw timeline bar
		timeline := drawTimelineBar(lifecycle, analysis.MinRecordIndex, analysis.MaxRecordIndex, width-20)
		out.WriteString(timeline)

		// Stats
		out.WriteString(fmt.Sprintf(" (%d accesses, %d encoders)\n",
			lifecycle.AccessCount, len(lifecycle.EncoderIDs)))
	}

	if len(lifecycles) > displayLimit {
		out.WriteString(fmt.Sprintf("\n... and %d more buffers\n", len(lifecycles)-displayLimit))
	}

	return out.String()
}

// drawTimelineBar creates an ASCII bar showing buffer lifetime.
func drawTimelineBar(lifecycle *BufferLifecycle, minIndex, maxIndex, width int) string {
	timelineRange := maxIndex - minIndex
	if timelineRange == 0 {
		timelineRange = 1
	}

	bar := make([]rune, width)
	for i := range bar {
		bar[i] = ' '
	}

	// Mark each access point
	for _, accessIdx := range lifecycle.AccessIndices {
		// Calculate position in bar
		relativePos := accessIdx - minIndex
		barPos := (relativePos * width) / timelineRange

		if barPos >= 0 && barPos < width {
			// Use different characters for different access densities
			if bar[barPos] == ' ' {
				bar[barPos] = '.'
			} else if bar[barPos] == '.' {
				bar[barPos] = '|'
			} else if bar[barPos] == '|' {
				bar[barPos] = '#'
			}
		}
	}

	// Draw continuous bar from first to last access
	startPos := ((lifecycle.FirstSeen - minIndex) * width) / timelineRange
	endPos := ((lifecycle.LastSeen - minIndex) * width) / timelineRange

	if startPos < 0 {
		startPos = 0
	}
	if endPos >= width {
		endPos = width - 1
	}

	for i := startPos; i <= endPos; i++ {
		if bar[i] == ' ' {
			bar[i] = '-'
		}
	}

	// Mark start and end
	if startPos >= 0 && startPos < width {
		bar[startPos] = '['
	}
	if endPos >= 0 && endPos < width {
		bar[endPos] = ']'
	}

	return string(bar)
}

// FormatBufferTimelineSummary generates a text summary of buffer timeline.
func FormatBufferTimelineSummary(analysis *BufferTimelineAnalysis) string {
	var out strings.Builder

	out.WriteString("=== Buffer Timeline Summary ===\n\n")

	// Overall statistics
	out.WriteString("Overall Statistics:\n")
	out.WriteString(fmt.Sprintf("  Total Unique Buffers:  %d\n", analysis.TotalBuffers))
	out.WriteString(fmt.Sprintf("  Total Allocations:     %d\n", analysis.TotalAllocations))
	out.WriteString(fmt.Sprintf("  Average Lifetime:      %.1f records\n", analysis.AverageLifetime))
	out.WriteString(fmt.Sprintf("  Timeline Range:        %d - %d records (span: %d)\n",
		analysis.MinRecordIndex, analysis.MaxRecordIndex,
		analysis.MaxRecordIndex-analysis.MinRecordIndex))
	out.WriteString("\n")

	// Top longest-lived buffers
	var lifecycles []*BufferLifecycle
	for _, lifecycle := range analysis.BufferEvents {
		lifecycles = append(lifecycles, lifecycle)
	}

	// Sort by lifetime
	sort.Slice(lifecycles, func(i, j int) bool {
		lifetimeI := lifecycles[i].LastSeen - lifecycles[i].FirstSeen
		lifetimeJ := lifecycles[j].LastSeen - lifecycles[j].FirstSeen
		return lifetimeI > lifetimeJ
	})

	out.WriteString("Top 10 Longest-Lived Buffers:\n")
	limit := 10
	if len(lifecycles) < limit {
		limit = len(lifecycles)
	}
	for i := 0; i < limit; i++ {
		lifecycle := lifecycles[i]
		lifetime := lifecycle.LastSeen - lifecycle.FirstSeen
		out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %d records (%d accesses, %d encoders)\n",
			i+1, lifecycle.Address, lifetime, lifecycle.AccessCount, len(lifecycle.EncoderIDs)))
	}
	out.WriteString("\n")

	// Most frequently accessed buffers
	sort.Slice(lifecycles, func(i, j int) bool {
		return lifecycles[i].AccessCount > lifecycles[j].AccessCount
	})

	out.WriteString("Top 10 Most Frequently Accessed Buffers:\n")
	for i := 0; i < limit; i++ {
		lifecycle := lifecycles[i]
		out.WriteString(fmt.Sprintf("  [%d] 0x%016x: %d accesses (%d encoders)\n",
			i+1, lifecycle.Address, lifecycle.AccessCount, len(lifecycle.EncoderIDs)))
	}
	out.WriteString("\n")

	// Optimization insights
	out.WriteString("Optimization Insights:\n")

	// Find short-lived buffers that could be pooled
	var shortLived int
	for _, lifecycle := range lifecycles {
		lifetime := lifecycle.LastSeen - lifecycle.FirstSeen
		if lifetime < 10 && lifecycle.AccessCount < 5 {
			shortLived++
		}
	}
	if shortLived > 0 {
		out.WriteString(fmt.Sprintf("  • %d short-lived buffers detected\n", shortLived))
		out.WriteString("    Consider buffer pooling to reduce allocation overhead\n")
	}

	// Find buffers with single access
	var singleAccess int
	for _, lifecycle := range lifecycles {
		if lifecycle.AccessCount == 1 {
			singleAccess++
		}
	}
	if singleAccess > 0 {
		out.WriteString(fmt.Sprintf("  • %d buffers accessed only once\n", singleAccess))
		out.WriteString("    These might be temporary buffers that could be eliminated\n")
	}

	// Find long-lived buffers
	var longLived int
	avgLifetime := int(analysis.AverageLifetime)
	for _, lifecycle := range lifecycles {
		lifetime := lifecycle.LastSeen - lifecycle.FirstSeen
		if lifetime > avgLifetime*3 {
			longLived++
		}
	}
	if longLived > 0 {
		out.WriteString(fmt.Sprintf("  • %d long-lived buffers detected\n", longLived))
		out.WriteString("    Review if these can be released earlier to reduce memory pressure\n")
	}

	if shortLived == 0 && singleAccess == 0 && longLived == 0 {
		out.WriteString("  • No obvious optimization opportunities detected\n")
		out.WriteString("    Buffer lifecycle patterns appear well-optimized\n")
	}

	return out.String()
}
