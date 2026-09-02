//go:build darwin

package xcodebindings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/objc"
)

// TestUSCTraceDataProbe records why the USC trace data is unreachable from a
// processor-built model, and what it would give if it were reachable.
//
// GTMioUSCTraceData is the class that implements -databaseInternal (Q16@0:8, an
// opaque handle), which GTMioTraceData does not implement at all. That is what
// -[GTMioTraceDataStats initWithTraceData:] is asking for when it throws
// "databaseInternal unrecognized": it wants a database-backed trace data, not
// the GTMioTraceData a stream processor produces. The class it leads to,
// GTMioTraceDataShaderStat, reports numberOfCliques, totalLatency and
// totalGPUCycles per shader, and GTMioUSCTraceData itself joins cliques to
// pipelines structurally through -pipelineStateIdForCliqueAtIndex: and
// -firstBinaryIndexForCliqueAtIndex: — a per-kernel attribution that needs no
// MCA analysis and so cannot race it.
//
// None of that is reachable here. On a processor-built model, reading -uscs
// segfaults: the property's encoding is @16@0:8 and -respondsToSelector: is
// true, but the getter dereferences the absent database. This is the sharper
// form of the object-versus-pointer trap — a declared object return is
// necessary but not sufficient, and the only way to learn the difference is to
// lose the process.
//
// The probe is gated separately from GPUTRACE_PROCESS_STREAMDATA precisely
// because it crashes the test binary rather than failing it.
func TestUSCTraceDataProbe(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" || os.Getenv("GPUTRACE_MIO_USC_PROBE") == "" {
		t.Skip("set GPUTRACE_MIO_USC_PROBE and GPUTRACE_PROCESS_STREAMDATA; this probe is expected to crash")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		// Load the archive directory rather than its inner streamData file, then
		// run the data-path setup, which is what lets the APS passes resolve the
		// sibling raw files. Without it the cost model stays empty.
		loadPath := streamPath
		if filepath.Base(loadPath) == "streamData" {
			loadPath = filepath.Dir(loadPath)
		}
		stream, err := loadStreamData(loadPath)
		if err != nil {
			t.Fatalf("load streamData: %v", err)
		}
		if responds(stream, "_setupDataPath") {
			t.Logf("_setupDataPath -> %v", objc.Send[objc.ID](stream, objc.Sel("_setupDataPath")) != 0)
		}
		helper, err := llvmHelperPath()
		if err != nil {
			t.Fatalf("locate helper: %v", err)
		}
		processor, err := newStreamDataProcessor(stream, helper)
		if err != nil {
			t.Fatalf("new processor: %v", err)
		}
		defer objc.Send[objc.ID](processor, objc.Sel("release"))

		for _, sel := range []string{"processStreamData", "processShaderProfilerStreamData", "processTimelineStreamData"} {
			objc.Send[objc.ID](processor, objc.Sel(sel))
		}
		for _, sel := range []string{"waitUntilShaderProfilerFinished", "waitUntilTimelineFinished", "waitUntilFinished"} {
			objc.Send[objc.ID](processor, objc.Sel(sel))
		}
		mio := objc.Send[objc.ID](processor, objc.Sel("mioData"))
		if mio == 0 {
			t.Fatal("mioData returned nil")
		}

		// Log before each call so a crash names the selector that caused it.
		// On the tested capture this does not survive "uscs".
		for _, sel := range []string{"uscs", "mGPUs", "timelineCounters"} {
			t.Logf("reading mio.%s", sel)
			if !responds(mio, sel) {
				t.Logf("mio.%s: no response", sel)
				continue
			}
			t.Logf("mio.%s count=%d", sel, collectionCount(mio, sel))
		}

		// Each USC object is one GPU core. The clique accessors are the
		// structural pipeline-to-binary join: unlike the MCA binary keys they
		// are indexes into archived metadata, so they cannot race an analysis.
		for i, usc := range elementsOf(objectFor(mio, "uscs")) {
			cliques := uint64Property(usc, "cliquesCount")
			t.Logf("usc[%d] databaseInternal=%#x cliques=%d kicks=%d tiles=%d costCount=%d drawTrace=%d binaryTrace=%d",
				i, uint64Property(usc, "databaseInternal"), cliques,
				uint64Property(usc, "kicksCount"), uint64Property(usc, "tilesCount"),
				uint64Property(usc, "costCount"), uint64Property(usc, "drawTraceCount"),
				uint64Property(usc, "binaryTraceCount"))
			if cliques == 0 || i > 1 {
				continue
			}
			for c := uint64(0); c < min(cliques, 6); c++ {
				t.Logf("  clique[%d] pipelineStateId=%#x firstBinaryIndex=%d firstPC=%#x",
					c,
					objc.Send[uint64](usc, objc.Sel("pipelineStateIdForCliqueAtIndex:"), uint32(c)),
					objc.Send[uint32](usc, objc.Sel("firstBinaryIndexForCliqueAtIndex:"), uint32(c)),
					objc.Send[uint64](usc, objc.Sel("firstPCForCliqueAtIndex:"), uint32(c)))
			}
		}

		// GTMioTraceDataStats threw "databaseInternal unrecognized" for the
		// GTMioTraceData, which does not implement that selector. The USC
		// objects do, and their handles are real, so they are what it wants.
		//
		// Gated separately: with the data path set up and 260k cliques per core,
		// this section crashes the process rather than returning. It is safe
		// only on a model built without the data-path setup, where -build has
		// nothing to aggregate.
		if os.Getenv("GPUTRACE_MIO_USC_STATS") == "" {
			t.Log("skipping GTMioTraceDataStats; set GPUTRACE_MIO_USC_STATS (expected to crash on a populated model)")
			return
		}
		uscs := elementsOf(objectFor(mio, "uscs"))
		if len(uscs) == 0 {
			return
		}
		statsCls := objc.GetClass("GTMioTraceDataStats")
		if statsCls == 0 {
			t.Log("GTMioTraceDataStats class not found")
			return
		}
		allocated := objc.Send[objc.ID](objc.ID(statsCls), objc.Sel("alloc"))
		stats := objc.Send[objc.ID](allocated, objc.Sel("initWithTraceData:"), uscs[0])
		if stats == 0 {
			t.Log("GTMioTraceDataStats initWithTraceData: on a USC object returned nil")
			return
		}
		t.Log("GTMioTraceDataStats accepted a USC trace data")
		if responds(stats, "build") {
			objc.Send[objc.ID](stats, objc.Sel("build"))
			t.Log("build returned")
		}
		// shaderStatForShader:programType: is Q16/S24; the shader identifier is
		// a pipeline state ID, which readPipelines already reports.
		for _, shader := range []uint64{0xaac, 0xaa8, 0xab2} {
			stat := objc.Send[objc.ID](stats, objc.Sel("shaderStatForShader:programType:"), shader, uint16(0))
			if stat == 0 {
				t.Logf("shaderStatForShader:%#x -> nil", shader)
				continue
			}
			t.Logf("shaderStatForShader:%#x cliques=%d totalLatency=%d totalGPUCycles=%d",
				shader, uint64Property(stat, "numberOfCliques"),
				uint64Property(stat, "totalLatency"), uint64Property(stat, "totalGPUCycles"))
		}
		objc.Send[objc.ID](stats, objc.Sel("release"))
	})
}
