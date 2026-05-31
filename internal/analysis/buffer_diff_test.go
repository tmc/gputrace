package analysis

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestExtractBufferSizesUsesTraceMetadata(t *testing.T) {
	dir := t.TempDir()
	addr := uint64(0x123456780000)

	if err := os.WriteFile(filepath.Join(dir, "MTLBuffer-7-0"), make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write buffer file: %v", err)
	}

	tr := &trace.Trace{
		Path:        dir,
		CaptureData: append(makeCtURecord(addr, "MTLBuffer-7-0"), makeCtRecord(addr)...),
	}

	info, err := ExtractBufferSizes(tr)
	if err != nil {
		t.Fatalf("ExtractBufferSizes failed: %v", err)
	}

	buf := info.Buffers[addr]
	if buf == nil {
		t.Fatalf("buffer 0x%x not found", addr)
	}
	if !buf.SizeKnown {
		t.Fatalf("SizeKnown = false, want true")
	}
	if got, want := buf.Size, uint64(4096); got != want {
		t.Fatalf("Size = %d, want %d", got, want)
	}
	if got, want := buf.SizeSource, bufferSizeSourceTraceMetadata; got != want {
		t.Fatalf("SizeSource = %q, want %q", got, want)
	}
	if got, want := info.TotalMemoryBytes, uint64(4096); got != want {
		t.Fatalf("TotalMemoryBytes = %d, want %d", got, want)
	}
	if got, want := info.KnownSizeBuffers, 1; got != want {
		t.Fatalf("KnownSizeBuffers = %d, want %d", got, want)
	}
	if got, want := info.UnknownSizeBuffers, 0; got != want {
		t.Fatalf("UnknownSizeBuffers = %d, want %d", got, want)
	}
}

func TestExtractBufferSizesDoesNotEstimateMissingMetadata(t *testing.T) {
	addr := uint64(0xabcdef0000)
	tr := &trace.Trace{
		CaptureData: makeCtRecord(addr),
	}

	info, err := ExtractBufferSizes(tr)
	if err != nil {
		t.Fatalf("ExtractBufferSizes failed: %v", err)
	}

	buf := info.Buffers[addr]
	if buf == nil {
		t.Fatalf("buffer 0x%x not found", addr)
	}
	if buf.SizeKnown {
		t.Fatalf("SizeKnown = true, want false")
	}
	if got, want := buf.Size, uint64(0); got != want {
		t.Fatalf("Size = %d, want %d", got, want)
	}
	if got, want := info.TotalMemoryBytes, uint64(0); got != want {
		t.Fatalf("TotalMemoryBytes = %d, want %d", got, want)
	}
	if got, want := info.UnknownSizeBuffers, 1; got != want {
		t.Fatalf("UnknownSizeBuffers = %d, want %d", got, want)
	}
}

func TestFormatBufferDiffCallsOutIncompleteSizeMetadata(t *testing.T) {
	commonAddr := uint64(0x1010101000)
	addedAddr := uint64(0x2020202000)

	info1, err := ExtractBufferSizes(&trace.Trace{CaptureData: makeCtRecord(commonAddr)})
	if err != nil {
		t.Fatalf("ExtractBufferSizes trace1 failed: %v", err)
	}
	info2, err := ExtractBufferSizes(&trace.Trace{CaptureData: makeCtRecord(commonAddr, addedAddr)})
	if err != nil {
		t.Fatalf("ExtractBufferSizes trace2 failed: %v", err)
	}

	out := FormatBufferDiff(CompareBuffers(info1, info2), "trace1.gputrace", "trace2.gputrace")

	for _, want := range []string{
		"Buffer Size Metadata:",
		"Status: incomplete; memory totals include only buffers with trace metadata",
		"Known Memory Usage:",
		"size unknown",
		"Buffer size metadata is incomplete; memory-change insights are unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted diff missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "1024 bytes") {
		t.Fatalf("formatted diff still includes old access-count estimate:\n%s", out)
	}
}

func makeCtURecord(addr uint64, name string) []byte {
	const markerOffset = 16

	recordSize := markerOffset + 28 + len(name) + 1
	if recordSize < 80 {
		recordSize = 80
	}

	data := make([]byte, recordSize)
	binary.LittleEndian.PutUint32(data[0:4], uint32(recordSize))
	copy(data[markerOffset:], []byte("CtU<b>ulul"))
	binary.LittleEndian.PutUint64(data[markerOffset+20:], addr)
	copy(data[markerOffset+28:], name)
	return data
}

func makeCtRecord(bindings ...uint64) []byte {
	const markerOffset = 16

	recordSize := markerOffset + 28 + len(bindings)*8
	if recordSize < 64 {
		recordSize = 64
	}

	data := make([]byte, recordSize)
	binary.LittleEndian.PutUint32(data[0:4], uint32(recordSize))
	copy(data[markerOffset:], []byte("Ct\000\000"))
	binary.LittleEndian.PutUint32(data[markerOffset+20:], uint32(len(bindings)))
	binary.LittleEndian.PutUint32(data[markerOffset+24:], 8)

	for i, binding := range bindings {
		binary.LittleEndian.PutUint64(data[markerOffset+28+i*8:], binding)
	}

	return data
}
