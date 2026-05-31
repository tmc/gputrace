package trace

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	testPath := writeSyntheticTraceBundle(t)

	trace, err := Open(testPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Verify metadata
	if trace.Metadata == nil {
		t.Error("Metadata is nil")
	} else {
		if trace.Metadata.UUID != "synthetic-trace-test" {
			t.Errorf("Expected synthetic UUID, got %q", trace.Metadata.UUID)
		}
		if trace.Metadata.GraphicsAPI != 1 {
			t.Errorf("Expected Metal (API=1), got %d", trace.Metadata.GraphicsAPI)
		}
		if trace.Metadata.DeviceID != 1234 {
			t.Errorf("Expected device ID 1234, got %d", trace.Metadata.DeviceID)
		}
	}

	// Verify capture data loaded
	if len(trace.CaptureData) == 0 {
		t.Error("No capture data loaded")
	} else {
		t.Logf("Capture data size: %d bytes", len(trace.CaptureData))
	}

	// Verify device resources loaded
	if len(trace.DeviceResources) == 0 {
		t.Error("No device resources loaded")
	} else {
		t.Logf("Device resources: %d files", len(trace.DeviceResources))
		for addr, data := range trace.DeviceResources {
			t.Logf("  Device %s: %d bytes", addr, len(data))
		}
	}

	// Verify labels extracted
	if !contains(trace.KernelNames, "synthetic_kernel") {
		t.Errorf("Expected synthetic_kernel in kernel names, got %v", trace.KernelNames)
	}
	if !contains(trace.EncoderLabels, "synthetic_kernel") {
		t.Errorf("Expected synthetic_kernel in encoder labels, got %v", trace.EncoderLabels)
	}
}

func TestOpenRealTraceFromEnv(t *testing.T) {
	testPath := os.Getenv("GPUTRACE_TRACE_TEST_TRACE")
	if testPath == "" {
		t.Skip("set GPUTRACE_TRACE_TEST_TRACE to run real-trace Open coverage")
	}

	trace, err := Open(testPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", testPath, err)
	}
	if trace.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if len(trace.CaptureData) == 0 {
		t.Fatal("No capture data loaded")
	}
}

func TestReadMTSPHeader(t *testing.T) {
	// Create test MTSP data
	data := []byte("MTSP")
	data = append(data, 0x00, 0x04, 0x00, 0x00) // version
	data = append(data, 0x68, 0x00, 0x00, 0x00) // size
	data = append(data, 0x0b, 0xd8, 0xff, 0xff) // offset

	header, err := ReadMTSPHeader(data)
	if err != nil {
		t.Fatalf("ReadMTSPHeader failed: %v", err)
	}

	if string(header.Magic[:]) != "MTSP" {
		t.Errorf("Expected magic 'MTSP', got '%s'", string(header.Magic[:]))
	}

	t.Logf("Version: 0x%08x", header.Version)
	t.Logf("Size: 0x%08x", header.Size)
	t.Logf("Offset: 0x%08x", header.Offset)
}

func TestDecompressStore(t *testing.T) {
	testPath := writeSyntheticTraceBundle(t)
	want := []byte("deterministic store payload")

	trace := &Trace{Path: testPath}
	decompressed, err := trace.DecompressStore(0)
	if err != nil {
		t.Fatalf("DecompressStore failed: %v", err)
	}
	if !bytes.Equal(decompressed, want) {
		t.Fatalf("DecompressStore = %q, want %q", decompressed, want)
	}
}

func TestHelperFunctions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid kernel 1", "step1_normalize", true},
		{"valid kernel 2", "step2_apply_relu", true},
		{"valid kernel 3", "ThreeStageKernel", true},
		{"generic root", "root", false},
		{"generic buffers", "buffers", false},
		{"too short", "ab", false},
		{"too long", "this_is_a_really_really_really_really_really_long_kernel_name_that_exceeds_limits", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikeKernelName(tt.input)
			if result != tt.expected {
				t.Errorf("looksLikeKernelName(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsPrintable(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hello", true},
		{"Stage1_Normalize", true},
		{"hello\x00world", false},
		{"hello\nworld", false},
		{"test123", true},
	}

	for _, tt := range tests {
		result := isPrintable(tt.input)
		if result != tt.expected {
			t.Errorf("isPrintable(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func writeSyntheticTraceBundle(t *testing.T) string {
	t.Helper()

	tracePath := filepath.Join(t.TempDir(), "synthetic.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir synthetic trace: %v", err)
	}

	writeFile(t, filepath.Join(tracePath, "metadata"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>(uuid)</key>
	<string>synthetic-trace-test</string>
	<key>DYCaptureSession.capture_version</key>
	<integer>1</integer>
	<key>DYCaptureSession.graphics_api</key>
	<integer>1</integer>
	<key>DYCaptureSession.deviceId</key>
	<integer>1234</integer>
	<key>DYCaptureSession.nativePointerSize</key>
	<integer>8</integer>
	<key>DYCaptureEngine.captured_frames_count</key>
	<integer>1</integer>
	<key>DYCaptureSession.boundaryLess</key>
	<false/>
</dict>
</plist>`))
	writeFile(t, filepath.Join(tracePath, "capture"), syntheticMTSPData("synthetic_kernel"))
	writeFile(t, filepath.Join(tracePath, "device-resources-0x1234"), syntheticMTSPData("resource_kernel"))
	writeFile(t, filepath.Join(tracePath, "store0"), zlibData(t, []byte("deterministic store payload")))

	return tracePath
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func syntheticMTSPData(label string) []byte {
	data := make([]byte, 16)
	copy(data, MagicMTSP)
	binary.LittleEndian.PutUint32(data[4:8], 0x400)
	binary.LittleEndian.PutUint32(data[8:12], uint32(16+12+len(label)+1))

	data = append(data, []byte("CS\x00\x00")...)
	data = binary.LittleEndian.AppendUint64(data, 0x1234)
	data = append(data, label...)
	data = append(data, 0)
	for len(data) < 300 {
		data = append(data, 0)
	}
	return data
}

func zlibData(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write zlib data: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}
	return buf.Bytes()
}
