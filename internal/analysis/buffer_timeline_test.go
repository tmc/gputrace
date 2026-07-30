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
	if !strings.Contains(out, "Memory Upper Bound: 4.00 KB (approximate)") {
		t.Fatalf("formatted timeline missing approximate memory semantics:\n%s", out)
	}
}

func TestBufferTimelineSuppressesAdviceWhenAttributionIncomplete(t *testing.T) {
	analysis := &BufferTimelineAnalysis{
		BufferEvents: map[uint64]*BufferLifecycle{
			2: {Address: 2, FirstSeen: 1, LastSeen: 1, AccessCount: 1},
		},
		TotalBuffers:        1,
		TotalAllocations:    1,
		ExpectedEncoders:    3,
		AttributedEncoders:  1,
		AttributionComplete: false,
		AttributionNote:     "test attribution gap",
	}

	out := FormatBufferTimelineSummary(analysis)
	if !strings.Contains(out, "Optimization advice withheld because encoder attribution is incomplete") {
		t.Fatalf("summary missing incomplete-attribution warning:\n%s", out)
	}
	for _, bad := range []string{"Consider buffer pooling", "could be eliminated", "released earlier"} {
		if strings.Contains(out, bad) {
			t.Fatalf("summary contains unsupported advice %q:\n%s", bad, out)
		}
	}
}

func TestBufferTimelineSummaryOrderIsDeterministic(t *testing.T) {
	analysis := &BufferTimelineAnalysis{
		BufferEvents: map[uint64]*BufferLifecycle{
			3: {Address: 3, FirstSeen: 1, LastSeen: 5, AccessCount: 2},
			1: {Address: 1, FirstSeen: 1, LastSeen: 5, AccessCount: 2},
			2: {Address: 2, FirstSeen: 1, LastSeen: 5, AccessCount: 2},
		},
		TotalBuffers:        3,
		TotalAllocations:    3,
		AttributionComplete: true,
	}
	want := FormatBufferTimelineSummary(analysis)
	for i := 0; i < 20; i++ {
		if got := FormatBufferTimelineSummary(analysis); got != want {
			t.Fatalf("summary changed between identical renders")
		}
	}
	first := strings.Index(want, "0x0000000000000001")
	second := strings.Index(want, "0x0000000000000002")
	third := strings.Index(want, "0x0000000000000003")
	if !(first < second && second < third) {
		t.Fatalf("tied buffers not ordered by address:\n%s", want)
	}
}
