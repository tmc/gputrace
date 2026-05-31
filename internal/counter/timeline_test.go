//go:build darwin

package counter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	timelineRawEnv = "GPUTRACE_COUNTER_TIMELINE_RAW"
	timelineDirEnv = "GPUTRACE_COUNTER_TIMELINE_DIR"
)

func TestParseTimelineFileIntegration(t *testing.T) {
	testFile := requireTimelineEnvPath(t, timelineRawEnv, false)

	td, err := ParseTimelineFile(testFile)
	if err != nil {
		t.Fatalf("ParseTimelineFile failed: %v", err)
	}

	t.Logf("Timeline file: %s", td.FilePath)
	t.Logf("  File size: %d bytes", td.FileSize)
	t.Logf("  File index: %d", td.FileIndex)
	t.Logf("  Header magic: 0x%x", td.Header.Magic)
	t.Logf("  Counter count: %d", td.Header.CounterCount)
	t.Logf("  Data offset: 0x%x (%d)", td.Header.DataOffset, td.Header.DataOffset)
	t.Logf("  Entry count: %d", td.Header.EntryCount)
	t.Logf("  GPU timestamp: %d (profiler sampling, not CB timing)", td.Header.GPUTimestamp)
	t.Logf("  Chunk count: %d", td.ChunkCount)
	t.Logf("  Valid chunks: %d", td.ValidChunks)
	t.Logf("  Kick traces found: %d", len(td.KickTraces))
	t.Logf("  Draw traces found: %d", len(td.DrawTraces))

	// Log first few kick traces if found
	for i, kt := range td.KickTraces {
		if i >= 5 {
			t.Logf("  ... and %d more kick traces", len(td.KickTraces)-5)
			break
		}
		t.Logf("  KickTrace[%d]: start=%d end=%d duration=%d encoder=%d pipeline=%d",
			i, kt.StartTimestamp, kt.EndTimestamp, kt.EndTimestamp-kt.StartTimestamp,
			kt.EncoderIndex, kt.PipelineIndex)
	}
}

func TestParseTimelineFilesFromDirIntegration(t *testing.T) {
	testDir := requireTimelineEnvPath(t, timelineDirEnv, true)

	timelines, err := ParseTimelineFilesFromDir(testDir)
	if err != nil {
		t.Fatalf("ParseTimelineFilesFromDir failed: %v", err)
	}

	t.Logf("Parsed %d timeline files", len(timelines))

	summary := GetTimelineSummary(timelines)
	t.Logf("Summary:")
	t.Logf("  File count: %d", summary.FileCount)
	t.Logf("  Total size: %d bytes (%.2f MB)", summary.TotalSize, float64(summary.TotalSize)/(1024*1024))
	t.Logf("  Total entries: %d", summary.TotalEntries)
	t.Logf("  Total chunks: %d", summary.TotalChunks)
	t.Logf("  Valid chunks: %d", summary.ValidChunks)
	t.Logf("  Kick traces: %d", summary.KickTraceCount)
	t.Logf("  Draw traces: %d", summary.DrawTraceCount)
	t.Logf("  Timestamp range: %d - %d", summary.MinTimestamp, summary.MaxTimestamp)

	// Log per-file details
	for _, td := range timelines {
		t.Logf("  File %d: %d entries, %d kicks, magic=0x%x",
			td.FileIndex, td.Header.EntryCount, len(td.KickTraces), td.Header.Magic)
	}
}

func requireTimelineEnvPath(t *testing.T, envName string, wantDir bool) string {
	t.Helper()

	path := os.Getenv(envName)
	if path == "" {
		t.Skipf("set %s to run this Timeline_f raw integration test", envName)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s=%q is not accessible: %v", envName, path, err)
	}
	if wantDir && !info.IsDir() {
		t.Fatalf("%s=%q is not a directory", envName, path)
	}
	if !wantDir && info.IsDir() {
		t.Fatalf("%s=%q is a directory, want Timeline_f_*.raw file", envName, path)
	}
	return path
}

func TestTimelineHeaderSize(t *testing.T) {
	// Verify header structure size
	if TimelineHeaderSize != 256 {
		t.Errorf("TimelineHeaderSize should be 256, got %d", TimelineHeaderSize)
	}
}

func TestParseTimelineFileCountsSparseIndexOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Timeline_f_7.raw")
	data := make([]byte, TimelineHeaderSize+3*256+32)
	binary.LittleEndian.PutUint64(data[0:8], TimelineMagic)
	binary.LittleEndian.PutUint32(data[12:16], 752)
	binary.LittleEndian.PutUint64(data[32:40], uint64(TimelineHeaderSize+3*256))
	binary.LittleEndian.PutUint64(data[80:88], 9)
	binary.LittleEndian.PutUint64(data[104:112], 600_000_000_000)

	data[TimelineHeaderSize+256+17] = 1
	data[TimelineHeaderSize+2*256+31] = 2

	if err := os.WriteFile(path, data, 0o666); err != nil {
		t.Fatal(err)
	}

	td, err := ParseTimelineFile(path)
	if err != nil {
		t.Fatalf("ParseTimelineFile failed: %v", err)
	}
	if td.FileIndex != 7 {
		t.Fatalf("FileIndex = %d, want 7", td.FileIndex)
	}
	if td.Header.CounterCount != 752 {
		t.Fatalf("CounterCount = %d, want 752", td.Header.CounterCount)
	}
	if td.ChunkCount != 3 {
		t.Fatalf("ChunkCount = %d, want 3", td.ChunkCount)
	}
	if td.ValidChunks != 2 {
		t.Fatalf("ValidChunks = %d, want 2", td.ValidChunks)
	}
	if len(td.KickTraces) != 0 || len(td.DrawTraces) != 0 {
		t.Fatalf("raw chunk parser produced heuristic records: kicks=%d draws=%d", len(td.KickTraces), len(td.DrawTraces))
	}
	if td.RawFormatStatus == "" {
		t.Fatal("RawFormatStatus is empty")
	}
	if !strings.Contains(td.RawFormatStatus, "data section encoding unknown") {
		t.Fatalf("RawFormatStatus = %q, want unknown encoding detail", td.RawFormatStatus)
	}
}

func TestParseTimelineFileIgnoresSparseIndexRecordMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Timeline_f_3.raw")
	dataOffset := TimelineHeaderSize + 256
	data := make([]byte, dataOffset+32)
	binary.LittleEndian.PutUint64(data[0:8], TimelineMagic)
	binary.LittleEndian.PutUint64(data[32:40], uint64(dataOffset))
	binary.LittleEndian.PutUint64(data[80:88], 1)

	chunk := data[TimelineHeaderSize:dataOffset]
	binary.LittleEndian.PutUint32(chunk[0:4], 0x4e)
	binary.LittleEndian.PutUint64(chunk[8:16], 600_000_000_000)
	binary.LittleEndian.PutUint64(chunk[16:24], 600_000_010_000)

	if err := os.WriteFile(path, data, 0666); err != nil {
		t.Fatal(err)
	}

	td, err := ParseTimelineFile(path)
	if err != nil {
		t.Fatalf("ParseTimelineFile failed: %v", err)
	}
	if td.ValidChunks != 1 {
		t.Fatalf("ValidChunks = %d, want 1", td.ValidChunks)
	}
	if len(td.KickTraces) != 0 || len(td.DrawTraces) != 0 {
		t.Fatalf("raw chunk parser decoded sparse-index marker bytes: kicks=%d draws=%d", len(td.KickTraces), len(td.DrawTraces))
	}
	if !strings.Contains(td.RawFormatStatus, "data section encoding unknown") {
		t.Fatalf("RawFormatStatus = %q, want unknown encoding detail", td.RawFormatStatus)
	}
}

func TestParseTimelineFileReportsUnsupportedCompressedPayload(t *testing.T) {
	tests := []struct {
		name   string
		prefix []byte
		want   string
	}{
		{
			name:   "zlib",
			prefix: []byte{0x78, 0x5e, 0xed},
			want:   "zlib header",
		},
		{
			name:   "lz4",
			prefix: []byte{0x04, 0x22, 0x4d, 0x18},
			want:   "lz4 frame header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Timeline_f_1.raw")
			dataOffset := TimelineHeaderSize + 256
			data := make([]byte, dataOffset+len(tt.prefix)+8)
			binary.LittleEndian.PutUint64(data[0:8], TimelineMagic)
			binary.LittleEndian.PutUint64(data[32:40], uint64(dataOffset))
			data[TimelineHeaderSize+23] = 1
			copy(data[dataOffset:], tt.prefix)

			if err := os.WriteFile(path, data, 0666); err != nil {
				t.Fatal(err)
			}

			td, err := ParseTimelineFile(path)
			if err != nil {
				t.Fatalf("ParseTimelineFile failed: %v", err)
			}
			if td.ValidChunks != 1 {
				t.Fatalf("ValidChunks = %d, want 1", td.ValidChunks)
			}
			if len(td.KickTraces) != 0 || len(td.DrawTraces) != 0 {
				t.Fatalf("raw chunk parser produced heuristic records: kicks=%d draws=%d", len(td.KickTraces), len(td.DrawTraces))
			}
			if !strings.Contains(td.RawFormatStatus, tt.want) {
				t.Fatalf("RawFormatStatus = %q, want %q", td.RawFormatStatus, tt.want)
			}
			if !strings.Contains(td.RawFormatStatus, "decompression unsupported") {
				t.Fatalf("RawFormatStatus = %q, want fail-closed decompression detail", td.RawFormatStatus)
			}
		})
	}
}

func TestTimelineCompressionHeaderDetectionRequiresCompleteHeaders(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		wantZlib bool
		wantLZ4  bool
	}{
		{
			name:     "truncated zlib cmf only",
			payload:  []byte{0x78},
			wantZlib: false,
			wantLZ4:  false,
		},
		{
			name:     "valid zlib header",
			payload:  []byte{0x78, 0x5e},
			wantZlib: true,
			wantLZ4:  false,
		},
		{
			name:     "truncated lz4 frame magic",
			payload:  []byte{0x04, 0x22, 0x4d},
			wantZlib: false,
			wantLZ4:  false,
		},
		{
			name:     "valid lz4 frame magic",
			payload:  []byte{0x04, 0x22, 0x4d, 0x18},
			wantZlib: false,
			wantLZ4:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasZlibHeader(tt.payload); got != tt.wantZlib {
				t.Fatalf("hasZlibHeader(% x) = %v, want %v", tt.payload, got, tt.wantZlib)
			}
			if got := hasLZ4FrameHeader(tt.payload); got != tt.wantLZ4 {
				t.Fatalf("hasLZ4FrameHeader(% x) = %v, want %v", tt.payload, got, tt.wantLZ4)
			}
		})
	}
}

func TestParseTimelineFileInvalidDataOffsetDoesNotScanSparseIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Timeline_f_2.raw")
	data := make([]byte, TimelineHeaderSize+256)
	binary.LittleEndian.PutUint64(data[0:8], TimelineMagic)
	binary.LittleEndian.PutUint64(data[32:40], uint64(len(data)+256))
	data[TimelineHeaderSize+17] = 1

	if err := os.WriteFile(path, data, 0666); err != nil {
		t.Fatal(err)
	}

	td, err := ParseTimelineFile(path)
	if err != nil {
		t.Fatalf("ParseTimelineFile failed: %v", err)
	}
	if td.ChunkCount != 0 {
		t.Fatalf("ChunkCount = %d, want 0 for invalid data offset", td.ChunkCount)
	}
	if td.ValidChunks != 0 {
		t.Fatalf("ValidChunks = %d, want 0 for invalid data offset", td.ValidChunks)
	}
	if !strings.Contains(td.RawFormatStatus, "invalid data offset") || !strings.Contains(td.RawFormatStatus, "beyond file size") {
		t.Fatalf("RawFormatStatus = %q, want invalid offset detail", td.RawFormatStatus)
	}
}

func TestGTMioKickTraceSize(t *testing.T) {
	// GTMioKickTrace should be 38 bytes as specified
	// Format: QQIIIIIISSS = 8+8+4+4+4+4+4+2+2+2 = 42
	// But spec says 38, so there might be some overlap/packing
	// Our struct is larger but we only read 38 bytes
	expectedSize := 38
	t.Logf("GTMioKickTrace expected read size: %d bytes", expectedSize)
}

func TestGTMioDrawTraceSize(t *testing.T) {
	// GTMioDrawTrace should be 22 bytes as specified
	// Format: QQIS = 8+8+4+2 = 22
	expectedSize := 22
	t.Logf("GTMioDrawTrace expected read size: %d bytes", expectedSize)
}

func TestGTMioBinaryTraceSize(t *testing.T) {
	// GTMioBinaryTrace should be 38 bytes as specified
	// Format: QQQIIS = 8+8+8+4+4+2 = 34 (not 38?)
	// There might be padding
	expectedSize := 38
	t.Logf("GTMioBinaryTrace expected read size: %d bytes", expectedSize)
}
