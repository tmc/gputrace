package gputrace

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SignpostInterval represents a Metal signpost interval.
// Signposts provide shader-level profiling information from the AGX driver.
type SignpostInterval struct {
	Name           string            // Signpost name (e.g., "ShaderExecution")
	Category       string            // Category (e.g., "ShaderTimeline")
	Subsystem      string            // Subsystem (e.g., "com.apple.Metal.AGXSignposts")
	StartTimestamp uint64            // Mach absolute time
	EndTimestamp   uint64            // Mach absolute time
	DurationNs     uint64            // Duration in nanoseconds
	ThreadID       uint64            // Thread ID
	ProcessID      uint64            // Process ID
	Arguments      map[string]string // Named arguments
}

// SignpostParser parses Metal signpost data from traces.
type SignpostParser struct {
	trace *Trace
}

// NewSignpostParser creates a new signpost parser.
func NewSignpostParser(trace *Trace) *SignpostParser {
	return &SignpostParser{trace: trace}
}

// ParseMetalSignposts extracts Metal AGX signpost intervals.
func (p *SignpostParser) ParseMetalSignposts() ([]*SignpostInterval, error) {
	// Try to find signpost data
	// Common locations:
	// - trace.gputrace/signpost.raw
	// - trace.trace (companion file with signpost data)

	signpostPaths := []string{
		filepath.Join(p.trace.Path, "signpost.raw"),
		filepath.Join(p.trace.Path, ".gpuprofiler_raw", "signpost.raw"),
	}

	// Try .trace companion
	traceBase := p.trace.Path
	if filepath.Ext(traceBase) == ".gputrace" {
		traceBase = traceBase[:len(traceBase)-9]
	}
	signpostPaths = append(signpostPaths, traceBase+".trace")

	var intervals []*SignpostInterval
	var lastErr error

	for _, path := range signpostPaths {
		i, err := p.parseSignpostFile(path)
		if err == nil && len(i) > 0 {
			intervals = append(intervals, i...)
		} else if err != nil {
			lastErr = err
		}
	}

	if len(intervals) == 0 && lastErr != nil {
		return nil, fmt.Errorf("no signpost data found: %w", lastErr)
	}

	// Filter for Metal AGX signposts only
	metalIntervals := make([]*SignpostInterval, 0, len(intervals))
	for _, interval := range intervals {
		if p.isMetalSignpost(interval) {
			metalIntervals = append(metalIntervals, interval)
		}
	}

	return metalIntervals, nil
}

// parseSignpostFile parses a signpost data file.
func (p *SignpostParser) parseSignpostFile(path string) ([]*SignpostInterval, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Check file type - signpost files often have a header
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return nil, err
	}

	// Reset to start
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	// Try different parsers based on file format
	// This is a simplified implementation - actual format may vary
	return p.parseSignpostRecords(f)
}

// parseSignpostRecords parses signpost records from a file.
func (p *SignpostParser) parseSignpostRecords(r io.Reader) ([]*SignpostInterval, error) {
	var intervals []*SignpostInterval

	// Signpost record format (simplified):
	// - Record header (variable length)
	// - Timestamp (8 bytes)
	// - Thread/Process IDs (16 bytes)
	// - Name length (4 bytes)
	// - Name (variable)
	// - Arguments (variable)

	buf := make([]byte, 4096)

	for {
		// Read record header
		n, err := r.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return intervals, err
		}
		if n < 32 {
			break
		}

		// Parse basic fields
		timestamp := binary.LittleEndian.Uint64(buf[0:8])
		threadID := binary.LittleEndian.Uint64(buf[8:16])
		processID := binary.LittleEndian.Uint64(buf[16:24])

		// Name length and string
		nameLen := binary.LittleEndian.Uint32(buf[24:28])
		if nameLen > 256 || int(nameLen) > n-28 {
			continue // Skip malformed record
		}

		name := string(buf[28 : 28+nameLen])

		// Try to extract category and subsystem from name
		// Format is often: "subsystem:category:name"
		parts := strings.Split(name, ":")
		var subsystem, category, actualName string
		if len(parts) >= 3 {
			subsystem = parts[0]
			category = parts[1]
			actualName = strings.Join(parts[2:], ":")
		} else {
			actualName = name
		}

		interval := &SignpostInterval{
			Name:           actualName,
			Category:       category,
			Subsystem:      subsystem,
			StartTimestamp: timestamp,
			ThreadID:       threadID,
			ProcessID:      processID,
			Arguments:      make(map[string]string),
		}

		intervals = append(intervals, interval)
	}

	// Match start/end events
	return p.matchSignpostIntervals(intervals), nil
}

// matchSignpostIntervals matches begin/end signpost events into intervals.
func (p *SignpostParser) matchSignpostIntervals(events []*SignpostInterval) []*SignpostInterval {
	// Build map of begin events
	begins := make(map[string]*SignpostInterval)
	var intervals []*SignpostInterval

	for _, event := range events {
		key := fmt.Sprintf("%s_%d", event.Name, event.ThreadID)

		// Check if this is an end event (has duration or matched begin)
		if begin, exists := begins[key]; exists {
			// This is an end event - create interval
			interval := &SignpostInterval{
				Name:           begin.Name,
				Category:       begin.Category,
				Subsystem:      begin.Subsystem,
				StartTimestamp: begin.StartTimestamp,
				EndTimestamp:   event.StartTimestamp,
				DurationNs:     event.StartTimestamp - begin.StartTimestamp,
				ThreadID:       begin.ThreadID,
				ProcessID:      begin.ProcessID,
				Arguments:      begin.Arguments,
			}
			intervals = append(intervals, interval)
			delete(begins, key)
		} else {
			// This is a begin event
			begins[key] = event
		}
	}

	return intervals
}

// isMetalSignpost checks if a signpost is from Metal AGX.
func (p *SignpostParser) isMetalSignpost(interval *SignpostInterval) bool {
	return strings.Contains(interval.Subsystem, "Metal") ||
		strings.Contains(interval.Subsystem, "AGX") ||
		interval.Category == "ShaderTimeline"
}

// ExtractShaderTimeline extracts shader timeline intervals from signposts.
func (p *SignpostParser) ExtractShaderTimeline() ([]*SignpostInterval, error) {
	intervals, err := p.ParseMetalSignposts()
	if err != nil {
		return nil, err
	}

	// Filter for shader timeline category
	shaderIntervals := make([]*SignpostInterval, 0)
	for _, interval := range intervals {
		if interval.Category == "ShaderTimeline" {
			shaderIntervals = append(shaderIntervals, interval)
		}
	}

	return shaderIntervals, nil
}

// CorrelateSignpostsWithEncoders matches signpost intervals with encoder timings.
func CorrelateSignpostsWithEncoders(
	signposts []*SignpostInterval,
	encoders []*EncoderTiming,
) map[string]*SignpostInterval {

	correlation := make(map[string]*SignpostInterval)

	// Match by timestamp overlap
	for _, encoder := range encoders {
		for _, signpost := range signposts {
			// Check if signpost overlaps with encoder
			if signpost.StartTimestamp >= encoder.StartTimestamp &&
				signpost.EndTimestamp <= encoder.EndTimestamp {

				// Associate this signpost with encoder
				correlation[encoder.Label] = signpost
				break
			}
		}
	}

	return correlation
}

// EnhancedTimingFromSignposts creates enhanced timing data from signposts.
func (p *SignpostParser) EnhancedTimingFromSignposts() ([]*EncoderTiming, error) {
	intervals, err := p.ParseMetalSignposts()
	if err != nil {
		return nil, err
	}

	timings := make([]*EncoderTiming, 0, len(intervals))

	for i, interval := range intervals {
		timing := &EncoderTiming{
			Label:          fmt.Sprintf("%s_%d", interval.Name, i),
			StartTimestamp: interval.StartTimestamp,
			EndTimestamp:   interval.EndTimestamp,
			DurationNs:     interval.DurationNs,
			DurationMs:     float64(interval.DurationNs) / 1e6,
		}

		timings = append(timings, timing)
	}

	// Calculate percentages
	if len(timings) > 0 {
		var totalNs uint64
		for _, t := range timings {
			totalNs += t.DurationNs
		}

		for _, t := range timings {
			t.Percentage = float32(t.DurationNs) * 100.0 / float32(totalNs)
		}
	}

	return timings, nil
}

// SignpostStatistics contains statistics about parsed signposts.
type SignpostStatistics struct {
	TotalIntervals    int
	MetalIntervals    int
	ShaderIntervals   int
	UniqueSubsystems  []string
	UniqueCategories  []string
	TotalDurationNs   uint64
	AverageDurationNs uint64
}

// CalculateSignpostStatistics computes statistics from signpost intervals.
func CalculateSignpostStatistics(intervals []*SignpostInterval) *SignpostStatistics {
	stats := &SignpostStatistics{
		TotalIntervals: len(intervals),
	}

	subsystems := make(map[string]bool)
	categories := make(map[string]bool)

	for _, interval := range intervals {
		stats.TotalDurationNs += interval.DurationNs

		if strings.Contains(interval.Subsystem, "Metal") ||
			strings.Contains(interval.Subsystem, "AGX") {
			stats.MetalIntervals++
		}

		if interval.Category == "ShaderTimeline" {
			stats.ShaderIntervals++
		}

		if interval.Subsystem != "" {
			subsystems[interval.Subsystem] = true
		}
		if interval.Category != "" {
			categories[interval.Category] = true
		}
	}

	// Convert maps to slices
	for s := range subsystems {
		stats.UniqueSubsystems = append(stats.UniqueSubsystems, s)
	}
	for c := range categories {
		stats.UniqueCategories = append(stats.UniqueCategories, c)
	}

	if len(intervals) > 0 {
		stats.AverageDurationNs = stats.TotalDurationNs / uint64(len(intervals))
	}

	return stats
}

// FormatSignpostInterval returns a human-readable representation.
func FormatSignpostInterval(interval *SignpostInterval) string {
	return fmt.Sprintf("%s:%s:%s [%d -> %d] %.2fms",
		interval.Subsystem,
		interval.Category,
		interval.Name,
		interval.StartTimestamp,
		interval.EndTimestamp,
		float64(interval.DurationNs)/1e6)
}
