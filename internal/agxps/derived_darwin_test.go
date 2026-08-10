//go:build darwin

package agxps

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// probeGPU is the triple this machine reports; the counter probes in this
// package already take it from GPUTRACE_PROBE_GPU. 16/6/1 is G16X here.
const (
	probeGen     = 16
	probeVariant = 6
	probeRev     = 1
)

// f32Utilization depends on exactly one raw counter, which is why it is the
// cheapest independently checkable derived counter on this GPU.
const (
	identF32Utilization = 184444
	identALUUtilization = 184161
)

func requireFramework(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(gtShaderProfilerPath); err != nil {
		t.Skipf("GTShaderProfiler not present: %v", err)
	}
}

// TestProbeDerivedRegistry reads what the registry says about the two derived
// counters this work targets, without evaluating anything.
func TestProbeDerivedRegistry(t *testing.T) {
	requireFramework(t)
	a, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		t.Fatal("agxps_initialize returned 0")
	}
	gpu, err := NewGPU(probeGen, probeVariant, probeRev, false)
	if err != nil {
		t.Fatalf("NewGPU: %v", err)
	}
	defer gpu.Destroy()
	for _, ident := range []uint64{identF32Utilization, identALUUtilization} {
		raw, err := RawCountersUsedBy(gpu, []uint64{ident})
		if err != nil {
			t.Fatalf("RawCountersUsedBy(%d): %v", ident, err)
		}
		t.Logf("%d %q derived=%v raw=%v", ident, a.counterGetName(ident), a.counterIsDerived(ident), raw)
	}
}

// TestProbeDerivedMechanism exercises the eleven-argument call with inputs the
// probe made up. It establishes only that the ABI is right and what the
// evaluator does with a complete and an incomplete input set; the numbers it
// prints are a function of invented samples and mean nothing about any GPU.
func TestProbeDerivedMechanism(t *testing.T) {
	requireFramework(t)
	gpu, err := NewGPU(probeGen, probeVariant, probeRev, false)
	if err != nil {
		t.Fatalf("NewGPU: %v", err)
	}
	defer gpu.Destroy()

	alwaysOn, err := AlwaysOnRawCounters(gpu)
	if err != nil {
		t.Fatalf("AlwaysOnRawCounters: %v", err)
	}
	a, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	for _, ident := range alwaysOn {
		t.Logf("always-on raw counter %d %q derived=%v", ident, a.counterGetName(ident), a.counterIsDerived(ident))
	}

	if h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
		if s, err := purego.Dlsym(h, "agxps_counter_compute_derived_counters"); err == nil {
			t.Logf("slide = %#x (compute_derived_counters at %#x, static 0x558f6c)", s-0x558f6c, s)
		}
	}
	for _, name := range []string{"GPUCycles", "DeltaSeconds", "not_a_counter_name"} {
		t.Logf("agxps_counter_get_ident(gpu, %q) = %d", name, a.counterGetIdent(uintptr(gpu), name))
	}

	synthetic := []RawCounterSeries{{Ident: 102796, Values: []float64{0, 1, 2, 3}}}
	for _, ident := range alwaysOn {
		synthetic = append(synthetic, RawCounterSeries{Ident: ident, Values: []float64{1, 1, 1, 1}})
	}
	for _, name := range []string{"GPUCycles", "DeltaSeconds"} {
		if id := a.counterGetIdent(uintptr(gpu), name); id != 0 {
			synthetic = append(synthetic, RawCounterSeries{Ident: id, Values: []float64{1, 1, 1, 1}})
		}
	}
	constants := []DerivedConstant{{Name: "NUM_CORES", Value: 40}}
	series, err := ComputeDerivedCounters(gpu, synthetic, constants, []uint64{identF32Utilization})
	t.Logf("F32 Utilization with one invented input series: %v, err=%v", series, err)

	// ALU Utilization needs four raw counters; supplying only the one they
	// share must be refused rather than answered.
	series, err = ComputeDerivedCounters(gpu, synthetic, constants, []uint64{identALUUtilization})
	t.Logf("ALU Utilization with one of its four inputs: %v, err=%v", series, err)

	for _, cores := range []float64{40, 20, 10} {
		s, err := ComputeDerivedCounters(gpu, synthetic,
			[]DerivedConstant{{Name: "NUM_CORES", Value: cores}}, []uint64{identF32Utilization})
		t.Logf("NUM_CORES=%v -> %v err=%v", cores, s, err)
	}

	full := append([]RawCounterSeries(nil), synthetic...)
	for _, ident := range []uint64{102800, 102804, 102792} {
		full = append(full, RawCounterSeries{Ident: ident, Values: []float64{0, 1, 2, 3}})
	}
	s, err := ComputeDerivedCounters(gpu, full, constants, []uint64{identF32Utilization, identALUUtilization})
	t.Logf("F32 and ALU on the same four invented inputs: %v err=%v", s, err)

	zeroed := append([]RawCounterSeries(nil), synthetic...)
	for _, ident := range []uint64{102800, 102804, 102792} {
		zeroed = append(zeroed, RawCounterSeries{Ident: ident, Values: []float64{0, 0, 0, 0}})
	}
	s, err = ComputeDerivedCounters(gpu, zeroed, constants, []uint64{identF32Utilization, identALUUtilization})
	t.Logf("ALU with its three non-F32 inputs zeroed: %v err=%v", s, err)

	// Is either implicit input load-bearing, or does supplying them merely
	// satisfy a presence check?
	for _, probe := range []struct {
		name  string
		cycle float64
		delta float64
	}{{"cycles=1 delta=1", 1, 1}, {"cycles=2 delta=1", 2, 1}, {"cycles=1 delta=2", 1, 2}} {
		in := []RawCounterSeries{{Ident: 102796, Values: []float64{0, 1, 2, 3}},
			{Ident: a.counterGetIdent(uintptr(gpu), "GPUCycles"), Values: []float64{probe.cycle, probe.cycle, probe.cycle, probe.cycle}},
			{Ident: a.counterGetIdent(uintptr(gpu), "DeltaSeconds"), Values: []float64{probe.delta, probe.delta, probe.delta, probe.delta}}}
		got, err := ComputeDerivedCounters(gpu, in, constants, []uint64{identF32Utilization})
		t.Logf("%s -> %v err=%v", probe.name, got, err)
	}
}

// captureAPI is the profile-data value surface the capture probe needs and
// countershape_darwin.go does not bind.
type captureAPI struct {
	pdCounterNum   func(uintptr) uint64
	pdCounterNames func(pd uintptr, out *unsafe.Pointer, first, count uint64) bool
	pdCounterVNum  func(pd uintptr, out *uint64, first, count uint64) bool
	// disasm: agxps_aps_profile_data_get_counter_values @0x4edce4 copies the
	// std::vector begin POINTER of each series, not its samples. The repo
	// refuses to call this a sample-copy accessor
	// (internal/counter.ErrAPSCounterValuesBinding) and this probe does not
	// change that: it dereferences the pointer only to answer the question of
	// what the evaluator would be fed, and reports the element width it
	// assumed.
	pdCounterValues func(pd uintptr, out *unsafe.Pointer, first, count uint64) bool
}

func loadCaptureAPI(t *testing.T) *captureAPI {
	t.Helper()
	h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	a := new(captureAPI)
	for _, b := range []struct {
		name   string
		target any
	}{
		{"agxps_aps_profile_data_get_counter_num", &a.pdCounterNum},
		{"agxps_aps_profile_data_get_counter_names", &a.pdCounterNames},
		{"agxps_aps_profile_data_get_counter_values_num", &a.pdCounterVNum},
		{"agxps_aps_profile_data_get_counter_values", &a.pdCounterValues},
	} {
		purego.RegisterLibFunc(b.target, h, b.name)
	}
	return a
}

// TestProbeDerivedFromCapture drives the evaluator from a real Counters_f_*.raw
// shard. GPUTRACE_PROBE_COUNTERS names the file; without it the test skips, so
// a normal `go test ./...` does not need a capture.
func TestProbeDerivedFromCapture(t *testing.T) {
	requireFramework(t)
	path := os.Getenv("GPUTRACE_PROBE_COUNTERS")
	if path == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS to a Counters_f_*.raw file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}

	shape, err := loadCounterShapeAPI()
	if err != nil {
		t.Fatalf("loadCounterShapeAPI: %v", err)
	}
	if shape.initialize(0, 0, 0, 0) == 0 {
		t.Fatal("agxps_initialize returned 0")
	}
	gpuHandle := shape.gpuCreate(probeGen, probeVariant, probeRev, 0)
	if gpuHandle == 0 {
		t.Fatal("agxps_gpu_create returned NULL")
	}
	defer shape.gpuDestroy(gpuHandle)

	descriptor := &counterDescriptor{
		GPU: gpuHandle, PulsePeriod: 16, EraPeriod: 64, CountPeriod: 128,
		ChunkSize: 0x1000, MaxTimestamp: ^uint64(0), MaxParseErrorCount: 50,
	}
	var pinner runtime.Pinner
	pinner.Pin(descriptor)
	defer pinner.Unpin()
	parser := shape.parserCreate(unsafe.Pointer(descriptor))
	if parser == 0 {
		t.Fatal("agxps_aps_parser_create returned NULL")
	}
	defer shape.parserDestroy(parser)
	var parseError uint32
	pd := shape.parserParse(parser, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &parseError)
	if pd == 0 || parseError != 0 {
		t.Fatalf("parse %s: pd=%#x code=%d", path, pd, parseError)
	}
	defer shape.profileDestroy(pd)

	cap := loadCaptureAPI(t)
	n := cap.pdCounterNum(pd)
	if n == 0 {
		t.Fatal("shard decoded with zero counter series")
	}
	namePtrs := make([]unsafe.Pointer, n)
	lengths := make([]uint64, n)
	pointers := make([]unsafe.Pointer, n)
	if !cap.pdCounterNames(pd, &namePtrs[0], 0, n) ||
		!cap.pdCounterVNum(pd, &lengths[0], 0, n) ||
		!cap.pdCounterValues(pd, &pointers[0], 0, n) {
		t.Fatal("bulk counter accessors failed")
	}
	t.Logf("%s: %d counter series", path, n)

	d, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	// get_counter_names copies the vector entry verbatim, and the entries are
	// `const char *`, not idents: the values move with ASLR between runs and
	// adjacent entries differ by exactly 65, the length of a 64-character hex
	// name plus its NUL. Dereference them and ask the registry for the ident.
	gpu := GPU(gpuHandle)
	registry := make([]uint64, n)
	names := make([]string, n)
	matched := 0
	for i, p := range namePtrs {
		names[i] = cString(p)
		registry[i] = d.counterGetIdent(uintptr(gpu), names[i])
		if registry[i] != ^uint64(0) {
			matched++
		}
	}
	t.Logf("series names resolve to registry idents: %d of %d", matched, n)
	for i := 0; i < len(names) && i < 4; i++ {
		t.Logf("  series[%d] name=%q ident=%d samples=%d", i, names[i], registry[i], lengths[i])
	}

	byRegistry := map[uint64]int{}
	for i, id := range registry {
		if id != ^uint64(0) {
			byRegistry[id] = i
		}
	}
	for _, want := range []uint64{102796, 102800, 102804, 102792, 181821, 181823} {
		if i, ok := byRegistry[want]; ok {
			t.Logf("raw counter %d %q -> series %d, %d samples", want, d.counterGetName(want), i, lengths[i])
		} else {
			t.Logf("raw counter %d %q NOT in this shard", want, d.counterGetName(want))
		}
	}

	// What is the element type behind the pointers get_counter_values copies?
	// The accessor's own bounds check divides by 8, so the element is 8 bytes;
	// whether those 8 bytes are a double or a uint64 is decided here rather
	// than assumed.
	if i, ok := byRegistry[102796]; ok && pointers[i] != nil && lengths[i] > 0 {
		asU := unsafe.Slice((*uint64)(pointers[i]), lengths[i])
		asF := unsafe.Slice((*float64)(pointers[i]), lengths[i])
		nonzero, first, maxU := 0, -1, uint64(0)
		for j, v := range asU {
			if v != 0 {
				nonzero++
				if first < 0 {
					first = j
				}
				if v > maxU {
					maxU = v
				}
			}
		}
		t.Logf("series %d: %d/%d nonzero, max as uint64 = %d (%#x)", i, nonzero, lengths[i], maxU, maxU)

		// Run the evaluator on the real series. GPUCycles is not in the
		// capture, so this is NOT a measurement: it is a plumbing check plus a
		// bound. F32 Utilization = raw / (GPUCycles * NUM_CORES * 4), so any
		// candidate GPUCycles below maxRaw/(NUM_CORES*4) makes some sample
		// exceed 1 and is refuted by that alone.
		real := make([]float64, lengths[i])
		for j, v := range asU {
			real[j] = float64(v)
		}
		gpuc, err := CounterIdent(gpu, "GPUCycles")
		if err != nil {
			t.Fatalf("CounterIdent(GPUCycles): %v", err)
		}
		deltas, err := CounterIdent(gpu, "DeltaSeconds")
		if err != nil {
			t.Fatalf("CounterIdent(DeltaSeconds): %v", err)
		}
		unit := make([]float64, lengths[i])
		for j := range unit {
			unit[j] = 1
		}
		got, err := ComputeDerivedCounters(gpu, []RawCounterSeries{
			{Ident: 102796, Values: real},
			{Ident: gpuc, Values: unit},
			{Ident: deltas, Values: unit},
		}, []DerivedConstant{{Name: "NUM_CORES", Value: 40}}, []uint64{identF32Utilization})
		if err != nil {
			t.Fatalf("evaluator on %d real samples: %v", lengths[i], err)
		}
		peak := 0.0
		for _, v := range got[0].Values {
			if v > peak {
				peak = v
			}
		}
		t.Logf("evaluator accepted %d real samples; peak with GPUCycles=1 is %v, "+
			"so any real GPUCycles below %v puts F32 Utilization above 1",
			len(got[0].Values), peak, peak)
		if first >= 0 {
			hi := first + 6
			if hi > len(asU) {
				hi = len(asU)
			}
			t.Logf("  from sample %d as uint64:  %v", first, asU[first:hi])
			t.Logf("  from sample %d as float64: %v", first, asF[first:hi])
		}
	}
}

// TestProbeShardCoverage asks whether the two implicit evaluator inputs,
// GPUCycles and DeltaSeconds, appear as counter series anywhere in a capture.
// GPUTRACE_PROBE_COUNTERS_DIR names a .gpuprofiler_raw directory.
func TestProbeShardCoverage(t *testing.T) {
	requireFramework(t)
	dir := os.Getenv("GPUTRACE_PROBE_COUNTERS_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS_DIR to a .gpuprofiler_raw directory")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "Counters_f_*.raw"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no Counters_f_*.raw under %s (%v)", dir, err)
	}
	shape, err := loadCounterShapeAPI()
	if err != nil {
		t.Fatalf("loadCounterShapeAPI: %v", err)
	}
	if shape.initialize(0, 0, 0, 0) == 0 {
		t.Fatal("agxps_initialize returned 0")
	}
	d, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	gpuHandle := shape.gpuCreate(probeGen, probeVariant, probeRev, 0)
	defer shape.gpuDestroy(gpuHandle)
	capture := loadCaptureAPI(t)

	union := map[uint64]int{}
	unresolved := 0
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		descriptor := &counterDescriptor{
			GPU: gpuHandle, PulsePeriod: 16, EraPeriod: 64, CountPeriod: 128,
			ChunkSize: 0x1000, MaxTimestamp: ^uint64(0), MaxParseErrorCount: 50,
		}
		var pinner runtime.Pinner
		pinner.Pin(descriptor)
		parser := shape.parserCreate(unsafe.Pointer(descriptor))
		var code uint32
		pd := shape.parserParse(parser, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &code)
		if pd == 0 || code != 0 {
			t.Fatalf("parse %s: code=%d", path, code)
		}
		n := capture.pdCounterNum(pd)
		if n > 0 {
			ptrs := make([]unsafe.Pointer, n)
			if !capture.pdCounterNames(pd, &ptrs[0], 0, n) {
				t.Fatalf("get_counter_names failed for %s", path)
			}
			for _, p := range ptrs {
				id := d.counterGetIdent(uintptr(gpuHandle), cString(p))
				if id == ^uint64(0) {
					unresolved++
					continue
				}
				union[id]++
			}
		}
		shape.profileDestroy(pd)
		shape.parserDestroy(parser)
		pinner.Unpin()
	}
	t.Logf("%d shards, %d distinct registry raw idents, %d series names the registry did not know",
		len(entries), len(union), unresolved)
	for _, want := range []uint64{102796, 102800, 102804, 102792, 181821, 181823} {
		t.Logf("  %d %q present in %d shards", want, d.counterGetName(want), union[want])
	}
}

// cString reads a NUL-terminated C string the framework owns.
func cString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	b := unsafe.Slice((*byte)(p), 4096)
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return ""
}

// TestDerivedEvaluatorABI pins the eleven-argument declaration of
// agxps_counter_compute_derived_counters and the 16-byte constant layout by
// asserting relationships the evaluator cannot satisfy if either is wrong.
//
// It uses invented input samples deliberately. Nothing here is a measurement of
// any GPU; the assertions are about arithmetic the evaluator must be doing on
// whatever it is handed, and every one of them fails when an argument lands in
// the wrong register.
func TestDerivedEvaluatorABI(t *testing.T) {
	requireFramework(t)
	gpu, err := NewGPU(probeGen, probeVariant, probeRev, false)
	if err != nil {
		t.Fatalf("NewGPU: %v", err)
	}
	defer gpu.Destroy()
	a, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		t.Fatal("agxps_initialize returned 0")
	}
	cycles := a.counterGetIdent(uintptr(gpu), "GPUCycles")
	delta := a.counterGetIdent(uintptr(gpu), "DeltaSeconds")
	if cycles == ^uint64(0) || delta == ^uint64(0) {
		t.Fatalf("registry does not know GPUCycles/DeltaSeconds: %d %d", cycles, delta)
	}

	ones := []float64{1, 1, 1, 1}
	ramp := []float64{0, 1, 2, 3}
	base := []RawCounterSeries{
		{Ident: 102796, Values: ramp},
		{Ident: cycles, Values: ones},
		{Ident: delta, Values: ones},
	}
	compute := func(in []RawCounterSeries, cores float64, want ...uint64) []DerivedCounterSeries {
		t.Helper()
		got, err := ComputeDerivedCounters(gpu, in,
			[]DerivedConstant{{Name: "NUM_CORES", Value: cores}}, want)
		if err != nil {
			t.Fatalf("ComputeDerivedCounters(cores=%v, %v): %v", cores, want, err)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d series, want %d: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i].Ident != want[i] {
				t.Fatalf("series %d ident = %d, want %d", i, got[i].Ident, want[i])
			}
			if len(got[i].Values) != len(ramp) {
				t.Fatalf("series %d has %d samples, want %d", i, len(got[i].Values), len(ramp))
			}
		}
		return got
	}

	f32at40 := compute(base, 40, identF32Utilization)[0]
	if f32at40.Name != "F32 Utilization" {
		t.Fatalf("ident %d names %q, want %q", identF32Utilization, f32at40.Name, "F32 Utilization")
	}
	// The evaluator is linear in the raw series: sample i must be i times
	// sample 1 exactly. A misplaced pointer argument does not produce a ramp.
	for i, v := range f32at40.Values {
		if want := f32at40.Values[1] * ramp[i]; v != want {
			t.Fatalf("F32 sample %d = %v, want %v (linear in the input)", i, v, want)
		}
	}
	if f32at40.Values[1] <= 0 {
		t.Fatalf("F32 sample 1 = %v, want a positive value", f32at40.Values[1])
	}

	// NUM_CORES reaches the formula as a divisor. Halving it must exactly
	// double every sample; scaling by two is exact in binary floating point, so
	// this compares without a tolerance. If the 16-byte constant record were
	// laid out wrongly the evaluator would read a different number, and if the
	// name array never arrived it would dereference NULL instead.
	f32at20 := compute(base, 20, identF32Utilization)[0]
	for i := range f32at40.Values {
		if want := 2 * f32at40.Values[i]; f32at20.Values[i] != want {
			t.Fatalf("NUM_CORES=20 sample %d = %v, want %v (2x the NUM_CORES=40 value)",
				i, f32at20.Values[i], want)
		}
	}

	// GPUCycles is a divisor too, which is why the registry hides it from
	// RawCountersUsedBy and why supplying it is not optional.
	doubled := append([]RawCounterSeries(nil), base...)
	doubled[1] = RawCounterSeries{Ident: cycles, Values: []float64{2, 2, 2, 2}}
	f32fast := compute(doubled, 40, identF32Utilization)[0]
	for i := range f32at40.Values {
		if want := f32at40.Values[i] / 2; f32fast.Values[i] != want {
			t.Fatalf("GPUCycles=2 sample %d = %v, want %v (half the GPUCycles=1 value)",
				i, f32fast.Values[i], want)
		}
	}

	// ALU Utilization shares raw counter 102796 with F32 Utilization. With its
	// other three inputs zeroed, the two counters must move together, and the
	// coefficient ALU gives the shared input must be exactly half F32's. That
	// is a relationship between two independent formulas evaluated in one call,
	// so it does not survive an ABI in which the derived-ident array or its
	// count is misplaced.
	shared := append([]RawCounterSeries(nil), base...)
	for _, ident := range []uint64{102800, 102804, 102792} {
		shared = append(shared, RawCounterSeries{Ident: ident, Values: []float64{0, 0, 0, 0}})
	}
	both := compute(shared, 40, identF32Utilization, identALUUtilization)
	if both[1].Name != "ALU Utilization" {
		t.Fatalf("ident %d names %q, want %q", identALUUtilization, both[1].Name, "ALU Utilization")
	}
	for i := range both[0].Values {
		if want := both[0].Values[i] / 2; both[1].Values[i] != want {
			t.Fatalf("ALU sample %d = %v, want %v (half of F32's %v)",
				i, both[1].Values[i], want, both[0].Values[i])
		}
	}
}

// TestDerivedRefusals pins the two ways the evaluator says no, one of which it
// signals with a return code and the other of which it does not signal at all.
func TestDerivedRefusals(t *testing.T) {
	requireFramework(t)
	gpu, err := NewGPU(probeGen, probeVariant, probeRev, false)
	if err != nil {
		t.Fatalf("NewGPU: %v", err)
	}
	defer gpu.Destroy()
	a, err := loadDerivedAPI()
	if err != nil {
		t.Fatalf("loadDerivedAPI: %v", err)
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		t.Fatal("agxps_initialize returned 0")
	}
	ones := []float64{1, 1, 1, 1}
	base := []RawCounterSeries{
		{Ident: 102796, Values: ones},
		{Ident: a.counterGetIdent(uintptr(gpu), "GPUCycles"), Values: ones},
		{Ident: a.counterGetIdent(uintptr(gpu), "DeltaSeconds"), Values: ones},
	}
	cores := []DerivedConstant{{Name: "NUM_CORES", Value: 40}}

	// ALU Utilization needs four raw counters. Given one of them it must refuse
	// rather than answer with the three treated as zero.
	if _, err := ComputeDerivedCounters(gpu, base, cores, []uint64{identALUUtilization}); !errors.Is(err, ErrDerivedInputsMissing) {
		t.Fatalf("ALU with one of four inputs: err = %v, want ErrDerivedInputsMissing", err)
	}
	// F32 Utilization needs only 102796, so the same inputs must succeed. This
	// is the control: without it the refusal above could be a blanket failure.
	if _, err := ComputeDerivedCounters(gpu, base, cores, []uint64{identF32Utilization}); err != nil {
		t.Fatalf("F32 with its one input: %v", err)
	}
	// A formula that asks for a constant nobody supplied dereferences NULL
	// inside the framework, so the refusal has to happen before the call.
	if _, err := ComputeDerivedCounters(gpu, base, nil, []uint64{identF32Utilization}); !errors.Is(err, ErrDerivedConstantMissing) {
		t.Fatalf("no constants: err = %v, want ErrDerivedConstantMissing", err)
	}
}
