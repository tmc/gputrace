package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestExtractBufferTimelineUsesTraceMetadata(t *testing.T) {
	dir := t.TempDir()
	addr := uint64(0x123456780000)

	if err := os.WriteFile(filepath.Join(dir, "MTLBuffer-7-0"), make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write buffer file: %v", err)
	}

	timeline, err := ExtractBufferTimeline(&trace.Trace{
		Path:        dir,
		CaptureData: append(makeCtURecord(addr, "MTLBuffer-7-0"), makeCtRecord(addr)...),
	})
	if err != nil {
		t.Fatalf("ExtractBufferTimeline failed: %v", err)
	}

	lifecycle := timeline.BufferEvents[addr]
	if lifecycle == nil {
		t.Fatalf("buffer 0x%x not found", addr)
	}
	if got, want := lifecycle.Size, uint64(4096); got != want {
		t.Fatalf("Size = %d, want %d", got, want)
	}
	if got, want := timeline.PeakMemoryBytes, uint64(4096); got != want {
		t.Fatalf("PeakMemoryBytes = %d, want %d", got, want)
	}

	out := FormatBufferTimelineASCII(timeline, 80)
	if !strings.Contains(out, "Peak Memory:") {
		t.Fatalf("formatted timeline missing peak memory:\n%s", out)
	}
}
