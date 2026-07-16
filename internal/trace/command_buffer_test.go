package trace

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// syntheticCommandBufferData returns MTSP capture bytes containing a single
// CUUU command-buffer record followed by a CS encoder record carrying label.
func syntheticCommandBufferData(label string) []byte {
	data := make([]byte, 16)
	copy(data, MagicMTSP)
	binary.LittleEndian.PutUint32(data[4:8], 0x400)

	// CUUU command-buffer marker with an 8-byte timestamp.
	data = append(data, []byte("CUUU")...)
	data = binary.LittleEndian.AppendUint64(data, 0xdeadbeef)

	// CS encoder record: 8-byte address, then a NUL-terminated label.
	data = append(data, []byte("CS\x00\x00")...)
	data = binary.LittleEndian.AppendUint64(data, 0x1234)
	data = append(data, label...)
	data = append(data, 0)

	for len(data) < 300 {
		data = append(data, 0)
	}
	return data
}

func writeCaptureOnlyBundle(t *testing.T, captureName string) *Trace {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, captureName)
	if err := os.WriteFile(path, syntheticCommandBufferData("MultipleEncoders"), 0o644); err != nil {
		t.Fatalf("write %s: %v", captureName, err)
	}
	return &Trace{Path: dir}
}

// TestParseCommandBuffersUnsortedCaptureFallback verifies that command buffers
// are parsed from unsorted-capture when the primary capture file is absent.
// Some bundles (e.g. certain profiler exports) retain only unsorted-capture;
// before the fallback ParseCommandBuffers returned a read error for them while
// loadCaptureData already handled the same case.
func TestParseCommandBuffersUnsortedCaptureFallback(t *testing.T) {
	for _, name := range []string{"capture", "unsorted-capture"} {
		t.Run(name, func(t *testing.T) {
			tr := writeCaptureOnlyBundle(t, name)

			cbs, err := tr.ParseCommandBuffers()
			if err != nil {
				t.Fatalf("ParseCommandBuffers: %v", err)
			}
			if len(cbs) != 1 {
				t.Fatalf("got %d command buffers, want 1", len(cbs))
			}
			if cbs[0].Timestamp != 0xdeadbeef {
				t.Errorf("timestamp = %#x, want 0xdeadbeef", cbs[0].Timestamp)
			}
			if cbs[0].Label != "MultipleEncoders" {
				t.Errorf("label = %q, want %q", cbs[0].Label, "MultipleEncoders")
			}
		})
	}
}
