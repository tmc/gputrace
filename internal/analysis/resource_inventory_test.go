package analysis

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestExtractResourceBufferInventoryCountsSidecarOnlyBuffers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MTLBuffer-1-0"), make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	data := appendResourceInventoryRecord(nil, "MTLBuffer-1-0", 999)
	data = appendResourceInventoryRecord(data, "MTLBuffer-2-0", 128)
	if err := os.WriteFile(filepath.Join(dir, "device-resources-0x1"), data, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	got := extractResourceBufferInventory(&trace.Trace{Path: dir})

	if got.Buffers != 2 {
		t.Fatalf("Buffers = %d, want 2", got.Buffers)
	}
	if got.Bytes != 192 {
		t.Fatalf("Bytes = %d, want 192", got.Bytes)
	}
}

func TestBufferAccessApplyResourceInventoryRaisesTotal(t *testing.T) {
	analysis := &BufferAccessAnalysis{
		BufferAccesses: map[uint64]*BufferAccessInfo{
			0x1000: {Address: 0x1000, AccessCount: 1},
		},
		TotalBuffers: 1,
	}

	analysis.applyResourceInventory(resourceBufferInventory{Buffers: 3, Bytes: 256})

	if analysis.TotalBuffers != 3 {
		t.Fatalf("TotalBuffers = %d, want 3", analysis.TotalBuffers)
	}
	if analysis.UnusedBuffers != 2 {
		t.Fatalf("UnusedBuffers = %d, want 2", analysis.UnusedBuffers)
	}
	if analysis.ResourceBytes != 256 {
		t.Fatalf("ResourceBytes = %d, want 256", analysis.ResourceBytes)
	}
}

func TestBufferTimelineApplyResourceInventoryRaisesTotals(t *testing.T) {
	analysis := &BufferTimelineAnalysis{
		TotalBuffers:    1,
		PeakMemoryBytes: 64,
	}

	analysis.applyResourceInventory(resourceBufferInventory{Buffers: 2, Bytes: 192})

	if analysis.TotalBuffers != 2 {
		t.Fatalf("TotalBuffers = %d, want 2", analysis.TotalBuffers)
	}
	if analysis.PeakMemoryBytes != 192 {
		t.Fatalf("PeakMemoryBytes = %d, want 192", analysis.PeakMemoryBytes)
	}
	if analysis.ResourceBytes != 192 {
		t.Fatalf("ResourceBytes = %d, want 192", analysis.ResourceBytes)
	}
}

func appendResourceInventoryRecord(dst []byte, name string, size uint64) []byte {
	dst = append(dst, []byte(name)...)
	dst = append(dst, 0)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], size)
	dst = append(dst, buf[:]...)
	return dst
}
