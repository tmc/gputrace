//go:build darwin

package xcodebindings

import (
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"

	puregoobjc "github.com/ebitengine/purego/objc"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objc/objcinspect"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// TestTimelineInfoSelectorTypes checks each selector this package reads off
// DYWorkloadGPUTimelineInfo against the type encoding the runtime reports,
// before anything sends it.
//
// objc.Send is unchecked: it reinterprets whatever comes back as the requested
// Go type. Reading -description, which returns an id, as a string yields the
// bytes of a pointer, and those bytes print as plausible-looking garbage. That
// happened here, and the garbage was quoted onward as if it were a real
// description. objcinspect.Check catches it without invoking the method.
//
// It also separates the two failures that are easy to confuse and that we did
// confuse: a selector the class does not implement, and one it implements with
// a different return type. Reading garbage out of the second and calling the
// data unreachable is how f06478e came to correct fcc72a1.
//
// The class was reachable the whole time. The bindings generate all 38 of its
// methods; the object is simply empty, for the reason recorded in
// agx2_streamdata_darwin_test.go.
//
// Manual: set GPUTRACE_AGX2_STREAMDATA to a profiler streamData archive.
func TestTimelineInfoSelectorTypes(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_AGX2_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_AGX2_STREAMDATA to a streamData archive")
	}
	raw, err := os.ReadFile(streamPath)
	if err != nil {
		t.Skipf("streamData unavailable: %v", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		target := gtshaderprofiler.GetGTShaderProfilerStreamDataClass().Class()
		unarchived, err := foundation.GetNSKeyedUnarchiverClass().
			UnarchivedObjectOfClassFromDataError(target, foundation.NewDataWithBytesLength(raw))
		if err != nil || unarchived.GetID() == 0 {
			t.Fatalf("unarchive as GTShaderProfilerStreamData: %v", err)
		}
		stream := gtshaderprofiler.GTShaderProfilerStreamDataFromID(unarchived.GetID())
		proc := gtshaderprofiler.GetGTAGX2StreamDataTimelineProcessorClass().Alloc().
			InitWithStreamData(stream)
		proc.ProcessStreamData()
		info := proc.TimelineInfo()
		if info.GetID() == 0 {
			t.Skip("no timelineInfo to inspect")
		}
		id := puregoobjc.ID(uintptr(info.GetID()))

		for _, c := range []struct {
			sel  string
			want reflect.Type
		}{
			{"timeBaseNumerator", reflect.TypeOf(uint32(0))},
			{"timeBaseDenominator", reflect.TypeOf(uint32(0))},
			{"perRingSampledDerivedCounters", reflect.TypeOf(objc.ID(0))},
			{"derivedEncoderCounterInfo", reflect.TypeOf(objc.ID(0))},
			{"counterGroups", reflect.TypeOf(objc.ID(0))},
			{"coalescedEncoderInfo", reflect.TypeOf(objc.ID(0))},
			{"coreCounts", reflect.TypeOf(objc.ID(0))},
			{"mGPUTimelineInfos", reflect.TypeOf(objc.ID(0))},
		} {
			if err := objcinspect.Check(id, puregoobjc.RegisterName(c.sel), c.want); err != nil {
				t.Errorf("%s as %v: %v", c.sel, c.want, err)
			}
		}

		// Negative controls. Without these the test passes just as well against
		// a Check that never reports anything, which is the failure mode a
		// guard like this is most likely to rot into.
		if err := objcinspect.Check(id, puregoobjc.RegisterName("description"), reflect.TypeOf("")); err == nil {
			t.Error("reading -description as a string was accepted; it returns an id, " +
				"and reinterpreting those bytes as a string is what produced the " +
				"garbage that was mistaken for a real description")
		}
		if err := objcinspect.Check(id, puregoobjc.RegisterName("noSuchSelectorOnThisClass"), reflect.TypeOf("")); err == nil {
			t.Error("a selector the class does not implement was accepted")
		}
	})
}
