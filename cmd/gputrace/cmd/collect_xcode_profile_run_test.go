//go:build darwin

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitForExportedTraceRequiresProfilerData(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "trace-perfdata.gputrace")
	if err := os.Mkdir(bundle, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := waitForExportedTrace([]string{bundle}, 0)
	if err == nil {
		t.Fatal("waitForExportedTrace succeeded without profiler data")
	}
	if !strings.Contains(err.Error(), "without .gpuprofiler_raw") {
		t.Fatalf("error = %q, want missing profiler data", err)
	}

	profilerDir := filepath.Join(bundle, "trace.gputrace.gpuprofiler_raw")
	if err := os.Mkdir(profilerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilerDir, "streamData"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := waitForExportedTrace([]string{bundle}, 0)
	if err != nil {
		t.Fatalf("waitForExportedTrace failed: %v", err)
	}
	if got != bundle {
		t.Fatalf("path = %q, want %q", got, bundle)
	}
}

func TestTargetedShowPerformanceFoundSentinel(t *testing.T) {
	if targetedShowPerformanceFound == 0 {
		t.Fatal("targetedShowPerformanceFound must be non-zero")
	}
	if !isTargetedShowPerformanceFound(targetedShowPerformanceFound) {
		t.Fatal("targeted Show Performance sentinel not recognized")
	}
	if isTargetedShowPerformanceFound(0) {
		t.Fatal("zero should not be recognized as targeted Show Performance sentinel")
	}
}
