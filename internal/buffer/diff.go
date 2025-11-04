package buffer

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// BufferDiff represents the differences between two buffer sets.
type BufferDiff struct {
	Added       []*BufferInfo   // Buffers only in trace2
	Removed     []*BufferInfo   // Buffers only in trace1
	Modified    []*BufferChange // Buffers in both with changes
	Unchanged   []*BufferInfo   // Buffers in both with no changes
	TotalDelta  int64           // Total size change (bytes)
	AddedSize   uint64          // Total size of added buffers
	RemovedSize uint64          // Total size of removed buffers
}

// BufferChange represents a change in a buffer between two traces.
type BufferChange struct {
	ID        string
	OldSize   uint64
	NewSize   uint64
	SizeDelta int64
}

// BufferInfo represents basic buffer information for comparison.
type BufferInfo struct {
	ID   string
	Size uint64
	Name string // Optional buffer name/label
}

// CompareBuffers compares buffers between two traces and returns a diff.
func CompareBuffers(trace1Buffers, trace2Buffers map[string]uint64) *BufferDiff {
	diff := &BufferDiff{
		Added:     make([]*BufferInfo, 0),
		Removed:   make([]*BufferInfo, 0),
		Modified:  make([]*BufferChange, 0),
		Unchanged: make([]*BufferInfo, 0),
	}

	// Find added and modified buffers
	for id, size2 := range trace2Buffers {
		if size1, exists := trace1Buffers[id]; exists {
			if size1 != size2 {
				// Modified
				delta := int64(size2) - int64(size1)
				diff.Modified = append(diff.Modified, &BufferChange{
					ID:        id,
					OldSize:   size1,
					NewSize:   size2,
					SizeDelta: delta,
				})
				diff.TotalDelta += delta
			} else {
				// Unchanged
				diff.Unchanged = append(diff.Unchanged, &BufferInfo{
					ID:   id,
					Size: size2,
				})
			}
		} else {
			// Added
			diff.Added = append(diff.Added, &BufferInfo{
				ID:   id,
				Size: size2,
			})
			diff.AddedSize += size2
			diff.TotalDelta += int64(size2)
		}
	}

	// Find removed buffers
	for id, size1 := range trace1Buffers {
		if _, exists := trace2Buffers[id]; !exists {
			diff.Removed = append(diff.Removed, &BufferInfo{
				ID:   id,
				Size: size1,
			})
			diff.RemovedSize += size1
			diff.TotalDelta -= int64(size1)
		}
	}

	// Sort for consistent output
	sort.Slice(diff.Added, func(i, j int) bool {
		return diff.Added[i].Size > diff.Added[j].Size
	})
	sort.Slice(diff.Removed, func(i, j int) bool {
		return diff.Removed[i].Size > diff.Removed[j].Size
	})
	sort.Slice(diff.Modified, func(i, j int) bool {
		absI := diff.Modified[i].SizeDelta
		if absI < 0 {
			absI = -absI
		}
		absJ := diff.Modified[j].SizeDelta
		if absJ < 0 {
			absJ = -absJ
		}
		return absI > absJ
	})

	return diff
}

// FormatBufferDiff returns a human-readable diff report.
func FormatBufferDiff(diff *BufferDiff, trace1Path, trace2Path string) string {
	out := fmt.Sprintf("=== Buffer Diff: %s vs %s ===\n\n", trace1Path, trace2Path)

	// Summary
	out += "Summary:\n"
	out += fmt.Sprintf("  Added:     %3d buffers (%s)\n", len(diff.Added), formatSize(diff.AddedSize))
	out += fmt.Sprintf("  Removed:   %3d buffers (%s)\n", len(diff.Removed), formatSize(diff.RemovedSize))
	out += fmt.Sprintf("  Modified:  %3d buffers\n", len(diff.Modified))
	out += fmt.Sprintf("  Unchanged: %3d buffers\n", len(diff.Unchanged))
	out += fmt.Sprintf("  Net delta: %s\n\n", formatSizeDelta(diff.TotalDelta))

	// Added buffers
	if len(diff.Added) > 0 {
		out += "Added Buffers:\n"
		for i, buf := range diff.Added {
			if i >= 10 {
				out += fmt.Sprintf("  ... and %d more\n", len(diff.Added)-i)
				break
			}
			out += fmt.Sprintf("  + %s (%s)\n", buf.ID, formatSize(buf.Size))
		}
		out += "\n"
	}

	// Removed buffers
	if len(diff.Removed) > 0 {
		out += "Removed Buffers:\n"
		for i, buf := range diff.Removed {
			if i >= 10 {
				out += fmt.Sprintf("  ... and %d more\n", len(diff.Removed)-i)
				break
			}
			out += fmt.Sprintf("  - %s (%s)\n", buf.ID, formatSize(buf.Size))
		}
		out += "\n"
	}

	// Modified buffers
	if len(diff.Modified) > 0 {
		out += "Modified Buffers:\n"
		for i, change := range diff.Modified {
			if i >= 10 {
				out += fmt.Sprintf("  ... and %d more\n", len(diff.Modified)-i)
				break
			}
			out += fmt.Sprintf("  ~ %s: %s → %s (%s)\n",
				change.ID,
				formatSize(change.OldSize),
				formatSize(change.NewSize),
				formatSizeDelta(change.SizeDelta))
		}
		out += "\n"
	}

	return out
}

// formatSize formats a size in bytes to human-readable format.
func formatSize(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// formatSizeDelta formats a size delta with +/- sign.
func formatSizeDelta(delta int64) string {
	sign := "+"
	abs := delta
	if delta < 0 {
		sign = "-"
		abs = -delta
	} else if delta == 0 {
		return "±0 B"
	}

	return sign + formatSize(uint64(abs))
}

// ExtractBufferSizes extracts buffer ID -> size mapping from a trace.
func ExtractBufferSizes(t *trace.Trace) (map[string]uint64, error) {
	buffers := make(map[string]uint64)

	// Look for buffer files in trace directory
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		// Match MTLBuffer-* files (version 0 contains actual data)
		if strings.HasPrefix(name, "MTLBuffer-") && strings.HasSuffix(name, "-0") {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			// Extract buffer ID (e.g., "MTLBuffer-16-0" -> "MTLBuffer-16")
			id := strings.TrimSuffix(name, "-0")
			buffers[id] = uint64(info.Size())
		}
	}

	return buffers, nil
}
