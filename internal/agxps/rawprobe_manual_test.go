//go:build darwin

package agxps

import (
	"os"
	"runtime"
	"sort"
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
	// The trace ids came back 0x61,0x60,0x63,0x62,0x65,0x64,0x67,0x66, which
	// is exactly 0x60+(i^1): a contiguous run, reordered within adjacent
	// pairs. Whether the swap lives in the data or in our call is the
	// question -- a single-element read cannot be pair-swapped by anything,
	// so it separates the two.
	one := make([]uint64, 1)
	for _, first := range []uint64{0, 1, 2, 3} {
		if !a.pdESLTrace(pd, &one[0], first, 1) {
			t.Logf("  trace[first=%d count=1] returned false", first)
			continue
		}
		t.Logf("  trace[first=%d count=1] = %#x", first, one[0])
	}
	// Past the first 8, to see whether 8 was a boundary or just where the
	// dump above stopped.
	wide := make([]uint64, 16)
	if a.pdESLTrace(pd, &wide[0], 0, 16) {
		t.Logf("  trace[first=0 count=16] = %#x", wide)
	}
	// A window that does not start on an even index: if the pairing is
	// structural in the data, the values follow the id; if it is an artifact
	// of the copy loop, the swap re-anchors to the start of the window.
	off := make([]uint64, 4)
	if a.pdESLTrace(pd, &off[0], 1, 4) {
		t.Logf("  trace[first=1 count=4] = %#x", off)
	}
	// The ids are not a contiguous run: they break at 8 into a second group
	// with a high byte set. Read all of them and describe the space, rather
	// than extrapolating a pattern from the first handful.
	all := make([]uint64, ne)
	if ne > 0 && a.pdESLTrace(pd, &all[0], 0, ne) {
		seen := map[uint64]int{}
		hi := map[uint64]int{}
		lo := map[uint64]int{}
		var max uint64
		for _, v := range all {
			seen[v]++
			hi[v>>8]++
			lo[v&0xff]++
			if v > max {
				max = v
			}
		}
		t.Logf("  traces: n=%d distinct=%d max=%#x distinctHigh=%d distinctLow=%d",
			len(all), len(seen), max, len(hi), len(lo))
		var los []uint64
		for v := range lo {
			los = append(los, v)
		}
		sort.Slice(los, func(i, j int) bool { return los[i] < los[j] })
		t.Logf("  low bytes present: %#x", los)
		dup := 0
		for _, c := range seen {
			if c > 1 {
				dup++
			}
		}
		t.Logf("  ids appearing more than once: %d", dup)

		// distinct high == 2 * kicks exactly (5020 == 2*2510), which suggests
		// the high field decomposes again into a kick and a one-bit
		// subgroup. If so the highs for a kick are {2k, 2k+1} and halving
		// them collapses to the kick count.
		half := map[uint64]int{}
		pairs := map[uint64]map[uint64]bool{}
		for _, v := range all {
			h := v >> 8
			half[h>>1]++
			if pairs[h>>1] == nil {
				pairs[h>>1] = map[uint64]bool{}
			}
			pairs[h>>1][h&1] = true
		}
		both := 0
		for _, s := range pairs {
			if len(s) == 2 {
				both++
			}
		}
		t.Logf("  high>>1 distinct=%d (kicks=%d) groups holding both parities=%d", len(half), nk, both)

		// The low byte may be a kind tag plus a 3-bit slot rather than a
		// base: every value is 0b01100sss. Kick ids are a different object
		// class, so if they carry their own fixed prefix the tag is real.
		kids := make([]uint64, nk)
		if nk > 0 && a.pdKickID(pd, &kids[0], 0, nk) {
			klo := map[uint64]int{}
			khi := map[uint64]int{}
			kdistinct := map[uint64]bool{}
			var kmax uint64
			for _, v := range kids {
				klo[v&0xff]++
				khi[v>>32]++
				kdistinct[v] = true
				if v > kmax {
					kmax = v
				}
			}
			var los []uint64
			for v := range klo {
				los = append(los, v)
			}
			sort.Slice(los, func(i, j int) bool { return los[i] < los[j] })
			if len(los) > 16 {
				los = los[:16]
			}
			t.Logf("  kick ids: n=%d distinct=%d max=%#x distinctLow=%d distinctHigh32=%d",
				len(kids), len(kdistinct), kmax, len(klo), len(khi))
			t.Logf("  kick id low bytes (first 16): %#x", los)
			t.Logf("  kick ids[0:8] = %#x", kids[:min(8, len(kids))])
		}

		// The pairing might be a seam rather than a structure: if out is
		// uint32_t* for this accessor, reading it as uint64 fuses adjacent
		// entries into (out32[2k+1]<<32)|out32[2k]. Read into a uint32 buffer
		// and see whether the values are simply the index.
		probeWidth := func(name string, g rangeGet, n uint64) {
			buf32 := make([]uint32, n+8)
			if !g(pd, (*uint64)(unsafe.Pointer(&buf32[0])), 0, n) {
				t.Logf("  %s as uint32: returned false", name)
				return
			}
			identity, mismatch := 0, []int{}
			for i := uint64(0); i < n; i++ {
				if uint64(buf32[i]) == i {
					identity++
				} else if len(mismatch) < 8 {
					mismatch = append(mismatch, int(i))
				}
			}
			t.Logf("  %s as uint32[%d]: head=%v identity=%d/%d firstMismatches=%v",
				name, n, buf32[:min(uint64(12), n)], identity, n, mismatch)
			lo := uint64(0)
			if n > 4 {
				lo = n - 4
			}
			t.Logf("    tail=%v", buf32[lo:n])
		}
		probeWidth("kick ids", a.pdKickID, nk)
		probeWidth("esl traces", a.pdESLTrace, ne)

		// Element width is not a property of the accessor class: read into a
		// uint32 buffer and a 64-bit array shows a zero in every odd slot,
		// while a 32-bit array does not. Sentinel the buffer first so an
		// untouched tail is distinguishable from a written zero.
		width := func(name string, g rangeGet, n uint64) {
			if n == 0 {
				return
			}
			if n > 4096 {
				n = 4096
			}
			buf := make([]uint32, 2*n+8)
			for i := range buf {
				buf[i] = 0xDEADBEEF
			}
			if !g(pd, (*uint64)(unsafe.Pointer(&buf[0])), 0, n) {
				t.Logf("  width[%s]: returned false", name)
				return
			}
			oddZero, written := 0, 0
			for i := uint64(0); i < 2*n; i++ {
				if buf[i] != 0xDEADBEEF {
					written = int(i) + 1
				}
				if i%2 == 1 && buf[i] == 0 {
					oddZero++
				}
			}
			// Bytes written is the sound discriminator. An all-zero odd slot
			// is not: kick_start and kick_end are 64-bit timestamps large
			// enough to occupy their high word, so they look 32-bit by that
			// test while writing 8 bytes an element.
			guess := "uint32"
			if written >= 2*int(n) {
				guess = "uint64"
			}
			t.Logf("  width[%-11s] n=%-6d wordsWritten=%-6d bytesPerElem=%d oddSlotsZero=%d/%d => %s",
				name, n, written, written*4/int(n), oddZero, n, guess)
		}
		width("kick_id", a.pdKickID, nk)
		width("kick_start", a.pdKickStart, nk)
		width("kick_end", a.pdKickEnd, nk)
		width("esl_start", a.pdESLStart, ne)
		width("esl_end", a.pdESLEnd, ne)
		width("esl_trace", a.pdESLTrace, ne)

		// kick_id is not identity everywhere, and the disorder sits in short
		// local clusters rather than scattered. That is the shape of a
		// nearly-sorted permutation: if the records are sorted by start
		// timestamp, kick_id maps sorted position to submission order, and
		// the disordered sites should be exactly where timestamps tie.
		ids32 := make([]uint32, nk+8)
		starts := make([]uint64, nk)
		if nk > 0 && a.pdKickID(pd, (*uint64)(unsafe.Pointer(&ids32[0])), 0, nk) &&
			a.pdKickStart(pd, &starts[0], 0, nk) {
			desc := 0
			for j := uint64(1); j < nk; j++ {
				if starts[j] < starts[j-1] {
					desc++
				}
			}
			t.Logf("  kick_start descents=%d of %d (0 => sorted by start)", desc, nk-1)

			var sites, tied, adjacent int
			for j := uint64(0); j < nk; j++ {
				if uint64(ids32[j]) == j {
					continue
				}
				sites++
				if j > 0 && starts[j] == starts[j-1] {
					tied++
				} else if j > 0 && starts[j]-starts[j-1] < 64 {
					adjacent++
				}
			}
			t.Logf("  disordered positions=%d tiedWithPrev=%d withinp64Ticks=%d", sites, tied, adjacent)

			// Show the first few sites with their timestamps, so a tie is
			// visible rather than asserted.
			shown := 0
			for j := uint64(1); j < nk && shown < 6; j++ {
				if uint64(ids32[j]) == j {
					continue
				}
				t.Logf("    j=%-5d id=%-5d start=%d delta_from_prev=%d",
					j, ids32[j], starts[j], starts[j]-starts[j-1])
				shown++
			}

			// Array order is not sorted by start. But at j=2,3 the ids swap
			// exactly where the starts descend, so the reverse may hold:
			// reordering by id may be what sorts them.
			byID := make([]uint64, nk)
			ok := true
			for j := uint64(0); j < nk; j++ {
				if uint64(ids32[j]) >= nk {
					ok = false
					break
				}
				byID[ids32[j]] = starts[j]
			}
			if ok {
				d := 0
				for j := uint64(1); j < nk; j++ {
					if byID[j] < byID[j-1] {
						d++
					}
				}
				t.Logf("  after permuting by kick_id: descents=%d of %d", d, nk-1)
			}
		}
	}
}
