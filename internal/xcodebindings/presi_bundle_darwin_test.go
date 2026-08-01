//go:build darwin

package xcodebindings

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
)

// TestPreSiBundleStreamData compares the two ways to load a profiler archive.
//
// The proven path is +[GTShaderProfilerStreamData dataFromArchivedDataURL:],
// which takes the streamData file alone. The class also has
// -initWithPreSiBundle: (@24@0:8@16), which takes a containing bundle, and
// -dataFileURL / -preSiBundleURL to report what it resolved.
//
// This matters because the cost model's emptiness has been attributed to the
// capture carrying no counter data, and that premise is wrong: the
// .gpuprofiler_raw directory holds 40 Counters_f_*.raw, 40 Profiling_f_*.raw and
// 40 Timeline_f_*.raw files — one per GPU core on this device, about 4 GB in
// total. A loader given only streamData cannot reach them. A loader given the
// bundle might.
//
// The signal to watch is derivedCountersData, which is an empty dictionary on
// the archived-URL path, and costCount's records becoming non-zero.
func TestPreSiBundleStreamData(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}

	cls := objc.GetClass("GTShaderProfilerStreamData")
	if cls == 0 {
		if _, err := loadStreamData(streamPath); err != nil {
			t.Fatalf("load framework: %v", err)
		}
		cls = objc.GetClass("GTShaderProfilerStreamData")
	}
	if cls == 0 {
		t.Fatal("GTShaderProfilerStreamData class not found")
	}
	if !responds(objc.ID(cls), "alloc") {
		t.Fatal("GTShaderProfilerStreamData does not respond to alloc")
	}

	// Candidates from most to least specific: the .gpuprofiler_raw directory
	// that holds the raw files, and the .gputrace bundle above it.
	rawDir := filepath.Dir(streamPath)
	candidates := []string{rawDir, filepath.Dir(rawDir)}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		for _, candidate := range candidates {
			allocated := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
			if allocated == 0 {
				t.Errorf("%s: alloc failed", candidate)
				continue
			}
			if !responds(allocated, "initWithPreSiBundle:") {
				t.Fatal("GTShaderProfilerStreamData does not respond to initWithPreSiBundle:")
			}
			url := foundation.NewURLFileURLWithPath(candidate)
			data := objc.Send[objc.ID](allocated, objc.Sel("initWithPreSiBundle:"), url)
			if data == 0 {
				t.Logf("initWithPreSiBundle:%s -> nil", candidate)
				continue
			}
			t.Logf("initWithPreSiBundle:%s -> %s", candidate, objc.IDToString(objc.Send[objc.ID](data, objc.Sel("description"))))
			for _, sel := range []string{"dataFileURL", "preSiBundleURL"} {
				if !responds(data, sel) {
					continue
				}
				if v := objc.Send[objc.ID](data, objc.Sel(sel)); v != 0 {
					t.Logf("  %s = %s", sel, objc.IDToString(objc.Send[objc.ID](v, objc.Sel("path"))))
				} else {
					t.Logf("  %s = nil", sel)
				}
			}
			for _, sel := range []string{"encoderInfoCount", "pipelineStateInfoCount", "gpuCommandInfoCount"} {
				if responds(data, sel) {
					t.Logf("  %s = %d", sel, uint64Property(data, sel))
				}
			}
			objc.Send[objc.ID](data, objc.Sel("release"))
		}
	})
}
