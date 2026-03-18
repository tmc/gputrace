package timing

import (
	"encoding/binary"
	"fmt"

	"github.com/tmc/gputrace/internal/trace"
)

// EncoderTiming is an alias to trace.EncoderTiming for backwards compatibility.
type EncoderTiming = trace.EncoderTiming

// ExtractTimingData extracts timing information for all encoders from capture data.
func ExtractTimingData(t *trace.Trace) ([]*EncoderTiming, error) {
	var timings []*EncoderTiming

	// For each encoder label we found, look for timestamps at fixed offsets
	for _, label := range t.EncoderLabels {
		timing, err := extractTimingForLabel(t.CaptureData, label)
		if err != nil {
			// Continue on error - some labels might not have timing
			continue
		}
		timings = append(timings, timing)
	}

	// Calculate percentages
	if len(timings) > 0 {
		calculatePercentages(timings)
	}

	return timings, nil
}

// extractTimingForLabel finds timestamps around a specific encoder label.
func extractTimingForLabel(data []byte, label string) (*EncoderTiming, error) {
	// Find the label in the data
	offset := findLabelOffset(data, label)
	if offset == -1 {
		return nil, fmt.Errorf("label %q not found", label)
	}

	// Search for timestamps around the label using multiple strategies
	// Pattern observed:
	// - Start timestamp is typically 60-80 bytes before the label
	// - End timestamp is typically 8-32 bytes after the label

	startTime, startFound := findTimestampBefore(data, offset, 40, 96)
	if !startFound {
		return nil, fmt.Errorf("start timestamp not found for label %q", label)
	}

	endTime, endFound := findTimestampAfter(data, offset+len(label), 0, 64)
	if !endFound {
		return nil, fmt.Errorf("end timestamp not found for label %q", label)
	}

	// Validate timestamps
	if endTime <= startTime {
		return nil, fmt.Errorf("end time before start time for label %q", label)
	}

	duration := endTime - startTime

	// Validate duration is reasonable for a GPU operation
	// Max reasonable duration: 10 seconds (10e9 ns)
	// Min reasonable duration: 100 ns
	if duration < 100 || duration > 10000000000 {
		return nil, fmt.Errorf("unreasonable duration %d ns for label %q", duration, label)
	}

	return &EncoderTiming{
		Label:          label,
		StartTimestamp: startTime,
		EndTimestamp:   endTime,
		DurationNs:     duration,
		DurationMs:     float64(duration) / 1e6,
	}, nil
}

// findTimestampBefore searches backwards from offset for a Mach timestamp.
// Scans from offset-maxDelta to offset-minDelta.
func findTimestampBefore(data []byte, offset, minDelta, maxDelta int) (uint64, bool) {
	for delta := minDelta; delta <= maxDelta; delta += 8 {
		checkOffset := offset - delta
		if checkOffset < 0 || checkOffset+8 > len(data) {
			continue
		}

		val := binary.LittleEndian.Uint64(data[checkOffset : checkOffset+8])
		if isValidMachTimestamp(val) {
			return val, true
		}
	}
	return 0, false
}

// findTimestampAfter searches forward from offset for a Mach timestamp.
// Scans from offset+minDelta to offset+maxDelta.
func findTimestampAfter(data []byte, offset, minDelta, maxDelta int) (uint64, bool) {
	for delta := minDelta; delta <= maxDelta; delta += 8 {
		checkOffset := offset + delta
		if checkOffset < 0 || checkOffset+8 > len(data) {
			continue
		}

		val := binary.LittleEndian.Uint64(data[checkOffset : checkOffset+8])
		if isValidMachTimestamp(val) {
			return val, true
		}
	}
	return 0, false
}

// isValidMachTimestamp checks if a value looks like a Mach absolute timestamp.
// Mach timestamps are typically in nanoseconds since boot and should be:
// - Greater than 1e16 (about 115 days of uptime minimum)
// - Less than 1e18 (about 31 years maximum)
// - Not have suspicious patterns like many trailing zeros
func isValidMachTimestamp(val uint64) bool {
	// Check basic range
	if val < 10000000000000000 || val > 1000000000000000000 {
		return false
	}

	// Reject values with too many trailing zeros (likely structure padding)
	if val&0xFFFF == 0 {
		return false
	}

	// Reject values with suspicious byte patterns
	bytes := [8]byte{
		byte(val), byte(val >> 8), byte(val >> 16), byte(val >> 24),
		byte(val >> 32), byte(val >> 40), byte(val >> 48), byte(val >> 56),
	}

	// Check for too many zero bytes (indicates non-timestamp data)
	zeroCount := 0
	for _, b := range bytes {
		if b == 0 {
			zeroCount++
		}
	}
	if zeroCount > 3 {
		return false
	}

	return true
}

// calculatePercentages calculates the percentage of total GPU time for each encoder.
func calculatePercentages(timings []*EncoderTiming) {
	var totalDuration uint64
	for _, t := range timings {
		totalDuration += t.DurationNs
	}

	if totalDuration == 0 {
		return
	}

	for _, t := range timings {
		t.Percentage = float32(float64(t.DurationNs) / float64(totalDuration) * 100.0)
	}
}

// findLabelOffset finds the byte offset of a label string in data.
func findLabelOffset(data []byte, label string) int {
	labelBytes := []byte(label)
	for i := 0; i <= len(data)-len(labelBytes); i++ {
		match := true
		for j := 0; j < len(labelBytes); j++ {
			if data[i+j] != labelBytes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
