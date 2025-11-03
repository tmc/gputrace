package gputrace

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// Store0TimingData contains timing information extracted from store0.
type Store0TimingData struct {
	Encoders       []Store0Encoder
	TotalDurationNs uint64
}

// Store0Encoder represents timing for a single encoder.
type Store0Encoder struct {
	Index       int
	StartTime   uint64
	EndTime     uint64
	DurationNs  uint64
	KernelIndex int // Index into trace.KernelNames
}

// ExtractStore0Timing attempts to extract timing data from store0 file.
// This implements a heuristic parser for the Instruments timeline format.
func (t *Trace) ExtractStore0Timing() (*Store0TimingData, error) {
	// Check if store0 exists
	store0Path := filepath.Join(t.Path, "store0")
	if _, err := os.Stat(store0Path); os.IsNotExist(err) {
		return nil, fmt.Errorf("store0 file not found")
	}

	// Decompress store0
	data, err := t.DecompressStore(0)
	if err != nil {
		return nil, fmt.Errorf("decompress store0: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("store0 is empty after decompression")
	}

	// Parse timing data using heuristics
	timingData := &Store0TimingData{
		Encoders: make([]Store0Encoder, 0),
	}

	// Strategy: Look for timestamp pairs (start/end) in the data
	// Timestamps are typically uint64 values in the range 1e15 to 1e18
	timestamps := extractTimestamps(data)

	if len(timestamps) == 0 {
		return nil, fmt.Errorf("no timestamps found in store0")
	}

	// Group timestamps into encoder timing windows
	encoders := groupTimestampsIntoEncoders(timestamps, len(t.KernelNames))

	for i, enc := range encoders {
		if enc.EndTime > enc.StartTime {
			timingData.Encoders = append(timingData.Encoders, Store0Encoder{
				Index:       i,
				StartTime:   enc.StartTime,
				EndTime:     enc.EndTime,
				DurationNs:  enc.EndTime - enc.StartTime,
				KernelIndex: enc.KernelIndex,
			})
			timingData.TotalDurationNs += enc.EndTime - enc.StartTime
		}
	}

	return timingData, nil
}

// timestampCandidate represents a potential timestamp in the data.
type timestampCandidate struct {
	offset uint64
	value  uint64
}

// extractTimestamps scans binary data for timestamp-like values.
func extractTimestamps(data []byte) []timestampCandidate {
	candidates := make([]timestampCandidate, 0)

	// Scan for uint64 values that look like Mach timestamps
	for i := 0; i < len(data)-8; i += 8 {
		val := binary.LittleEndian.Uint64(data[i : i+8])

		// Mach timestamps are typically in range 1e15 to 1e18
		if val > 1000000000000000 && val < 10000000000000000000 {
			candidates = append(candidates, timestampCandidate{
				offset: uint64(i),
				value:  val,
			})
		}
	}

	return candidates
}

// groupTimestampsIntoEncoders attempts to pair timestamps into encoder timing windows.
func groupTimestampsIntoEncoders(timestamps []timestampCandidate, kernelCount int) []Store0Encoder {
	if len(timestamps) < 2 {
		return nil
	}

	encoders := make([]Store0Encoder, 0)

	// Simple heuristic: pair consecutive timestamps as start/end
	// This assumes timestamps are stored chronologically
	for i := 0; i < len(timestamps)-1; i += 2 {
		start := timestamps[i].value
		end := timestamps[i+1].value

		// Sanity check: end must be after start
		if end <= start {
			continue
		}

		// Duration should be reasonable (< 1 second for individual encoders)
		duration := end - start
		if duration > 1000000000 { // > 1 second
			continue
		}

		// Estimate kernel index based on position in sequence
		kernelIdx := i / 2
		if kernelIdx >= kernelCount {
			kernelIdx = kernelCount - 1
		}

		encoders = append(encoders, Store0Encoder{
			Index:       i / 2,
			StartTime:   start,
			EndTime:     end,
			DurationNs:  duration,
			KernelIndex: kernelIdx,
		})
	}

	return encoders
}


// ConvertStore0ToEncoderTimings converts Store0TimingData to EncoderTiming for pprof generation.
func (t *Trace) ConvertStore0ToEncoderTimings(store0Data *Store0TimingData) []*EncoderTiming {
	timings := make([]*EncoderTiming, 0, len(store0Data.Encoders))

	for _, enc := range store0Data.Encoders {
		// Get kernel name for this encoder
		label := fmt.Sprintf("Encoder %d", enc.Index)
		if enc.KernelIndex >= 0 && enc.KernelIndex < len(t.KernelNames) {
			label = t.KernelNames[enc.KernelIndex]
		}

		timing := &EncoderTiming{
			Label:          label,
			StartTimestamp: enc.StartTime,
			EndTimestamp:   enc.EndTime,
			DurationNs:     enc.DurationNs,
			DurationMs:     float64(enc.DurationNs) / 1e6,
		}

		timings = append(timings, timing)
	}

	// Calculate percentages
	calculatePercentages(timings)

	return timings
}
