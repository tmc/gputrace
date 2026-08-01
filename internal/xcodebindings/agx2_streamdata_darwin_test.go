//go:build darwin

package xcodebindings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// TestAGX2StreamDataConstruction records the construction contract for the
// GTAGX2 Objective-C processors, and how much of their output is reachable.
//
// The contract was not obvious and cost a session to find. The processors take
// an inflated GTShaderProfilerStreamData, not the raw bytes: handing them an
// NSData raises
//
//	-[OS_dispatch_data archivedGPUTimelineData]: unrecognized selector
//
// so the archive has to go through NSKeyedUnarchiver first. That step is the
// whole finding, and it lived only in a loose file under ~/tmp until this test.
//
// What it buys is currently metadata, not data. The processor reports the
// device, the plugin and the object counts, all of which check out against the
// capture. But the unarchived timeline, shader-profiler and counter payloads
// come back empty, and timelineInfo answers -description rather than anything
// enumerable: the accessors beyond this point return C++ internals with no
// Objective-C selector to reach them. So this test asserts the parts that
// answer and logs the parts that do not, rather than reporting the chain as a
// way to extract counters. It is a starting point for the bridge, not a bridge.
//
// Manual: set GPUTRACE_AGX2_STREAMDATA to a profiler streamData archive. The
// capture this was established on has 24 command buffers and 23 encoders.
func TestAGX2StreamDataConstruction(t *testing.T) {
	streamPath := os.Getenv("GPUTRACE_AGX2_STREAMDATA")
	if streamPath == "" {
		t.Skip("set GPUTRACE_AGX2_STREAMDATA to a profiler streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(streamPath); err != nil {
		t.Skipf("streamData unavailable: %v", err)
	}

	// Autorelease pools are thread affine and a goroutine may be migrated
	// between threads, which drains the pool under the objects still in use.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		inspectAGX2StreamData(t, streamPath)
	})
}

func inspectAGX2StreamData(t *testing.T, streamPath string) {
	raw, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatalf("read streamData: %v", err)
	}
	t.Logf("streamData: %d bytes", len(raw))

	target := gtshaderprofiler.GetGTShaderProfilerStreamDataClass().Class()
	unarchived, err := foundation.GetNSKeyedUnarchiverClass().
		UnarchivedObjectOfClassFromDataError(target, foundation.NewDataWithBytesLength(raw))
	if err != nil {
		t.Fatalf("unarchive as GTShaderProfilerStreamData: %v", err)
	}
	if unarchived.GetID() == 0 {
		t.Fatal("unarchive returned nil; the archive is not a GTShaderProfilerStreamData")
	}
	stream := gtshaderprofiler.GTShaderProfilerStreamDataFromID(unarchived.GetID())

	// These come back populated and are checkable against the capture, so they
	// are the part of the chain worth depending on today.
	cbs := stream.CommandBufferInfoCount()
	encoders := stream.EncoderInfoCount()
	t.Logf("device=%q plugin=%q generation=%d commandBuffers=%d encoders=%d",
		stream.MetalDeviceName(), stream.MetalPluginName(),
		stream.GpuGeneration(), cbs, encoders)
	if cbs == 0 || encoders == 0 {
		t.Errorf("commandBuffers=%d encoders=%d, want both nonzero: an inflated "+
			"stream that reports no work means the unarchive silently produced an "+
			"empty object", cbs, encoders)
	}

	// Everything below is the boundary. Log it; do not assert it. Asserting
	// emptiness would freeze the gap in place, and asserting content would fail
	// for a reason nobody could act on.
	for _, p := range []struct {
		name string
		id   objc.ID
	}{
		{"unarchivedGPUTimelineData", stream.UnarchivedGPUTimelineData().GetID()},
		{"unarchivedShaderProfilerData", stream.UnarchivedShaderProfilerData().GetID()},
	} {
		if p.id == 0 {
			t.Logf("%s: nil", p.name)
			continue
		}
		t.Logf("%s: %s", p.name, objc.Send[string](p.id, objc.Sel("className")))
	}

	proc := gtshaderprofiler.GetGTAGX2StreamDataTimelineProcessorClass().Alloc().
		InitWithStreamData(stream)
	proc.ProcessStreamData()
	info := proc.TimelineInfo()
	if info.GetID() == 0 {
		t.Log("timelineInfo: nil after processStreamData")
		return
	}
	// DYWorkloadGPUTimelineInfo answers ordinary Objective-C selectors: nm on
	// the framework lists 36, and every one below is answered. The generated
	// bindings not exposing the class is not the same thing as the contents
	// being unreachable, so do not conclude the second from the first.
	//
	// They are all empty here, which is a different and more useful finding.
	// timeBaseNumerator/Denominator come back 0/0, and a zero timebase is not
	// a value the framework would compute -- the object was never filled.
	//
	// The reason is already recorded in timeline_durations_darwin_test.go: the
	// raw sibling files resolve only when the framework is handed the archive
	// *directory* and _setupDataPath has run. The framework agrees, since
	// APSTraceDataHelper::LoadProfilingData takes an NSURL and
	// APSTraceDataHelper::LoadGPUTimeline is what fills the timeline. Feeding
	// streamData alone skips both. That is the next experiment, and it is why
	// this test asserts nothing about these selectors.
	for _, sel := range []string{
		"perRingSampledDerivedCounters",
		"derivedEncoderCounterInfo",
		"counterGroups",
		"coalescedEncoderInfo",
		"coreCounts",
		"mGPUTimelineInfos",
	} {
		if !objc.RespondsToSelector(info.GetID(), objc.Sel(sel)) {
			t.Logf("%-32s not answered", sel)
			continue
		}
		got := objc.Send[objc.ID](info.GetID(), objc.Sel(sel))
		if got == 0 {
			t.Logf("%-32s nil", sel)
			continue
		}
		t.Logf("%-32s count=%d", sel, objc.Send[uint64](got, objc.Sel("count")))
	}
	// Read the timebase last: it is the cheapest proof of whether anything was
	// populated at all.
	t.Logf("timebase %d/%d",
		objc.Send[uint32](info.GetID(), objc.Sel("timeBaseNumerator")),
		objc.Send[uint32](info.GetID(), objc.Sel("timeBaseDenominator")))
}
