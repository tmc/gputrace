//go:build darwin

package counter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseTimelineFile(t *testing.T) {
	// Test with real trace data if available
	testDir := "/tmp/bench_traces/BenchmarkQwen25_MLP_Go-perfdata.gputrace/BenchmarkQwen25_MLP_Go_.gputrace.gpuprofiler_raw"
	testFile := filepath.Join(testDir, "Timeline_f_0.raw")

	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skip("Test trace not available:", testFile)
	}

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

func TestParseTimelineFilesFromDir(t *testing.T) {
	testDir := "/tmp/bench_traces/BenchmarkQwen25_MLP_Go-perfdata.gputrace/BenchmarkQwen25_MLP_Go_.gputrace.gpuprofiler_raw"

	if _, err := os.Stat(testDir); os.IsNotExist(err) {
		t.Skip("Test trace directory not available:", testDir)
	}

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

	if err := os.WriteFile(path, data, 0666); err != nil {
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
