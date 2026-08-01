//go:build darwin

package agxps

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// rawDescriptor is the true layout of agxps_aps_descriptor, derived from the
// disassembly of agxps_aps_descriptor_create (104 bytes / 0x68).
type rawDescriptor struct {
	GPU                    uintptr // 0x00
	PulsePeriod            uint32  // 0x08
	EraPeriod              uint32  // 0x0c
	CountPeriod            uint32  // 0x10
	_                      uint32  // 0x14
	ChunkSize              uint64  // 0x18 default 0x1000
	CounterUarchBehaviour  int32   // 0x20
	ExcludeFlags           int32   // 0x24
	MinTimestamp           uint64  // 0x28 default 0
	MaxTimestamp           uint64  // 0x30 default -1
	CountersFilter         uintptr // 0x38
	CountersFilterSize     uint64  // 0x40
	TimestampSyncPointData uintptr // 0x48
	TimestampSyncPointSize uint64  // 0x50
	MaxParseErrorCount     uint32  // 0x58 default 50
	_                      uint32  // 0x5c
	TimebaseOffset         uint64  // 0x60
}

// rangeGet is the shape of the agxps bulk accessors:
//
//	bool get_X(profile_data, uint64_t *out, size_t first, size_t count)
//
// It copies out[i] = X[first+i] for i in [0,count) and returns false if the
// requested range is out of bounds.
type rangeGet func(pd uintptr, out *uint64, first, count uint64) bool

type rawAPI struct {
	initialize          func() int32
	gpuCreate           func(gen, variant, rev uint32, exact uint32) uintptr
	gpuIsValid          func(uintptr) bool
	gpuGetGen           func(uintptr) uint32
	gpuGetVariant       func(uintptr) uint32
	gpuGetRev           func(uintptr) uint32
	gpuFormatName       func(uintptr, *byte, uint64) int32
	apsGPUIsSupported   func(gen, variant, rev uint32) bool
	apsFindSupportedRev func(gen, variant, rev uint32, out *uint32) bool
	parserCreate        func(unsafe.Pointer) uintptr
	parserIsValid       func(uintptr) bool
	parserParse         func(parser uintptr, data unsafe.Pointer, size uint64, flags uint32, errOut *uint32) uintptr
	parserDestroy       func(uintptr)
	pdIsValid           func(uintptr) bool
	pdKicksNum          func(uintptr) uint64
	pdKickStart         rangeGet
	pdKickEnd           rangeGet
	pdKickID            rangeGet
	pdESLNum            func(uintptr) uint64
	pdESLStart          rangeGet
	pdESLEnd            rangeGet
	pdESLTrace          rangeGet
	pdChunkSize         func(uintptr) uint64
	itExecEventsNum     func(uintptr, uint64) uint64
	itPCAdvancesNum     func(uintptr, uint64) uint64
	genToString         func(gen uint32, buf *byte, size uint64) int32
	genFromString       func(s string) uint32
	numUSCs             func(uintptr) uint32
	numMGPUs            func(uintptr) uint32
	numAGCs             func(uintptr) uint32
	uscArch             func(uintptr) uint32
	revWithFallback     func(uintptr) uint32
	getChunkSize        func(uintptr, uint32, uint32) uint64
	pulsePeriodNum      func(uintptr) uint64
	pulsePeriod         func(uintptr, uint64) uint32
	eraPeriodNum        func(uintptr) uint64
	eraPeriod           func(uintptr, uint64) uint32
	countPeriodNum      func(uintptr) uint64
	countPeriod         func(uintptr, uint64) uint32
}

func loadRawAPI(t *testing.T) *rawAPI {
	t.Helper()
	h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	a := &rawAPI{}
	reg := func(p any, name string) {
		purego.RegisterLibFunc(p, h, name)
	}
	reg(&a.initialize, "agxps_initialize")
	reg(&a.gpuCreate, "agxps_gpu_create")
	reg(&a.gpuIsValid, "agxps_gpu_is_valid")
	reg(&a.gpuGetGen, "agxps_gpu_get_gen")
	reg(&a.gpuGetVariant, "agxps_gpu_get_variant")
	reg(&a.gpuGetRev, "agxps_gpu_get_rev")
	reg(&a.gpuFormatName, "agxps_gpu_format_name")
	reg(&a.apsGPUIsSupported, "agxps_aps_gpu_is_supported")
	reg(&a.apsFindSupportedRev, "agxps_aps_gpu_find_supported_revision")
	reg(&a.parserCreate, "agxps_aps_parser_create")
	reg(&a.parserIsValid, "agxps_aps_parser_is_valid")
	reg(&a.parserParse, "agxps_aps_parser_parse")
	reg(&a.parserDestroy, "agxps_aps_parser_destroy")
	reg(&a.pdIsValid, "agxps_aps_profile_data_is_valid")
	reg(&a.pdKicksNum, "agxps_aps_profile_data_get_kicks_num")
	reg(&a.pdKickStart, "agxps_aps_profile_data_get_kick_start")
	reg(&a.pdKickEnd, "agxps_aps_profile_data_get_kick_end")
	reg(&a.pdKickID, "agxps_aps_profile_data_get_kick_id")
	reg(&a.pdESLNum, "agxps_aps_profile_data_get_esl_cliques_num")
	reg(&a.pdESLStart, "agxps_aps_profile_data_get_esl_clique_start")
	reg(&a.pdESLEnd, "agxps_aps_profile_data_get_esl_clique_end")
	reg(&a.pdESLTrace, "agxps_aps_profile_data_get_esl_clique_instruction_trace")
	reg(&a.pdChunkSize, "agxps_aps_profile_data_get_chunk_size")
	reg(&a.itExecEventsNum, "agxps_aps_clique_instruction_trace_get_execution_events_num")
	reg(&a.itPCAdvancesNum, "agxps_aps_clique_instruction_trace_get_pc_advances_num")
	reg(&a.genToString, "agxps_gpu_gen_to_string")
	reg(&a.genFromString, "agxps_gpu_gen_from_string")
	reg(&a.numUSCs, "agxps_gpu_get_num_physical_uscs")
	reg(&a.numMGPUs, "agxps_gpu_get_num_physical_mgpus")
	reg(&a.numAGCs, "agxps_gpu_get_num_physical_agcs")
	reg(&a.uscArch, "agxps_gpu_get_usc_arch")
	reg(&a.revWithFallback, "agxps_gpu_get_rev_with_aps_fallback")
	reg(&a.getChunkSize, "agxps_aps_get_chunk_size")
	reg(&a.pulsePeriodNum, "agxps_aps_get_valid_pulse_period_num")
	reg(&a.pulsePeriod, "agxps_aps_get_valid_pulse_period")
	reg(&a.eraPeriodNum, "agxps_aps_get_valid_era_period_num")
	reg(&a.eraPeriod, "agxps_aps_get_valid_era_period")
	reg(&a.countPeriodNum, "agxps_aps_get_valid_count_period_num")
	reg(&a.countPeriod, "agxps_aps_get_valid_count_period")
	return a
}

func TestRawProbeGPUDetails(t *testing.T) {
	a := loadRawAPI(t)
	a.initialize()
	for gen := uint32(14); gen <= 20; gen++ {
		for variant := uint32(0); variant < 8; variant++ {
			g := a.gpuCreate(gen, variant, 1, 1)
			if g == 0 {
				continue
			}
			buf := make([]byte, 128)
			a.genToString(gen, &buf[0], 128)
			name := cstr(buf)
			t.Logf("gen=%2d(%s) variant=%d: uscs=%d mgpus=%d agcs=%d uscArch=%d supported(rev1)=%v",
				gen, name, variant, a.numUSCs(g), a.numMGPUs(g), a.numAGCs(g), a.uscArch(g),
				a.apsGPUIsSupported(gen, variant, 1))
		}
	}
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func TestRawProbeSupportedGPUs(t *testing.T) {
	a := loadRawAPI(t)
	t.Logf("agxps_initialize() = %d", a.initialize())

	var probeRev uint32
	t.Logf("find_supported_revision(0,0,0) = %v out=%d", a.apsFindSupportedRev(0, 0, 0, &probeRev), probeRev)

	var found int
	for gen := uint32(0); gen < 64; gen++ {
		for variant := uint32(0); variant < 64; variant++ {
			for rev := uint32(0); rev < 16; rev++ {
				if a.apsGPUIsSupported(gen, variant, rev) {
					g := a.gpuCreate(gen, variant, rev, 1)
					name := "?"
					if g != 0 {
						buf := make([]byte, 128)
						if n := a.gpuFormatName(g, &buf[0], 128); n > 0 {
							name = string(buf[:n])
						} else {
							for i, c := range buf {
								if c == 0 {
									name = string(buf[:i])
									break
								}
							}
						}
					}
					t.Logf("supported gen=%d variant=%d rev=%d handle=%#x name=%q", gen, variant, rev, g, name)
					found++
				}
			}
		}
	}
	t.Logf("total supported triples: %d", found)
}

func TestRawProbeParserCreate(t *testing.T) {
	a := loadRawAPI(t)
	a.initialize()

	genS := os.Getenv("GPUTRACE_PROBE_GEN")
	varS := os.Getenv("GPUTRACE_PROBE_VARIANT")
	revS := os.Getenv("GPUTRACE_PROBE_REV")
	// 16/6/1 is M4 Max: gen is the AGX G-number (16 = G16), and variant 6
	// reports 40 USCs, matching the 40-core part. Not the gpuGeneration in a
	// streamData archive, which reads 2 for this machine -- gpu_create(2,2,0)
	// yields a handle that reports valid but is_supported=false, i.e. no
	// backing GPU description, and every parser_create against it returns null.
	gen, variant, rev := uint32(16), uint32(6), uint32(1)
	parse := func(s string, dst *uint32) {
		if s == "" {
			return
		}
		var v uint32
		for _, c := range s {
			v = v*10 + uint32(c-'0')
		}
		*dst = v
	}
	parse(genS, &gen)
	parse(varS, &variant)
	parse(revS, &rev)

	gpu := a.gpuCreate(gen, variant, rev, 0)
	t.Logf("gpu_create(%d,%d,%d) = %#x valid=%v supported=%v", gen, variant, rev, gpu,
		gpu != 0 && a.gpuIsValid(gpu), a.apsGPUIsSupported(gen, variant, rev))
	if gpu == 0 {
		t.Fatalf("gpu_create returned null")
	}
	t.Logf("gpu actual gen=%d variant=%d rev=%d", a.gpuGetGen(gpu), a.gpuGetVariant(gpu), a.gpuGetRev(gpu))

	t.Logf("rev_with_aps_fallback = %d, is_supported(gen,var,fallback) = %v",
		a.revWithFallback(gpu), a.apsGPUIsSupported(gen, variant, a.revWithFallback(gpu)))
	t.Logf("chunk_size(gpu,0,0)=%d (gpu,1,0)=%d (gpu,0,1)=%d",
		a.getChunkSize(gpu, 0, 0), a.getChunkSize(gpu, 1, 0), a.getChunkSize(gpu, 0, 1))
	dump := func(name string, num func(uintptr) uint64, get func(uintptr, uint64) uint32) {
		n := num(gpu)
		var vs []uint32
		for i := uint64(0); i < n && i < 32; i++ {
			vs = append(vs, get(gpu, i))
		}
		t.Logf("%s: n=%d %v", name, n, vs)
	}
	dump("pulse", a.pulsePeriodNum, a.pulsePeriod)
	dump("era", a.eraPeriodNum, a.eraPeriod)
	dump("count", a.countPeriodNum, a.countPeriod)

	var pinner runtime.Pinner
	var p uintptr
	var desc *rawDescriptor
	type variantCfg struct {
		name string
		d    rawDescriptor
	}
	cfgs := []variantCfg{
		{"defaults", rawDescriptor{GPU: gpu, ChunkSize: 0x1000, MaxTimestamp: ^uint64(0), MaxParseErrorCount: 50}},
		{"chunk40000", rawDescriptor{GPU: gpu, ChunkSize: 0x40000, MaxTimestamp: ^uint64(0), MaxParseErrorCount: 50}},
		{"periods", rawDescriptor{GPU: gpu, PulsePeriod: a.pulsePeriod(gpu, 0), EraPeriod: a.eraPeriod(gpu, 0), CountPeriod: a.countPeriod(gpu, 0), ChunkSize: 0x1000, MaxTimestamp: ^uint64(0), MaxParseErrorCount: 50}},
		{"zeroed", rawDescriptor{GPU: gpu}},
	}
	for i := range cfgs {
		d := &cfgs[i].d
		pinner.Pin(d)
		got := a.parserCreate(unsafe.Pointer(d))
		t.Logf("parser_create[%s] = %#x", cfgs[i].name, got)
		if got != 0 && p == 0 {
			p, desc = got, d
		}
	}
	_ = desc
	defer pinner.Unpin()
	if p == 0 {
		t.Fatalf("parser_create returned null for all descriptor variants")
	}
	t.Logf("parser_is_valid = %v", a.parserIsValid(p))
	defer a.parserDestroy(p)

	path := os.Getenv("GPUTRACE_PROBE_RAW")
	if path == "" {
		t.Skip("set GPUTRACE_PROBE_RAW to a Profiling_f_*.raw path to parse")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("parsing %s (%d bytes)", path, len(data))
	var perr uint32
	pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &perr)
	t.Logf("parse(flags=1) profileData=%#x err=%d", pd, perr)
	if pd == 0 {
		perr = 0
		pd = a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 0x21, &perr)
		t.Logf("parse(flags=0x21) profileData=%#x err=%d", pd, perr)
	}
	if pd == 0 {
		return
	}
	nk, ne := a.pdKicksNum(pd), a.pdESLNum(pd)
	t.Logf("pd valid=%v chunkSize=%d kicks=%d eslCliques=%d", a.pdIsValid(pd), a.pdChunkSize(pd), nk, ne)

	fetch := func(g rangeGet, n uint64) []uint64 {
		if n > 8 {
			n = 8
		}
		out := make([]uint64, n)
		if n == 0 {
			return out
		}
		ok := g(pd, &out[0], 0, n)
		if !ok {
			t.Logf("  (range get returned false)")
		}
		return out
	}
	ks, ke, kid := fetch(a.pdKickStart, nk), fetch(a.pdKickEnd, nk), fetch(a.pdKickID, nk)
	t.Logf("  kick ids=%v", kid)
	t.Logf("  kick starts=%v", ks)
	t.Logf("  kick ends=%v", ke)
	es, ee, tr := fetch(a.pdESLStart, ne), fetch(a.pdESLEnd, ne), fetch(a.pdESLTrace, ne)
	t.Logf("  esl starts=%v", es)
	t.Logf("  esl ends=%v", ee)
	t.Logf("  esl traces=%#x", tr)
	// NOTE: the values returned by get_esl_clique_instruction_trace are small
	// (0x60-0x67), not pointers, so agxps_aps_clique_instruction_trace_get_*
	// cannot be called on them directly - doing so faults. The real
	// agxps_aps_clique_instruction_trace ref must come from somewhere else.
}
