//go:build darwin

package xcodebindings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"
)

// TestProbeStreamDataPerfFixture exercises the Objective-C extraction path on
// a real profiler capture. The fixture is intentionally opt-in because it is
// several gigabytes and is not part of this repository.
func TestProbeStreamDataPerfFixture(t *testing.T) {
	fixture := testtrace.Path("GPUTRACE_PERF_FIXTURE", testtrace.Bundle)
	if fixture == "" {
		t.Skip("set GPUTRACE_TEST_TRACE or GPUTRACE_PERF_FIXTURE to a .gputrace bundle")
	}
	fixture, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	perfDir := fixture
	if !strings.HasSuffix(filepath.Base(fixture), ".gpuprofiler_raw") {
		entries, readErr := os.ReadDir(fixture)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var found bool
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".gpuprofiler_raw") {
				perfDir = filepath.Join(fixture, entry.Name())
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no .gpuprofiler_raw sidecar in %s", fixture)
		}
	}
	streamPath := filepath.Join(perfDir, "streamData")
	if _, err := os.Stat(streamPath); err != nil {
		t.Skipf("streamData unavailable at %s: %v", streamPath, err)
	}
	summary, err := ProbeStreamData(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ObjectID == "" {
		t.Fatal("ProbeStreamData returned no Objective-C object")
	}
	if summary.EncoderInfoCount == 0 || summary.FunctionInfoCount == 0 {
		t.Fatalf("streamData counts = encoders %d, functions %d; want nonzero", summary.EncoderInfoCount, summary.FunctionInfoCount)
	}
}
