//go:build darwin

package xcodebindings

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"unsafe"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// mioKickTrace is the layout the framework publishes for the read-only kicks
// pointer: ^{GTMioKickTrace=QQIIIIIISSS}. The final two bytes are ABI padding.
// The member meanings are not established.
type mioKickTrace struct {
	Q0     uint64
	Q1     uint64
	Fields [6]uint32
	Flags  [3]uint16
	_      [2]byte
}

var _ [48 - unsafe.Sizeof(mioKickTrace{})]byte
var _ [unsafe.Sizeof(mioKickTrace{}) - 48]byte

// TestAPSDataClasses identifies the objects from which Xcode constructs its
// APS processor. It does not construct a processor: knowing the archive's
// concrete class is a prerequisite to choosing that construction path.
func TestAPSDataClasses(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		stream, err := loadStreamData(filepath.Dir(streamPath))
		if err != nil {
			t.Fatalf("load streamData directory: %v", err)
		}
		checkSelector(t, stream, "unarchivedAPSData", reflect.TypeOf(objc.ID(0)))
		apsData := objc.Send[objc.ID](stream, objc.Sel("unarchivedAPSData"))
		if apsData == 0 {
			t.Log("unarchivedAPSData=nil")
			return
		}
		checkSelector(t, apsData, "count", reflect.TypeOf(uint(0)))
		checkSelector(t, apsData, "objectAtIndex:", reflect.TypeOf(objc.ID(0)), uint(0))
		classes := make(map[string]int)
		for i, n := uint(0), objc.Send[uint](apsData, objc.Sel("count")); i < n; i++ {
			id := objc.Send[objc.ID](apsData, objc.Sel("objectAtIndex:"), i)
			classes[className(id)]++
		}
		names := make([]string, 0, len(classes))
		for name := range classes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			t.Logf("unarchivedAPSData class=%s count=%d", name, classes[name])
		}
		limit := uint64(5)
		if os.Getenv("GPUTRACE_APS_DATA_ALL") == "1" {
			limit = uint64(objc.Send[uint](apsData, objc.Sel("count")))
		}
		for _, sample := range objectSamples(apsData, limit) {
			t.Logf("unarchivedAPSData[%d] class=%s keys=%v children=%v", sample.Index, sample.ClassName, sample.Keys, sample.Children)
		}
	})
}

// TestTimelineSignpostCounts reports the populated cost timeline's scalar
// kick and signpost counts. It intentionally does not read the corresponding
// pointer arrays: their element layouts are not established.
func TestTimelineSignpostCounts(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(streamPath); err != nil {
		t.Skipf("streamData unavailable: %v", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		stream, err := loadStreamData(filepath.Dir(streamPath))
		if err != nil {
			t.Fatalf("load streamData directory: %v", err)
		}
		if objc.Send[objc.ID](stream, objc.Sel("_setupDataPath")) == 0 {
			t.Fatal("_setupDataPath returned nil")
		}
		helper, err := llvmHelperPath()
		if err != nil {
			t.Fatalf("locate GTLLVMHelper: %v", err)
		}
		processor, err := newStreamDataProcessor(stream, helper)
		if err != nil {
			t.Fatalf("new processor: %v", err)
		}
		defer objc.Send[objc.ID](processor, objc.Sel("release"))
		for _, selector := range []string{
			"processStreamData", "processShaderProfilerStreamData", "processTimelineStreamData",
			"waitUntilShaderProfilerFinished", "waitUntilTimelineFinished", "waitUntilFinished",
		} {
			objc.Send[objc.ID](processor, objc.Sel(selector))
		}
		mio := objc.Send[objc.ID](processor, objc.Sel("mioData"))
		if mio == 0 {
			t.Fatal("mioData returned nil")
		}
		timeline := openCostTimeline(t, mio, stream)
		defer objc.Send[objc.ID](timeline, objc.Sel("release"))

		counts := []struct {
			name string
			get  func(gtshaderprofiler.GTMioTraceTimelineData) uint64
		}{
			{"kicksCount", func(v gtshaderprofiler.GTMioTraceTimelineData) uint64 { return v.KicksCount() }},
			{"signpostProcessCount", func(v gtshaderprofiler.GTMioTraceTimelineData) uint64 { return v.SignpostProcessCount() }},
			{"signpostPipelineStateCount", func(v gtshaderprofiler.GTMioTraceTimelineData) uint64 { return v.SignpostPipelineStateCount() }},
			{"signpostShaderCount", func(v gtshaderprofiler.GTMioTraceTimelineData) uint64 { return v.SignpostShaderCount() }},
		}
		model := gtshaderprofiler.GTMioTraceTimelineDataFromID(timeline)
		for _, count := range counts {
			checkSelector(t, timeline, count.name, reflect.TypeOf(uint64(0)))
			t.Logf("%s=%d", count.name, count.get(model))
		}
		checkSelector(t, timeline, "kicks", reflect.TypeOf(unsafe.Pointer(nil)))
		kicks := model.Kicks()
		if kicks == nil && model.KicksCount() != 0 {
			t.Fatal("kicks returned nil with a non-zero count")
		}

		helperClass := gtshaderprofiler.GetGTMioTraceDataHelperClass()
		traceHelper := helperClass.Alloc().InitWithTraceData(gtshaderprofiler.GTMioTraceDataFromID(mio))
		if traceHelper.GetID() == 0 {
			t.Fatal("GTMioTraceDataHelper initWithTraceData: returned nil")
		}
		defer objc.Send[objc.ID](traceHelper.GetID(), objc.Sel("release"))
		checkSelector(t, traceHelper.GetID(), "generateTopKickTracks", reflect.TypeOf(objc.ID(0)))
		tracksID := traceHelper.GenerateTopKickTracks().GetID()
		checkSelector(t, tracksID, "count", reflect.TypeOf(uint(0)))
		checkSelector(t, tracksID, "objectAtIndex:", reflect.TypeOf(objc.ID(0)), uint(0))
		trackCount := objc.Send[uint](tracksID, objc.Sel("count"))
		t.Logf("topKickTracks=%d", trackCount)
		seenKick := make(map[uint64]int)
		for i := uint(0); i < trackCount; i++ {
			trackID := objc.Send[objc.ID](tracksID, objc.Sel("objectAtIndex:"), i)
			track := gtshaderprofiler.GTMioTraceTrackFromID(trackID)
			checkSelector(t, trackID, "startTimestamp", reflect.TypeOf(uint64(0)))
			checkSelector(t, trackID, "endTimestamp", reflect.TypeOf(uint64(0)))
			checkSelector(t, trackID, "duration", reflect.TypeOf(uint64(0)))
			checkSelector(t, trackID, "trackId", reflect.TypeOf(int(0)))
			checkSelector(t, trackID, "lanes", reflect.TypeOf(objc.ID(0)))
			lanes := track.Lanes()
			checkSelector(t, lanes.GetID(), "count", reflect.TypeOf(uint(0)))
			t.Logf("topKickTrack[%d] id=%d start=%d end=%d duration=%d lanes=%d",
				i, track.TrackId(), track.StartTimestamp(), track.EndTimestamp(), track.Duration(), lanes.Count())
			for j := uint(0); j < lanes.Count(); j++ {
				laneID := lanes.ObjectAtIndex(j).GetID()
				checkSelector(t, laneID, "indexCount", reflect.TypeOf(uint64(0)))
				checkSelector(t, laneID, "indexes", reflect.TypeOf(unsafe.Pointer(nil)))
				count := gtshaderprofiler.GTMioTraceTrackLaneFromID(laneID).IndexCount()
				indexes := gtshaderprofiler.GTMioTraceTrackLaneFromID(laneID).Indexes()
				if count == 0 {
					t.Logf("topKickTrack[%d].lane[%d] indexes=0", i, j)
					continue
				}
				if indexes == nil {
					t.Fatalf("topKickTrack[%d].lane[%d] has %d indexes but nil data", i, j, count)
				}
				values := unsafe.Slice((*uint64)(indexes), int(count))
				low, high := values[0], values[0]
				for _, index := range values {
					if index < low {
						low = index
					}
					if index > high {
						high = index
					}
					seenKick[index]++
				}
				t.Logf("topKickTrack[%d].lane[%d] indexes=%d range=%d..%d", i, j, count, low, high)
			}
		}
		var duplicate, outOfRange int
		for index, count := range seenKick {
			if count > 1 {
				duplicate++
			}
			if index >= model.KicksCount() {
				outOfRange++
			}
		}
		t.Logf("topKickLaneCoverage unique=%d duplicates=%d out_of_range=%d", len(seenKick), duplicate, outOfRange)
		if len(seenKick) != int(model.KicksCount()) || duplicate != 0 || outOfRange != 0 {
			t.Fatalf("top-kick lanes do not partition kicks: unique=%d want=%d duplicates=%d out_of_range=%d",
				len(seenKick), model.KicksCount(), duplicate, outOfRange)
		}
	})
}
