//go:build darwin

package xcodebindings

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/gputrace/internal/testtrace"

	puregoobjc "github.com/ebitengine/purego/objc"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objc/objcinspect"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
	gtcounter "github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/parity"
)

// TestTimelineDrawDurations reads every per-draw duration from Xcode's
// serialized cost timeline rather than the three the exported summary samples,
// and reports its total beside the profiler's own GPUTime.
//
// The exported TimelineSummary reports the first three durations, which shows
// the selector answers but says nothing about what the numbers mean. On the
// reference capture, Data Master 2's sum differs from GPUTime, so these are
// not published as Xcode GPU Time. The sweep over data masters is here for
// the same reason: dataMaster 2 was chosen from a working example, not from a
// documented enumeration.
func TestTimelineDrawDurations(t *testing.T) {
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
		measureDrawDurations(t, streamPath)
	})
}

// checkSelector fails the test unless the runtime reports that selector on id
// returns want. objc.Send is unchecked and reinterprets whatever comes back as
// the requested Go type, so reading a selector at the wrong type yields bytes
// that print as plausible data; this is what stands between a probe and that.
func checkSelector(t *testing.T, id objc.ID, selector string, want reflect.Type, args ...any) {
	t.Helper()
	if err := objcinspect.Check(puregoobjc.ID(uintptr(id)), puregoobjc.RegisterName(selector), want, args...); err != nil {
		t.Fatalf("%s type check: %v", selector, err)
	}
}

func measureDrawDurations(t *testing.T, streamPath string) {
	check := func(id objc.ID, selector string, want reflect.Type, args ...any) {
		t.Helper()
		checkSelector(t, id, selector, want, args...)
	}

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
	check(mio, "gpuTime", reflect.TypeOf(uint64(0)))
	mioGPUTime := gtshaderprofiler.GTMioTraceDataFromID(mio).GpuTime()
	result := shaderProfilerResult(processor)
	check(result, "gpuTime", reflect.TypeOf(uint64(0)))
	gpuTime := gtshaderprofiler.GTMioShaderProfilerResultFromID(result).GpuTime()
	drawCount := uint64Property(mio, "drawCount")
	t.Logf("model draws=%d mioGPUTime=%d shaderGPUTime=%d", drawCount, mioGPUTime, gpuTime)

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
		checkSelector(t, id, selector, want, args...)
	}

	check(timeline, "timelineCounters", reflect.TypeOf(objc.ID(0)))
	check(timeline, "timestampBegin", reflect.TypeOf(uint64(0)))
	check(timeline, "timestampEnd", reflect.TypeOf(uint64(0)))
	timelineModel := gtshaderprofiler.GTMioTraceTimelineDataFromID(timeline)
	timestampBegin, timestampEnd := timelineModel.TimestampBegin(), timelineModel.TimestampEnd()
	if timestampEnd < timestampBegin {
		t.Fatalf("cost timeline timestamp range=%d..%d", timestampBegin, timestampEnd)
	}
	t.Logf("cost timeline timestamp range=%d..%d", timestampBegin, timestampEnd)
	counters := timelineModel.TimelineCounters()
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
		// An empty dictionary is the documented state without
		// _setupDataPath, not a broken binding: the counters are populated
		// by the directory-backed load. Failing here would report "you did
		// not opt in" as "the runtime join regressed", which is the more
		// alarming of the two and the wrong one.
		if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") != "1" {
			t.Skip("timeline counter dictionary is empty; set GPUTRACE_MIO_SETUP_DATA_PATH=1 to populate it")
		}
		t.Fatal("timeline counter dictionary is empty despite _setupDataPath")
	} else {
		t.Logf("timeline counter dictionary entries=%d", got)
		check(dictionary.GetID(), "allKeys", reflect.TypeOf(objc.ID(0)))
		keys := dictionary.AllKeys()
		if len(keys) != int(got) {
			t.Fatalf("timeline counter keys = %d, want %d", len(keys), got)
		}
		names := make([]string, 0, len(keys))
		for _, key := range keys {
			check(key.GetID(), "UTF8String", reflect.TypeOf((*byte)(nil)))
			names = append(names, foundation.NSStringFromID(key.GetID()).UTF8String())
		}
		sort.Strings(names)
		t.Logf("timeline counter names=%q", names)

		catalog, err := parity.LoadCatalog(parity.CounterGraphPaths())
		if err != nil {
			t.Fatalf("load Xcode counter catalog: %v", err)
		}
		if catalog == nil {
			t.Fatal("Xcode counter catalog is unavailable")
		}
		var catalogNames []string
		for _, name := range names {
			if _, ok := catalog.Lookup(name); ok {
				catalogNames = append(catalogNames, name)
			}
		}
		t.Logf("timeline counter names in catalog=%q (catalog=%s)", catalogNames, catalog.Path)
		_, totalInCatalog := catalog.Lookup("ALU Total Instructions")
		kernelName := foundation.NewStringWithString("Kernel ALU Instructions")
		check(counters.GetID(), "counterForName:", reflect.TypeOf(objc.ID(0)), kernelName)
		kernel := counters.CounterForName(kernelName)
		t.Logf("ALU Total Instructions in catalog=%t; Kernel ALU Instructions runtime counter=%t",
			totalInCatalog, kernel.GetID() != 0)
		reportTimelineCounterMetadata(t, check, counters, names)
	}

	// counterForName: has to be able to answer "no" before the identity
	// comparison below means anything. If it echoed the query into a wrapper
	// over some default series, every name would return a counter, the
	// counter's own name would match the query, and any two names would
	// compare equal. Asking for a name the dictionary cannot hold is what
	// separates a real alias from that.
	absent := counters.CounterForName(foundation.NewStringWithString("ZZNotACounter"))
	if absent.GetID() != 0 {
		t.Fatal("counterForName: returned a counter for a name that is not in the dictionary; " +
			"names do not select a series and the identity check below is vacuous")
	}

	total := readTimelineCounter(t, check, counters, "ALU Total Instructions")
	alu := readTimelineCounter(t, check, counters, "ALUInstructions")
	if !slices.Equal(total.timestamps, alu.timestamps) || !slices.Equal(total.values, alu.values) {
		t.Fatal("ALU Total Instructions and ALUInstructions differ")
	}
	if total.timestamps[0] < timestampBegin || total.timestamps[len(total.timestamps)-1] > timestampEnd {
		t.Fatalf("ALU Total Instructions timestamp range=%d..%d is outside cost timeline=%d..%d",
			total.timestamps[0], total.timestamps[len(total.timestamps)-1], timestampBegin, timestampEnd)
	}
	t.Log("ALU Total Instructions and ALUInstructions are identical")
	reportTimestampShape(t, alu.timestamps)
}

type timelineCounterSeries struct {
	timestamps []uint64
	values     []float64
}

// readTimelineCounter copies one named counter's samples before the enclosing
// Objective-C autorelease pool drains. The runtime name is deliberately not
// translated to an Xcode display name here.
func readTimelineCounter(t *testing.T, check func(objc.ID, string, reflect.Type, ...any), counters gtshaderprofiler.IGTMioTimelineCounters, name string) timelineCounterSeries {
	t.Helper()
	key := foundation.NewStringWithString(name)
	check(counters.GetID(), "counterForName:", reflect.TypeOf(objc.ID(0)), key)
	counterID := counters.CounterForName(key).GetID()
	if counterID == 0 {
		t.Fatalf("counterForName(%q) returned nil", name)
	}
	counter := gtshaderprofiler.GTMioCounterDataFromID(counterID)
	check(counterID, "name", reflect.TypeOf(objc.ID(0)))
	check(counterID, "sampleCount", reflect.TypeOf(uint64(0)))
	// The generated binding returns the runtime's ^d pointer. SampleCount is the
	// independently checked bound, and the copy keeps the series valid after
	// this Objective-C autorelease pool drains.
	check(counterID, "values", reflect.TypeOf(unsafe.Pointer(nil)))
	if got := counter.Name(); got != name {
		t.Fatalf("counter name = %q, want %q", got, name)
	}
	count := counter.SampleCount()
	if count == 0 {
		t.Fatalf("%s sample count is zero", name)
	}
	t.Logf("%s samples=%d", name, count)
	check(counterID, "timestamps", reflect.TypeOf(unsafe.Pointer(nil)))
	stampsPointer := counter.Timestamps()
	if stampsPointer == nil {
		t.Fatalf("%s timestamps returned nil", name)
	}
	valuesPointer := counter.Values()
	if valuesPointer == nil {
		t.Fatalf("%s values returned nil", name)
	}
	checkCounterBufferExtent(t, name+" values", valuesPointer, count, unsafe.Sizeof(float64(0)))
	checkCounterBufferExtent(t, name+" timestamps", stampsPointer, count, unsafe.Sizeof(uint64(0)))

	values, stamps, err := gtcounter.CounterSeries(counter)
	if err != nil {
		t.Fatalf("%s series: %v", name, err)
	}
	if len(stamps) != int(count) {
		t.Fatalf("%s timestamps = %d, want %d", name, len(stamps), count)
	}
	if stamps[0] == 0 || stamps[len(stamps)-1] == 0 {
		t.Fatalf("%s timestamps have zero endpoint: first=%d last=%d", name, stamps[0], stamps[len(stamps)-1])
	}
	for i := 1; i < len(stamps) && i < 1024; i++ {
		if stamps[i] < stamps[i-1] {
			t.Fatalf("%s timestamps descend at %d: %d < %d", name, i, stamps[i], stamps[i-1])
		}
	}
	t.Logf("%s timestamp range=%d..%d", name, stamps[0], stamps[len(stamps)-1])

	if len(values) != int(count) {
		t.Fatalf("%s values = %d, want %d", name, len(values), count)
	}
	low, high, sum := math.Inf(1), math.Inf(-1), 0.0
	for i, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("%s value[%d] is not finite: %g", name, i, value)
		}
		low = math.Min(low, value)
		high = math.Max(high, value)
		sum += value
	}
	if got, want := low, counter.MinValue(); math.Abs(got-want) > math.Max(1, math.Abs(want))*1e-12 {
		t.Fatalf("%s value minimum = %g, want metadata %g", name, got, want)
	}
	if got, want := high, counter.MaxValue(); math.Abs(got-want) > math.Max(1, math.Abs(want))*1e-12 {
		t.Fatalf("%s value maximum = %g, want metadata %g", name, got, want)
	}
	if sum == 0 {
		t.Fatalf("%s values sum to zero", name)
	}
	edge := min(len(values), 10)
	t.Logf("%s value range=%g..%g sum=%g mean=%g first=%v last=%v",
		name, low, high, sum, sum/float64(len(values)), values[:edge], values[len(values)-edge:])
	return timelineCounterSeries{timestamps: stamps, values: values}
}

// checkCounterBufferExtent records whether malloc can establish that a raw
// counter pointer has enough backing storage for SampleCount elements. A zero
// result is not evidence of a short buffer: malloc_size is silent for some
// valid pointers, so callers must retain any bound gate in that case.
func checkCounterBufferExtent(t *testing.T, name string, ptr unsafe.Pointer, count uint64, elementSize uintptr) {
	t.Helper()
	if count > uint64(^uintptr(0))/uint64(elementSize) {
		t.Fatalf("%s size overflows uintptr: %d elements of %d bytes", name, count, elementSize)
	}
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("open libSystem for malloc_size: %v", err)
	}
	var mallocSize func(unsafe.Pointer) uintptr
	purego.RegisterLibFunc(&mallocSize, lib, "malloc_size")
	got := mallocSize(ptr)
	want := uintptr(count) * elementSize
	if got == 0 {
		t.Logf("%s allocation extent unavailable (malloc_size=0, need at least %d bytes)", name, want)
		return
	}
	if got < want {
		t.Fatalf("%s allocation = %d bytes, need at least %d", name, got, want)
	}
	t.Logf("%s allocation = %d bytes, need at least %d", name, got, want)
}

func reportTimestampShape(t *testing.T, timestamps []uint64) {
	t.Helper()
	const ceiling = uint64(1) << 31
	const nearCeiling = uint64(1_000_000)
	var drops, equal, near, longestPlateau int
	plateau := 1
	for i, timestamp := range timestamps {
		if ceiling-timestamp <= nearCeiling {
			near++
		}
		if i == 0 {
			continue
		}
		if timestamp < timestamps[i-1] {
			drops++
		}
		if timestamp == timestamps[i-1] {
			equal++
			plateau++
		} else {
			if plateau > longestPlateau {
				longestPlateau = plateau
			}
			plateau = 1
		}
	}
	if plateau > longestPlateau {
		longestPlateau = plateau
	}
	t.Logf("counter timestamps full scan: samples=%d span=%d ceiling_delta=%d drops=%d equal_pairs=%d longest_plateau=%d within_%d_of_2^31=%d",
		len(timestamps), timestamps[len(timestamps)-1]-timestamps[0], ceiling-timestamps[len(timestamps)-1], drops, equal, longestPlateau, nearCeiling, near)
	reportTimestampDeltas(t, timestamps)
	reportTimestampBurstCount(t, timestamps, 100_000)
	reportLargestTimestampGaps(t, timestamps)
}

type deltaCount struct {
	delta uint64
	count int
}

func reportTimestampDeltas(t *testing.T, timestamps []uint64) {
	t.Helper()
	deltas := make([]uint64, 0, len(timestamps)-1)
	counts := make(map[uint64]int)
	var belowThousand int
	for i := 1; i < len(timestamps); i++ {
		delta := timestamps[i] - timestamps[i-1]
		deltas = append(deltas, delta)
		counts[delta]++
		if delta < 1000 {
			belowThousand++
		}
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	common := make([]deltaCount, 0, len(counts))
	for delta, count := range counts {
		common = append(common, deltaCount{delta: delta, count: count})
	}
	sort.Slice(common, func(i, j int) bool {
		if common[i].count != common[j].count {
			return common[i].count > common[j].count
		}
		return common[i].delta < common[j].delta
	})
	top := min(len(common), 10)
	t.Logf("counter timestamp deltas: min=%d median=%d max=%d below_1000=%d top=%v",
		deltas[0], deltas[len(deltas)/2], deltas[len(deltas)-1], belowThousand, common[:top])
}

type timestampGap struct {
	position int
	delta    uint64
}

func reportTimestampBurstCount(t *testing.T, timestamps []uint64, threshold uint64) {
	t.Helper()
	runs := 1
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i]-timestamps[i-1] > threshold {
			runs++
		}
	}
	t.Logf("counter timestamp bursts=%d gap_threshold=%d", runs, threshold)
}

func reportLargestTimestampGaps(t *testing.T, timestamps []uint64) {
	t.Helper()
	gaps := make([]timestampGap, 0, len(timestamps)-1)
	for i := 1; i < len(timestamps); i++ {
		gaps = append(gaps, timestampGap{position: i, delta: timestamps[i] - timestamps[i-1]})
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].delta != gaps[j].delta {
			return gaps[i].delta > gaps[j].delta
		}
		return gaps[i].position < gaps[j].position
	})
	for i, gap := range gaps[:min(len(gaps), 40)] {
		ratio := 0.0
		if i+1 < len(gaps) {
			ratio = float64(gap.delta) / float64(gaps[i+1].delta)
		}
		t.Logf("counter timestamp gap rank=%d position=%d range=%d..%d delta=%d next_ratio=%g",
			i+1, gap.position, timestamps[gap.position-1], timestamps[gap.position], gap.delta, ratio)
	}
}

func reportTimelineCounterMetadata(t *testing.T, check func(objc.ID, string, reflect.Type, ...any), counters gtshaderprofiler.IGTMioTimelineCounters, names []string) {
	t.Helper()
	scopes := make(map[uint16]int)
	for _, name := range names {
		key := foundation.NewStringWithString(name)
		check(counters.GetID(), "counterForName:", reflect.TypeOf(objc.ID(0)), key)
		counterID := counters.CounterForName(key).GetID()
		if counterID == 0 {
			t.Fatalf("counterForName(%q) returned nil", name)
		}
		counter := gtshaderprofiler.GTMioCounterDataFromID(counterID)
		check(counterID, "counterIndex", reflect.TypeOf(uint64(0)))
		check(counterID, "dataType", reflect.TypeOf(uint32(0)))
		check(counterID, "maxValue", reflect.TypeOf(float64(0)))
		check(counterID, "minValue", reflect.TypeOf(float64(0)))
		check(counterID, "sampleInterval", reflect.TypeOf(uint64(0)))
		check(counterID, "scope", reflect.TypeOf(uint16(0)))
		check(counterID, "scopeIndex", reflect.TypeOf(uint64(0)))
		check(counterID, "valueType", reflect.TypeOf(uint16(0)))
		scope := counter.Scope()
		scopes[scope]++
		t.Logf("counter metadata name=%q index=%d interval=%d scope=%d scope_index=%d data_type=%d value_type=%d min=%g max=%g",
			name, counter.CounterIndex(), counter.SampleInterval(), scope, counter.ScopeIndex(),
			counter.DataType(), counter.ValueType(), counter.MinValue(), counter.MaxValue())
	}
	var scopeValues []uint16
	for scope := range scopes {
		scopeValues = append(scopeValues, scope)
	}
	sort.Slice(scopeValues, func(i, j int) bool { return scopeValues[i] < scopeValues[j] })
	for _, scope := range scopeValues {
		t.Logf("counter metadata scope=%d count=%d", scope, scopes[scope])
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
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
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
