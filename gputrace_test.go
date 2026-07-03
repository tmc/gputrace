package gputrace

import (
	"path/filepath"
	"testing"
)

func TestOpenMinimalTraceBundle(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "minimal.gputrace")
	if err := writeMinimalTraceBundle(tracePath); err != nil {
		t.Fatal(err)
	}

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if trace.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if trace.Metadata.UUID != "minimal-test-trace" {
		t.Fatalf("UUID = %q, want minimal-test-trace", trace.Metadata.UUID)
	}
	if trace.Metadata.GraphicsAPI != 1 {
		t.Fatalf("GraphicsAPI = %d, want 1", trace.Metadata.GraphicsAPI)
	}
	if got := string(trace.CaptureData); got != MagicMTSP {
		t.Fatalf("CaptureData = %q, want %q", got, MagicMTSP)
	}
	if len(trace.DeviceResources) != 0 {
		t.Fatalf("DeviceResources length = %d, want 0", len(trace.DeviceResources))
	}
}
