//go:build darwin

// Package counter provides GPU performance counter parsing and mapping.
// This file parses Timeline_f_*.raw files from .gpuprofiler_raw to extract timeline data.

package counter

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// TimelineHeader represents the 256-byte header of a Timeline_f_*.raw file.
//
// The file structure is:
//   - Header (256 bytes): Contains metadata including counter count and data offset
//   - Sparse Index (0 to DataOffset): Contains embedded timestamps for profiler sampling
//   - Data Section (DataOffset onwards): Zigzag varint-encoded counter/profiler data
//
// Note: Timestamps in raw files are profiler sampling timestamps (500B-700B range),
// NOT GPU command execution timestamps. They're in a different timebase than CB timestamps.
// For accurate timing, use CB timestamps from APSTimelineData plist.
type TimelineHeader struct {
	Magic        uint64 // Offset 0: File magic number
	Flags        uint64 // Offset 8: File flags
	CounterCount uint32 // Offset 12: Number of counters (e.g., 752)
	Reserved1    uint64 // Offset 16
	Reserved2    uint64 // Offset 24
	DataOffset   uint64 // Offset 32: Offset to data section (typically 0x3c000 = 245760)
	Reserved3    uint64 // Offset 40
	Reserved4    uint64 // Offset 48
	Reserved5    uint64 // Offset 56
	Reserved6    uint64 // Offset 64
	Reserved7    uint64 // Offset 72
	EntryCount   uint64 // Offset 80: Number of entries in the timeline
	Reserved8    uint64 // Offset 88
	Reserved9    uint64 // Offset 96
	GPUTimestamp uint64 // Offset 104: Base GPU timestamp (profiler sampling, not CB timing)
	Reserved10   uint64 // Offset 112
	Reserved11   uint64 // Offset 120
}

const (
	TimelineHeaderSize = 256
	TimelineMagic      = 0x773d413b0016b551 // Common magic observed in Timeline files

	rawTimelineUnsupportedStatus = "raw Timeline_f payload not decoded; use streamData APSTimelineData for command timing"
)

// GTMioKickTrace represents a GPU kick (encoder execution) trace record.
// Format: QQIIIIIISSS (38 bytes) - but may vary by GPU generation.
type GTMioKickTrace struct {
	StartTimestamp uint64 // GPU start timestamp
	EndTimestamp   uint64 // GPU end timestamp
	KickID         uint32 // Kick (encoder) identifier
	EncoderIndex   uint32 // Index into encoder array
	CommandIndex   uint32 // Command buffer index
	PipelineIndex  uint32 // Pipeline state index
	Flags          uint32 // Execution flags
	Short1         uint16 // Additional flags/metadata
	Short2         uint16 // Additional flags/metadata
	Short3         uint16 // Additional flags/metadata
}

// GTMioDrawTrace represents a draw/dispatch trace record.
// Format: QQIS (22 bytes)
type GTMioDrawTrace struct {
	StartTimestamp uint64 // GPU start timestamp
	EndTimestamp   uint64 // GPU end timestamp
	CommandType    uint32 // Type of draw/dispatch command
	Flags          uint16 // Execution flags
}

// GTMioBinaryTrace represents a binary (shader) trace record.
// Format: QQQIIS (38 bytes)
type GTMioBinaryTrace struct {
	Timestamp1    uint64 // Primary timestamp
	Timestamp2    uint64 // Secondary timestamp
	BinaryAddress uint64 // Address of the binary/shader
	BinarySize    uint32 // Size of the binary
	BinaryType    uint32 // Type of binary (vertex, fragment, compute, etc.)
	Flags         uint16 // Additional flags
}

// TimelineData contains all parsed timeline information from a Timeline_f_*.raw file.
type TimelineData struct {
	Header          TimelineHeader
	FilePath        string
	FileIndex       int    // Index from filename (e.g., 0 from Timeline_f_0.raw)
	FileSize        int64  // Total file size
	RawData         []byte // Raw file data for advanced parsing
	KickTraces      []GTMioKickTrace
	DrawTraces      []GTMioDrawTrace
	ChunkCount      int    // Number of 256-byte sparse-index blocks before DataOffset.
	ValidChunks     int    // Number of non-zero sparse-index blocks.
	RawFormatStatus string // Current parser status for Timeline_f_*.raw payloads.
}

// ParseTimelineFile parses a single Timeline_f_*.raw file.
func ParseTimelineFile(path string) (*TimelineData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read timeline file: %w", err)
	}

	if len(data) < TimelineHeaderSize {
		return nil, fmt.Errorf("file too small for header: %d bytes", len(data))
	}

	td := &TimelineData{
		FilePath:        path,
		FileSize:        int64(len(data)),
		RawData:         data,
		RawFormatStatus: rawTimelineUnsupportedStatus,
	}

	// Extract file index from filename
	base := filepath.Base(path)
	if strings.HasPrefix(base, "Timeline_f_") && strings.HasSuffix(base, ".raw") {
		idxStr := strings.TrimSuffix(strings.TrimPrefix(base, "Timeline_f_"), ".raw")
		fmt.Sscanf(idxStr, "%d", &td.FileIndex)
	}

	// Parse header
	td.Header = TimelineHeader{
		Magic:        binary.LittleEndian.Uint64(data[0:8]),
		Flags:        binary.LittleEndian.Uint64(data[8:16]),
		CounterCount: binary.LittleEndian.Uint32(data[12:16]), // Counter count at offset 12
		Reserved1:    binary.LittleEndian.Uint64(data[16:24]),
		Reserved2:    binary.LittleEndian.Uint64(data[24:32]),
		DataOffset:   binary.LittleEndian.Uint64(data[32:40]), // Data section offset (typically 0x3c000)
		Reserved3:    binary.LittleEndian.Uint64(data[40:48]),
		Reserved4:    binary.LittleEndian.Uint64(data[48:56]),
		Reserved5:    binary.LittleEndian.Uint64(data[56:64]),
		Reserved6:    binary.LittleEndian.Uint64(data[64:72]),
		Reserved7:    binary.LittleEndian.Uint64(data[72:80]),
		EntryCount:   binary.LittleEndian.Uint64(data[80:88]),
		Reserved8:    binary.LittleEndian.Uint64(data[88:96]),
		Reserved9:    binary.LittleEndian.Uint64(data[96:104]),
		GPUTimestamp: binary.LittleEndian.Uint64(data[104:112]),
		Reserved10:   binary.LittleEndian.Uint64(data[112:120]),
		Reserved11:   binary.LittleEndian.Uint64(data[120:128]),
	}
	td.RawFormatStatus = timelineRawFormatStatus(data, td.Header)

	// Calculate chunk count based on data offset (this is the data section size, not chunk size)
	// The data section starts at DataOffset, so chunk count is calculated from remaining data
	if td.Header.DataOffset > 0 && td.Header.DataOffset <= uint64(len(data)) {
		// Count 256-byte blocks in the sparse index section.
		if td.Header.DataOffset > TimelineHeaderSize {
			td.ChunkCount = int((td.Header.DataOffset - TimelineHeaderSize) / 256)
		}
	}

	// Count sparse-index chunks. Raw record decoding is intentionally disabled
	// until the delta/varint payload format is understood.
	td.parseChunks()

	return td, nil
}

// parseChunks extracts timeline records from data chunks.
//
// Timeline_f_*.raw structure:
//   - Header (256 bytes)
//   - Sparse Index (256 to DataOffset): Contains embedded profiler sampling timestamps
//   - Data Section (DataOffset onwards): Zigzag varint-encoded counter/profiler data
//
// The sparse index section is scanned for valid timestamp patterns.
func (td *TimelineData) parseChunks() {
	if td.Header.DataOffset <= TimelineHeaderSize || td.Header.DataOffset > uint64(len(td.RawData)) {
		return
	}

	// The "chunks" are 256-byte blocks in the sparse index section
	chunkSize := 256
	dataStart := TimelineHeaderSize
	dataEnd := int(td.Header.DataOffset)
	if dataEnd > len(td.RawData) {
		dataEnd = len(td.RawData)
	}

	// Process sparse index section in 256-byte blocks
	for chunkOffset := dataStart; chunkOffset+chunkSize <= dataEnd; chunkOffset += chunkSize {
		chunk := td.RawData[chunkOffset : chunkOffset+chunkSize]

		// Check if chunk has data (not all zeros)
		hasData := false
		for i := 0; i < len(chunk); i++ {
			if chunk[i] != 0 {
				hasData = true
				break
			}
		}

		if hasData {
			td.ValidChunks++
			td.parseChunkRecords(chunk)
		}
	}
}

// parseChunkRecords records that a sparse-index chunk is populated.
//
// Timeline_f_*.raw record bytes are not decoded. The data section status is
// reported by RawFormatStatus, including recognized zlib and lz4 frame headers.
// Sparse-index bytes that look like record markers are intentionally ignored:
// tests cover that no kick/draw records are synthesized from those markers.
func (td *TimelineData) parseChunkRecords(chunk []byte) {
	// Timeline chunks contain packed records that are likely:
	// 1. Delta-encoded timestamps (not absolute)
	// 2. Variable-length encoded
	// 3. Possibly compressed
	//
	// The raw format remains unsupported until the payload encoding is known.
	// APSTimelineData parsing lives in streamdata.go and is the supported
	// source for command timing.
}

func timelineRawFormatStatus(data []byte, header TimelineHeader) string {
	detail := timelineRawFormatDetail(data, header)
	if detail == "" {
		return rawTimelineUnsupportedStatus
	}
	return rawTimelineUnsupportedStatus + "; " + detail
}

func timelineRawFormatDetail(data []byte, header TimelineHeader) string {
	switch {
	case header.DataOffset == 0:
		return "missing data offset"
	case header.DataOffset < TimelineHeaderSize:
		return fmt.Sprintf("invalid data offset 0x%x before header end 0x%x", header.DataOffset, TimelineHeaderSize)
	case header.DataOffset > uint64(len(data)):
		return fmt.Sprintf("invalid data offset 0x%x beyond file size 0x%x", header.DataOffset, len(data))
	}

	payload := data[header.DataOffset:]
	if len(payload) == 0 {
		return "empty data section"
	}
	if hasZlibHeader(payload) {
		return "data section starts with zlib header; decompression unsupported"
	}
	if hasLZ4FrameHeader(payload) {
		return "data section starts with lz4 frame header; decompression unsupported"
	}
	return "data section encoding unknown"
}

func hasZlibHeader(data []byte) bool {
	if len(data) < 2 {
		return false
	}

	// RFC 1950 zlib streams use compression method 8 in CMF and a CMF/FLG
	// checksum divisible by 31. This recognizes common headers such as
	// 0x78 0x01, 0x78 0x5e, 0x78 0x9c, and 0x78 0xda without decoding.
	cmf, flg := data[0], data[1]
	return cmf&0x0f == 8 && cmf>>4 <= 7 && (uint16(cmf)<<8|uint16(flg))%31 == 0
}

func hasLZ4FrameHeader(data []byte) bool {
	// LZ4 frame streams start with little-endian magic 0x184d2204.
	return len(data) >= 4 && data[0] == 0x04 && data[1] == 0x22 && data[2] == 0x4d && data[3] == 0x18
}

// ParseTimelineFiles parses all Timeline_f_*.raw files from a trace.
func ParseTimelineFiles(t *trace.Trace) ([]*TimelineData, error) {
	// Find .gpuprofiler_raw directory
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return nil, fmt.Errorf("read trace directory: %w", err)
		}

		found := false
		for _, entry := range entries {
			if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
				perfDir = filepath.Join(t.Path, entry.Name())
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("no .gpuprofiler_raw directory found")
		}
	}

	return ParseTimelineFilesFromDir(perfDir)
}

// ParseTimelineFilesFromDir parses all Timeline_f_*.raw files from a directory.
func ParseTimelineFilesFromDir(dir string) ([]*TimelineData, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var timelines []*TimelineData
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "Timeline_f_") && strings.HasSuffix(name, ".raw") {
			path := filepath.Join(dir, name)
			td, err := ParseTimelineFile(path)
			if err != nil {
				// Skip invalid files, continue with others
				continue
			}
			timelines = append(timelines, td)
		}
	}

	// Sort by file index
	sort.Slice(timelines, func(i, j int) bool {
		return timelines[i].FileIndex < timelines[j].FileIndex
	})

	return timelines, nil
}

// TimelineSummary provides aggregate statistics across all timeline files.
type TimelineSummary struct {
	FileCount      int
	TotalSize      int64
	TotalEntries   uint64
	TotalChunks    int
	ValidChunks    int
	KickTraceCount int
	DrawTraceCount int
	MinTimestamp   uint64
	MaxTimestamp   uint64
}

// TimelineSummaryForData computes aggregate statistics from multiple timeline files.
func TimelineSummaryForData(timelines []*TimelineData) *TimelineSummary {
	summary := &TimelineSummary{
		FileCount:    len(timelines),
		MinTimestamp: ^uint64(0), // Max uint64
	}

	for _, td := range timelines {
		summary.TotalSize += td.FileSize
		summary.TotalEntries += td.Header.EntryCount
		summary.TotalChunks += td.ChunkCount
		summary.ValidChunks += td.ValidChunks
		summary.KickTraceCount += len(td.KickTraces)
		summary.DrawTraceCount += len(td.DrawTraces)

		if td.Header.GPUTimestamp > 0 && td.Header.GPUTimestamp < summary.MinTimestamp {
			summary.MinTimestamp = td.Header.GPUTimestamp
		}
		if td.Header.GPUTimestamp > summary.MaxTimestamp {
			summary.MaxTimestamp = td.Header.GPUTimestamp
		}

		for _, kt := range td.KickTraces {
			if kt.StartTimestamp < summary.MinTimestamp {
				summary.MinTimestamp = kt.StartTimestamp
			}
			if kt.EndTimestamp > summary.MaxTimestamp {
				summary.MaxTimestamp = kt.EndTimestamp
			}
		}
	}

	if summary.MinTimestamp == ^uint64(0) {
		summary.MinTimestamp = 0
	}

	return summary
}

// ExtractTimelineFromTrace is a convenience function to extract and summarize timeline data.
func ExtractTimelineFromTrace(t *trace.Trace) (*TimelineSummary, []*TimelineData, error) {
	timelines, err := ParseTimelineFiles(t)
	if err != nil {
		return nil, nil, err
	}

	summary := TimelineSummaryForData(timelines)
	return summary, timelines, nil
}
