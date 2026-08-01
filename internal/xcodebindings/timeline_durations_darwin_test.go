//go:build darwin

package xcodebindings

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"unsafe"

	puregoobjc "github.com/ebitengine/purego/objc"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objc/objcinspect"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// TestTimelineDrawDurations reads every per-draw duration from Xcode's
// serialized cost timeline rather than the three the exported summary samples,
// and checks the total against the profiler's own gpuTime.
//
// The exported TimelineSummary reports the first three durations, which shows
// the selector answers but says nothing about what the numbers mean. If the
// 574 draw durations sum to gpuTime then they are per-dispatch GPU time in the
// same unit the result already reports, which is the difference between a
// structural count and usable timing. The sweep over data masters is here for
// the same reason: dataMaster 2 was chosen from a working example, not from a
// documented enumeration.
func TestTimelineDrawDurations(t *testing.T) {
	streamPath := os.Getenv("GPUTRACE_PROCESS_STREAMDATA")
	if streamPath == "" {
		t.Skip("set GPUTRACE_PROCESS_STREAMDATA to a profiler streamData archive")
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
		measureDrawDurations(t, streamPath)
	})
}

func measureDrawDurations(t *testing.T, streamPath string) {
	// The raw sibling files are resolved only when the archive directory is the
	// URL handed to the framework, and only after _setupDataPath runs. This
	// mirrors processStreamData so the two configurations can be compared.
	setupPath := os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1"
	loadPath := streamPath
	if setupPath && filepath.Base(loadPath) == "streamData" {
		loadPath = filepath.Dir(loadPath)
	}
	stream, err := loadStreamData(loadPath)
	if err != nil {
		t.Fatalf("load streamData: %v", err)
	}
	if setupPath {
		if objc.Send[objc.ID](stream, objc.Sel("_setupDataPath")) == 0 {
			t.Fatal("_setupDataPath returned nil")
		}
	}
	t.Logf("loadPath=%s setupDataPath=%v", loadPath, setupPath)
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
	gpuTime := uint64Property(shaderProfilerResult(processor), "gpuTime")
	drawCount := uint64Property(mio, "drawCount")
	t.Logf("model draws=%d gpuTime=%d", drawCount, gpuTime)

	timeline := openCostTimeline(t, mio, stream)
	defer objc.Send[objc.ID](timeline, objc.Sel("release"))
	checkTimelineCounters(t, timeline)

	if !responds(timeline, "durationForDraw:dataMaster:") {
		t.Fatal("timeline does not respond to durationForDraw:dataMaster:")
	}
	// The data master selects which of the profiler's parallel measurement
	// streams answers. Only one is expected to carry durations; reading the
	// neighbours is what shows that 2 is a choice rather than a coincidence.
	for master := uint16(0); master < 4; master++ {
		var total, nonZero, max uint64
		for i := uint64(0); i < drawCount; i++ {
			d := objc.Send[uint64](timeline, objc.Sel("durationForDraw:dataMaster:"), uint32(i), master)
			total += d
			if d != 0 {
				nonZero++
			}
			if d > max {
				max = d
			}
		}
		t.Logf("dataMaster=%d draws=%d nonzero=%d total=%d max=%d", master, drawCount, nonZero, total, max)
		if master == 2 && gpuTime != 0 {
			t.Logf("  total/gpuTime = %.6f", float64(total)/float64(gpuTime))
		}
	}

	// Per-pipeline totals are the metric that would matter downstream: draw
	// durations are only useful if they attribute to a kernel.
	if responds(timeline, "numDrawsForPipelineState:") {
		for _, pipeline := range readPipelines(shaderProfilerResult(processor)) {
			draws := objc.Send[uint64](timeline, objc.Sel("numDrawsForPipelineState:"), pipeline.ObjectID)
			if draws == 0 {
				continue
			}
			t.Logf("pipeline %#x draws=%d %s", pipeline.ObjectID, draws, pipeline.FunctionName)
		}
	}
}

// checkTimelineCounters records the runtime counter-name join carried by the
// populated cost timeline. The reference archive exposes thirty named counters
// here, unlike the empty non-overlapping-counter model. This is the boundary
// where a caller can stop guessing how opaque archive hashes map to names.
func checkTimelineCounters(t *testing.T, timeline objc.ID) {
	t.Helper()
	check := func(id objc.ID, selector string, want reflect.Type, args ...any) {
		t.Helper()
		if err := objcinspect.Check(puregoobjc.ID(uintptr(id)), puregoobjc.RegisterName(selector), want, args...); err != nil {
			t.Fatalf("%s type check: %v", selector, err)
		}
	}

	check(timeline, "timelineCounters", reflect.TypeOf(objc.ID(0)))
	counters := gtshaderprofiler.GTMioTraceTimelineDataFromID(timeline).TimelineCounters()
	if counters.GetID() == 0 {
		t.Fatal("timelineCounters returned nil")
	}
	check(counters.GetID(), "counters", reflect.TypeOf(objc.ID(0)))
	dictionary := counters.Counters()
	if dictionary.GetID() == 0 {
		t.Fatal("GTMioTimelineCounters counters returned nil")
	}
	check(dictionary.GetID(), "count", reflect.TypeOf(uint(0)))
	if got := dictionary.Count(); got == 0 {
		t.Fatal("timeline counter dictionary is empty")
	} else {
		t.Logf("timeline counter dictionary entries=%d", got)
	}

	name := foundation.NewStringWithString("ALU Total Instructions")
	check(counters.GetID(), "counterForName:", reflect.TypeOf(objc.ID(0)), name)
	counterID := counters.CounterForName(name).GetID()
	if counterID == 0 {
		t.Fatal("counterForName(ALU Total Instructions) returned nil")
	}
	counter := gtshaderprofiler.GTMioCounterDataFromID(counterID)
	check(counterID, "name", reflect.TypeOf(objc.ID(0)))
	check(counterID, "sampleCount", reflect.TypeOf(uint64(0)))
	// The generated binding returns the runtime's ^d pointer. SampleCount is the
	// independently checked bound, and the copy keeps the series valid after
	// this Objective-C autorelease pool drains.
	check(counterID, "values", reflect.TypeOf(unsafe.Pointer(nil)))
	if got, want := counter.Name(), "ALU Total Instructions"; got != want {
		t.Fatalf("counter name = %q, want %q", got, want)
	}
	if got := counter.SampleCount(); got == 0 {
		t.Fatal("ALU Total Instructions sample count is zero")
	} else {
		t.Logf("ALU Total Instructions samples=%d", got)
		check(counterID, "timestamps", reflect.TypeOf(unsafe.Pointer(nil)))
		stamps := unsafe.Slice((*uint64)(counter.Timestamps()), int(got))
		if stamps[0] == 0 || stamps[len(stamps)-1] == 0 {
			t.Fatalf("counter timestamps have zero endpoint: first=%d last=%d", stamps[0], stamps[len(stamps)-1])
		}
		for i := 1; i < len(stamps) && i < 1024; i++ {
			if stamps[i] < stamps[i-1] {
				t.Fatalf("counter timestamps descend at %d: %d < %d", i, stamps[i], stamps[i-1])
			}
		}
		t.Logf("ALU Total Instructions timestamp range=%d..%d", stamps[0], stamps[len(stamps)-1])

		valuesPointer := counter.Values()
		if valuesPointer == nil {
			t.Fatal("ALU Total Instructions values returned nil")
		}
		values := append([]float64(nil), unsafe.Slice((*float64)(valuesPointer), int(got))...)
		low, high, sum := math.Inf(1), math.Inf(-1), 0.0
		for i, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("ALU Total Instructions value[%d] is not finite: %g", i, value)
			}
			low = math.Min(low, value)
			high = math.Max(high, value)
			sum += value
		}
		if sum == 0 {
			t.Fatal("ALU Total Instructions values sum to zero")
		}
		edge := min(len(values), 10)
		t.Logf("ALU Total Instructions value range=%g..%g sum=%g mean=%g first=%v last=%v",
			low, high, sum, sum/float64(len(values)), values[:edge], values[len(values)-edge:])
	}
}

// openCostTimeline rebuilds the timeline through the archive seam: the live
// model does not answer duration selectors, but its own serialized costTimeline
// child does.
func openCostTimeline(t *testing.T, mio, stream objc.ID) objc.ID {
	t.Helper()
	var archiveError objc.ID
	data := objc.Send[objc.ID](mio, objc.Sel("archivedData:error:"), false, unsafe.Pointer(&archiveError))
	if data == 0 {
		t.Fatalf("archivedData: nil (error %#x)", archiveError)
	}
	kv := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(objc.GetClass("GTMioKVDataStore")), objc.Sel("alloc")),
		objc.Sel("initWithData:"), data)
	if kv == 0 {
		t.Fatal("GTMioKVDataStore initWithData: nil")
	}
	child := objc.Send[objc.ID](kv, objc.Sel("getChild:"), objc.String("costTimeline"))
	if child == 0 {
		t.Fatal("no costTimeline child")
	}
	timeline := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("GTMioTraceTimelineData")), objc.Sel("alloc")),
		objc.Sel("initWithSerializedData:streamData:parentData:"), child, stream, mio)
	if timeline == 0 {
		t.Fatal("GTMioTraceTimelineData initWithSerializedData: nil")
	}
	return timeline
}

// drawMetadata mirrors GTMioDrawMetadata, whose Objective-C type encoding is
// ^{GTMioDrawMetadata=IIIIiIQIII}: six 32-bit fields, a 64-bit field that
// natural alignment places at offset 24, then three more 32-bit fields, for a
// 48-byte record.
//
// This is a raw C array rather than an object collection, so it is read as
// memory and never messaged. The field names are unknown; they are numbered.
type drawMetadata struct {
	F0, F4, F8, F12 uint32
	F16             int32
	F20             uint32
	F24             uint64
	F32, F36, F40   uint32
}

// TestDrawPipelineEdge looks for the field of GTMioDrawMetadata that identifies
// the draw's pipeline.
//
// durationForDraw:dataMaster: gives per-draw GPU time and
// numDrawsForPipelineState: gives each pipeline's share of the draws, but
// nothing published joins the two, so per-kernel GPU time is still out of
// reach. The join would be one field of the draw record.
//
// The search validates itself: bucketing 574 draws by the correct field must
// reproduce the eighteen counts numDrawsForPipelineState: already reports. A
// field that merely looks plausible will not match that multiset, which is the
// check the earlier binary-index and MCA-key routes could not offer.
func TestDrawPipelineEdge(t *testing.T) {
	streamPath := os.Getenv("GPUTRACE_PROCESS_STREAMDATA")
	if streamPath == "" {
		t.Skip("set GPUTRACE_PROCESS_STREAMDATA to a profiler streamData archive")
	}
	if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") != "1" {
		t.Skip("set GPUTRACE_MIO_SETUP_DATA_PATH=1: draw durations are zero without it")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		findDrawPipelineEdge(t, streamPath)
	})
}

func findDrawPipelineEdge(t *testing.T, streamPath string) {
	stream, err := loadStreamData(filepath.Dir(streamPath))
	if err != nil {
		t.Fatalf("load streamData: %v", err)
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

	drawCount := uint64Property(timeline, "drawCount")
	pipelines := readPipelines(shaderProfilerResult(processor))

	// The target multiset: what the framework itself says each pipeline's draw
	// count is.
	want := map[uint64]uint64{}
	var wantSizes []uint64
	for _, pipeline := range pipelines {
		n := objc.Send[uint64](timeline, objc.Sel("numDrawsForPipelineState:"), pipeline.ObjectID)
		if n == 0 {
			continue
		}
		want[pipeline.ObjectID] = n
		wantSizes = append(wantSizes, n)
	}
	sort.Slice(wantSizes, func(i, j int) bool { return wantSizes[i] > wantSizes[j] })
	t.Logf("draws=%d pipelines=%d wantSizes=%v", drawCount, len(want), wantSizes)

	// Taken as unsafe.Pointer rather than uintptr: the array must stay a live
	// pointer for the whole read, and a uintptr round trip would not keep it so.
	base := objc.Send[*drawMetadata](timeline, objc.Sel("draws"))
	if base == nil {
		t.Fatal("draws returned a null pointer")
	}
	records := unsafe.Slice(base, drawCount)
	t.Logf("record[0] = %+v", records[0])
	t.Logf("record[1] = %+v", records[1])

	// The 48-byte stride implied by C alignment misreads every record after the
	// first, so the layout is located rather than assumed: the pipeline object
	// ids are known, so the bytes are scanned for them and the hit offsets are
	// reduced modulo each candidate stride. The true stride is the one where
	// every hit shares a single residue, and that residue is the field offset.
	//
	// The scan is bounded to drawCount*44 bytes, the smallest stride worth
	// considering, so it cannot read past the array.
	ids := map[uint64]bool{}
	for id := range want {
		ids[id] = true
	}
	const minStride = 44
	span := int(drawCount) * minStride
	raw := unsafe.Slice((*byte)(unsafe.Pointer(base)), span)

	var hits32, hits64 []int
	for off := 0; off+8 <= span; off += 4 {
		if ids[uint64(binary.LittleEndian.Uint32(raw[off:]))] {
			hits32 = append(hits32, off)
		}
		if ids[binary.LittleEndian.Uint64(raw[off:])] {
			hits64 = append(hits64, off)
		}
	}
	t.Logf("pipeline-id hits: %d as uint32, %d as uint64 (in %d bytes)", len(hits32), len(hits64), span)
	for _, probe := range []struct {
		name string
		hits []int
	}{{"uint32", hits32}, {"uint64", hits64}} {
		if len(probe.hits) == 0 {
			continue
		}
		t.Logf("  %s first offsets: %v", probe.name, headInt(probe.hits, 10))
		for stride := 32; stride <= 72; stride += 4 {
			residues := map[int]int{}
			for _, off := range probe.hits {
				residues[off%stride]++
			}
			if len(residues) != 1 {
				continue
			}
			for residue, n := range residues {
				t.Logf("  ** %s: stride=%d field offset=%d covers %d/%d draws **",
					probe.name, stride, residue, n, drawCount)
				verifyStride(t, timeline, base, stride, residue, probe.name == "uint64", want, pipelines)
			}
		}
	}
}

// verifyStride confirms a located layout by bucketing every draw and comparing
// against numDrawsForPipelineState:, then totals GPU time per kernel.
func verifyStride(t *testing.T, timeline objc.ID, base *drawMetadata, stride, offset int,
	wide bool, want map[uint64]uint64, pipelines []PipelineRecord) {
	drawCount := len(want)
	_ = drawCount
	total := uint64Property(timeline, "drawCount")
	raw := unsafe.Slice((*byte)(unsafe.Pointer(base)), int(total)*stride)
	get := func(i int) uint64 {
		b := raw[i*stride+offset:]
		if wide {
			return binary.LittleEndian.Uint64(b)
		}
		return uint64(binary.LittleEndian.Uint32(b))
	}
	buckets := map[uint64]uint64{}
	for i := 0; i < int(total); i++ {
		buckets[get(i)]++
	}
	for id, n := range want {
		if buckets[id] != n {
			t.Logf("     rejected: pipeline %#x bucketed %d, framework says %d", id, buckets[id], n)
			return
		}
	}
	if len(buckets) != len(want) {
		t.Logf("     rejected: %d distinct values, want %d", len(buckets), len(want))
		return
	}
	t.Logf("     CONFIRMED: all %d pipeline draw counts reproduced", len(want))
	names := map[uint64]string{}
	for _, p := range pipelines {
		names[p.ObjectID] = p.FunctionName
	}
	totals := map[uint64]uint64{}
	var grand uint64
	for i := 0; i < int(total); i++ {
		d := objc.Send[uint64](timeline, objc.Sel("durationForDraw:dataMaster:"), uint32(i), uint16(2))
		totals[get(i)] += d
		grand += d
	}
	ids := make([]uint64, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return totals[ids[i]] > totals[ids[j]] })
	t.Logf("     per-kernel GPU time (dataMaster 2), total %d:", grand)
	for _, id := range ids {
		t.Logf("       %6.2f%%  %10d  n=%-4d %#x %s",
			100*float64(totals[id])/float64(grand), totals[id], want[id], id, names[id])
	}
}

func headInt(v []int, n int) []int {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// reportPerKernelTime totals each kernel's GPU time once the draw records are
// known to carry its pipeline.
func reportPerKernelTime(t *testing.T, timeline objc.ID, records []drawMetadata,
	get func(drawMetadata) uint64, pipelines []PipelineRecord) {
	names := map[uint64]string{}
	for _, pipeline := range pipelines {
		names[pipeline.ObjectID] = pipeline.FunctionName
	}
	totals := map[uint64]uint64{}
	counts := map[uint64]uint64{}
	var grand uint64
	for i, record := range records {
		d := objc.Send[uint64](timeline, objc.Sel("durationForDraw:dataMaster:"), uint32(i), uint16(2))
		totals[get(record)] += d
		counts[get(record)]++
		grand += d
	}
	ids := make([]uint64, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return totals[ids[i]] > totals[ids[j]] })
	t.Logf("per-kernel GPU time (dataMaster 2), total %d:", grand)
	for _, id := range ids {
		t.Logf("  %6.2f%%  %10d  n=%-4d %#x %s",
			100*float64(totals[id])/float64(grand), totals[id], counts[id], id, names[id])
	}
}

func head(v []uint64, n int) []uint64 {
	if len(v) > n {
		return v[:n]
	}
	return v
}
