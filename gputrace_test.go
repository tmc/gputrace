package gputrace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	// Try to find a test .gputrace file
	testPath := "/tmp/objc_metal_trace.gputrace"

	// Check if test file exists
	if _, err := os.Stat(testPath); os.IsNotExist(err) {
		t.Skip("Test .gputrace file not found at /tmp/objc_metal_trace.gputrace")
	}

	trace, err := Open(testPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Verify metadata
	if trace.Metadata == nil {
		t.Error("Metadata is nil")
	} else {
		t.Logf("UUID: %s", trace.Metadata.UUID)
		t.Logf("Graphics API: %d", trace.Metadata.GraphicsAPI)
		t.Logf("Device ID: %d", trace.Metadata.DeviceID)
		t.Logf("Captured Frames: %d", trace.Metadata.CapturedFramesCount)

		if trace.Metadata.GraphicsAPI != 1 {
			t.Errorf("Expected Metal (API=1), got %d", trace.Metadata.GraphicsAPI)
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
	t.Logf("Kernel names found: %v", trace.KernelNames)
	t.Logf("Encoder labels found: %v", trace.EncoderLabels)
	t.Logf("Buffer labels found: %v", trace.BufferLabels)
	t.Logf("Command queue label: %s", trace.CommandQueueLabel)

	// Check for expected kernel names from our test case
	expectedKernels := []string{"step1_normalize", "step2_apply_relu", "step3_scale_output"}
	for _, expected := range expectedKernels {
		found := false
		for _, actual := range trace.KernelNames {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected kernel '%s' not found", expected)
		}
	}

	// Check for expected encoder labels
	expectedLabels := []string{"Stage1_Normalize", "Stage2_ReLU", "Stage3_Scale"}
	for _, expected := range expectedLabels {
		found := false
		for _, actual := range trace.EncoderLabels {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected encoder label '%s' not found", expected)
		}
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
	testPath := "/tmp/objc_metal_trace.gputrace"

	if _, err := os.Stat(testPath); os.IsNotExist(err) {
		t.Skip("Test .gputrace file not found")
	}

	// Check if store0 exists
	store0Path := filepath.Join(testPath, "store0")
	if _, err := os.Stat(store0Path); os.IsNotExist(err) {
		t.Skip("store0 not found in test trace")
	}

	trace := &Trace{Path: testPath}
	decompressed, err := trace.DecompressStore(0)
	if err != nil {
		t.Fatalf("DecompressStore failed: %v", err)
	}

	t.Logf("Decompressed store0: %d bytes", len(decompressed))

	// Store is mostly zeros in our simple test case
	nonZeroCount := 0
	for _, b := range decompressed {
		if b != 0 {
			nonZeroCount++
		}
	}
	t.Logf("Non-zero bytes: %d (%.2f%%)", nonZeroCount, 100.0*float64(nonZeroCount)/float64(len(decompressed)))
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
