//go:build darwin

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireXcodeOpenableTraceRejectsProfilerOnly(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "profile.gputrace")
	raw := filepath.Join(bundle, "profile.gpuprofiler_raw")
	if err := os.MkdirAll(raw, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raw, "streamData"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	err := requireXcodeOpenableTrace(bundle)
	if err == nil {
		t.Fatal("profiler-only input accepted")
	}
	for _, text := range []string{"profiler-only", "no capture or index", "without --profiler-only"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
}

func TestRequireXcodeOpenableTraceAcceptsCapture(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "capture.gputrace")
	if err := os.Mkdir(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "capture"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := requireXcodeOpenableTrace(bundle); err != nil {
		t.Fatal(err)
	}
}
