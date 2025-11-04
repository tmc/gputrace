package buffer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/timing"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases
var NewTimingMetricsExtractor = timing.NewTimingMetricsExtractor

// BufferEvent represents a buffer lifecycle event.
type BufferEvent struct {
	BufferName string
	EventType  string // "allocate", "bind", "use", "deallocate"
	Timestamp  uint64
	Encoder    string // Which encoder is using the buffer
	Index      int    // Binding index for "bind" events
	Size       uint64 // Buffer size for "allocate" events
}

// BufferLifetime represents the complete lifetime of a buffer.
type BufferLifetime struct {
	BufferName    string
	BufferID      string
	Size          uint64
	AllocTime     uint64
	DeallocTime   uint64
	FirstUse      uint64
	LastUse       uint64
	UsageCount    int
	Encoders      []string // List of encoders that used this buffer
	BindingEvents []BufferEvent
	Active        bool // Still active at trace end
}

// BufferTimeline contains all buffer lifecycle information.
type BufferTimeline struct {
	Buffers      []*BufferLifetime
	StartTime    uint64
	EndTime      uint64
	Duration     uint64
	TotalBuffers int
	PeakBuffers  int    // Maximum concurrent buffers
	TotalMemory  uint64 // Total memory allocated
	PeakMemory   uint64 // Peak memory usage
	Events       []BufferEvent
}

// ExtractBufferTimeline analyzes buffer allocations and usage across the trace.
func ExtractBufferTimeline(t *trace.Trace) (*BufferTimeline, error) {
	timeline := &BufferTimeline{
		Buffers: make([]*BufferLifetime, 0),
		Events:  make([]BufferEvent, 0),
	}

	// Get buffer information from filesystem
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace directory: %w", err)
	}

	// Build buffer map with sizes
	bufferMap := make(map[string]*BufferLifetime)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "MTLBuffer-") {
			continue
		}

		// Skip symlinks
		fullPath := filepath.Join(t.Path, name)
		info, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		// Extract buffer ID
		parts := strings.TrimPrefix(name, "MTLBuffer-")
		idEnd := strings.Index(parts, "-")
		if idEnd == -1 {
			continue
		}
		bufferID := parts[:idEnd]

		// Get size
		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		lifetime := &BufferLifetime{
			BufferName:    name,
			BufferID:      bufferID,
			Size:          uint64(stat.Size()),
			Encoders:      make([]string, 0),
			BindingEvents: make([]BufferEvent, 0),
			Active:        true, // Assume still active
		}
		bufferMap[bufferID] = lifetime
	}

	// Get timing information for timeline bounds
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err == nil && len(metrics.EncoderTimings) > 0 {
		timeline.StartTime = metrics.EncoderTimings[0].StartTimestamp
		timeline.EndTime = metrics.EncoderTimings[0].EndTimestamp

		for _, encoder := range metrics.EncoderTimings {
			if encoder.StartTimestamp < timeline.StartTime {
				timeline.StartTime = encoder.StartTimestamp
			}
			if encoder.EndTimestamp > timeline.EndTime {
				timeline.EndTime = encoder.EndTimestamp
			}
		}
	}
	timeline.Duration = timeline.EndTime - timeline.StartTime

	// Parse buffer bindings to get usage events
	if err := populateBufferUsage(t, bufferMap, &timeline.Events); err != nil {
		// Not fatal - continue with what we have
	}

	// Convert map to slice and calculate statistics
	for _, lifetime := range bufferMap {
		// Set allocation time to first usage or trace start
		if lifetime.FirstUse > 0 {
			lifetime.AllocTime = lifetime.FirstUse
		} else {
			lifetime.AllocTime = timeline.StartTime
		}

		// Set deallocation to last usage or trace end
		if lifetime.LastUse > 0 {
			lifetime.DeallocTime = lifetime.LastUse
		} else {
			lifetime.DeallocTime = timeline.EndTime
		}

		timeline.Buffers = append(timeline.Buffers, lifetime)
		timeline.TotalMemory += lifetime.Size
	}

	// Sort by allocation time
	sort.Slice(timeline.Buffers, func(i, j int) bool {
		return timeline.Buffers[i].AllocTime < timeline.Buffers[j].AllocTime
	})

	// Calculate peak memory usage
	timeline.PeakMemory = calculatePeakMemory(timeline.Buffers)
	timeline.TotalBuffers = len(timeline.Buffers)

	// Sort events by timestamp
	sort.Slice(timeline.Events, func(i, j int) bool {
		return timeline.Events[i].Timestamp < timeline.Events[j].Timestamp
	})

	return timeline, nil
}

// populateBufferUsage fills in buffer usage information from bindings.
func populateBufferUsage(t *trace.Trace, bufferMap map[string]*BufferLifetime, events *[]BufferEvent) error {
	// Read capture file
	capturePath := filepath.Join(t.Path, "capture")
	captureData, err := os.ReadFile(capturePath)
	if err != nil {
		return fmt.Errorf("read capture: %w", err)
	}

	// Get encoder labels
	encoderLabels := t.EncoderLabels
	if len(encoderLabels) == 0 {
		encoderLabels = t.KernelNames
	}

	// Get timing for encoder timestamps
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return err
	}

	// Build encoder timestamp map
	encoderTimestamps := make(map[string]uint64)
	if len(metrics.EncoderTimings) > 0 {
		for i, encoder := range metrics.EncoderTimings {
			if i < len(encoderLabels) {
				encoderTimestamps[encoderLabels[i]] = encoder.StartTimestamp
			}
		}
	}

	// Parse buffer address mapping
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

	// Extract buffer IDs from names and update usage
	for _, name := range addrToName {
		parts := strings.TrimPrefix(name, "MTLBuffer-")
		idEnd := strings.Index(parts, "-")
		if idEnd > 0 {
			bufferID := parts[:idEnd]
			if lifetime, ok := bufferMap[bufferID]; ok {
				lifetime.UsageCount++
			}
		}
	}

	return nil
}

// calculatePeakMemory computes the maximum concurrent memory usage.
func calculatePeakMemory(buffers []*BufferLifetime) uint64 {
	if len(buffers) == 0 {
		return 0
	}

	// Collect all time points
	type timePoint struct {
		time  uint64
		delta int64 // +size for alloc, -size for dealloc
	}
	points := make([]timePoint, 0, len(buffers)*2)

	for _, buf := range buffers {
		points = append(points, timePoint{buf.AllocTime, int64(buf.Size)})
		points = append(points, timePoint{buf.DeallocTime, -int64(buf.Size)})
	}

	// Sort by time
	sort.Slice(points, func(i, j int) bool {
		return points[i].time < points[j].time
	})

	// Calculate peak
	var current, peak uint64
	for _, pt := range points {
		if pt.delta > 0 {
			current += uint64(pt.delta)
		} else {
			current -= uint64(-pt.delta)
		}
		if current > peak {
			peak = current
		}
	}

	return peak
}

// FormatBufferTimelineASCII generates an ASCII timeline visualization.
func FormatBufferTimelineASCII(timeline *BufferTimeline, width int) string {
	if width < 40 {
		width = 80
	}

	output := "=== Buffer Timeline ===\n\n"
	output += fmt.Sprintf("Duration: %.2f ms\n", float64(timeline.Duration)/1e6)
	output += fmt.Sprintf("Total Buffers: %d\n", timeline.TotalBuffers)
	output += fmt.Sprintf("Total Memory: %s\n", formatBytes(timeline.TotalMemory))
	output += fmt.Sprintf("Peak Memory: %s\n\n", formatBytes(timeline.PeakMemory))

	if len(timeline.Buffers) == 0 {
		return output + "No buffers found\n"
	}

	// Timeline visualization
	output += "Buffer Lifetimes:\n"
	nameWidth := 25
	timelineWidth := width - nameWidth - 15

	// Header
	output += fmt.Sprintf("%-*s %10s  ", nameWidth, "Buffer", "Size")
	output += "Timeline\n"
	output += strings.Repeat("-", width) + "\n"

	for _, buf := range timeline.Buffers {
		// Calculate bar position and length
		startPos := int(float64(buf.AllocTime-timeline.StartTime) / float64(timeline.Duration) * float64(timelineWidth))
		endPos := int(float64(buf.DeallocTime-timeline.StartTime) / float64(timeline.Duration) * float64(timelineWidth))
		if endPos > timelineWidth {
			endPos = timelineWidth
		}
		if startPos < 0 {
			startPos = 0
		}
		if endPos < startPos {
			endPos = startPos
		}

		// Build timeline bar
		bar := strings.Repeat(" ", startPos)
		length := endPos - startPos
		if length > 0 {
			bar += strings.Repeat("█", length)
		}
		remaining := timelineWidth - len(bar)
		if remaining > 0 {
			bar += strings.Repeat(" ", remaining)
		}

		name := buf.BufferName
		if len(name) > nameWidth {
			name = name[:nameWidth-3] + "..."
		}

		output += fmt.Sprintf("%-*s %10s  %s\n",
			nameWidth, name, formatBytes(buf.Size), bar)
	}

	return output
}

// FormatBufferTimelineSummary generates a text summary of buffer usage.
func FormatBufferTimelineSummary(timeline *BufferTimeline) string {
	output := "=== Buffer Timeline Summary ===\n\n"
	output += fmt.Sprintf("Trace Duration: %.2f ms\n", float64(timeline.Duration)/1e6)
	output += fmt.Sprintf("Total Buffers: %d\n", timeline.TotalBuffers)
	output += fmt.Sprintf("Total Memory Allocated: %s\n", formatBytes(timeline.TotalMemory))
	output += fmt.Sprintf("Peak Memory Usage: %s (%.1f%% of total)\n\n",
		formatBytes(timeline.PeakMemory),
		float64(timeline.PeakMemory)/float64(timeline.TotalMemory)*100)

	// Top buffers by size
	sorted := make([]*BufferLifetime, len(timeline.Buffers))
	copy(sorted, timeline.Buffers)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Size > sorted[j].Size
	})

	output += "Top Buffers by Size:\n"
	output += fmt.Sprintf("%-30s %15s %12s\n", "Buffer", "Size", "Lifetime(ms)")
	output += strings.Repeat("-", 60) + "\n"

	count := 10
	if len(sorted) < count {
		count = len(sorted)
	}

	for i := 0; i < count; i++ {
		buf := sorted[i]
		lifetime := float64(buf.DeallocTime-buf.AllocTime) / 1e6
		output += fmt.Sprintf("%-30s %15s %12.2f\n",
			truncateString(buf.BufferName, 30),
			formatBytes(buf.Size),
			lifetime)
	}

	return output
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	if bytes >= GB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	} else if bytes >= MB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	} else if bytes >= KB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	}
	return fmt.Sprintf("%d B", bytes)
}

// ExportBufferTimelineToChrome exports buffer timeline in Chrome tracing format.
func ExportBufferTimelineToChrome(timeline *BufferTimeline) map[string]interface{} {
	events := make([]map[string]interface{}, 0)

	// Add process/thread metadata
	events = append(events, map[string]interface{}{
		"name": "process_name",
		"ph":   "M",
		"pid":  2,
		"tid":  0,
		"args": map[string]interface{}{
			"name": "Buffer Memory",
		},
	})

	// Add buffer lifetime events
	for i, buf := range timeline.Buffers {
		// Complete event showing buffer lifetime
		events = append(events, map[string]interface{}{
			"name": buf.BufferName,
			"cat":  "buffer",
			"ph":   "X",
			"ts":   buf.AllocTime / 1000, // Convert to microseconds
			"dur":  (buf.DeallocTime - buf.AllocTime) / 1000,
			"pid":  2,
			"tid":  i % 20, // Distribute across 20 tracks
			"args": map[string]interface{}{
				"size":        buf.Size,
				"size_mb":     float64(buf.Size) / (1024 * 1024),
				"usage_count": buf.UsageCount,
			},
		})
	}

	return map[string]interface{}{
		"traceEvents":     events,
		"displayTimeUnit": "ms",
		"metadata": map[string]interface{}{
			"total_buffers": timeline.TotalBuffers,
			"total_memory":  timeline.TotalMemory,
			"peak_memory":   timeline.PeakMemory,
			"duration_ns":   timeline.Duration,
		},
	}
}
