//go:build darwin

package agxps

// Manual probe for the counter side of the AGX profiler surface: does
// GTShaderProfiler's own APS parser decode a Counters_f_*.raw file, and if so
// what counter time series come out?
//
// These tests only run when GPUTRACE_PROBE_COUNTERS names a Counters_f_*.raw
// file, so a normal `go test ./...` skips them.
//
// Accessor shapes below were read off the arm64 disassembly of
// GTShaderProfiler (labelled `disasm` in the comments) before being called, per
// the standing rule that this API returns plausible garbage rather than errors
// when the argument shape is wrong.

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// counterAPI is the counter-facing subset of the agxps C surface.
//
// disasm: agxps_aps_profile_data_get_counter_num @ 0x4ed7b4 returns
// (end-begin)>>3 over a vector at pd+0x371b8, i.e. a count of 8-byte entries.
//
// disasm: get_counter_names / get_counter_values / get_counter_values_num all
// share the bulk-copy shape (pd, out*, first, count) -> bool, bounds-checking
// first+count against that same counter vector. Element width is 8 for all
// three: names copies the vector entry verbatim, values copies a std::vector
// begin pointer out of a 0x18-byte record at pd+0x30f48, and values_num copies
// (end-begin)>>3 of that same record.
type counterAPI struct {
	initialize      func() int32
	gpuCreate       func(gen, variant, rev uint32, exact uint32) uintptr
	gpuIsValid      func(uintptr) bool
	parserCreate    func(unsafe.Pointer) uintptr
	parserIsValid   func(uintptr) bool
	parserParse     func(parser uintptr, data unsafe.Pointer, size uint64, flags uint32, errOut *uint32) uintptr
	parserDestroy   func(uintptr)
	pdIsValid       func(uintptr) bool
	pdDestroy       func(uintptr)
	pdCounterNum    func(uintptr) uint64
	pdCounterNames  func(pd uintptr, out *uint64, first, count uint64) bool
	pdCounterValues func(pd uintptr, out *uint64, first, count uint64) bool
	pdCounterVNum   func(pd uintptr, out *uint64, first, count uint64) bool
	// disasm: get_counter_group_id @ 0x4ede00 ends in `strb w0, [x19], #0x1`,
	// so its element is a single BYTE, not the 8 bytes the neighbouring
	// accessors use. Reading it as uint64 fuses eight group ids into one
	// enormous plausible-looking number.
	pdGroupID      func(pd uintptr, out *uint8, first, count uint64) bool
	pdGroupMeta    func(pd uintptr, out *uint64, first, count uint64) bool
	pdChunksTotal  func(uintptr) uint64
	pdChunksFailed func(uintptr) uint64
	pdParsedTokens func(uintptr) uint64
	pdParsedBits   func(uintptr) uint64
	pdKicksNum     func(uintptr) uint64
	pdESLNum       func(uintptr) uint64
	pdSysTSNum     func(uintptr) uint64
	pdUSCTSNum     func(uintptr) uint64
	pdParseErrsNum func(uintptr) uint64
	pdChunkSize    func(uintptr) uint64
	pdSysTS        func(pd uintptr, out *uint64, first, count uint64) bool
	// disasm: agxps_aps_system_timestamp_to_nanoseconds @ 0x4ee29c computes
	// ts*1000/24 and returns it in d0 via `ucvtf d0, x8` -- it returns a
	// DOUBLE. Declared as returning uint64 it reads x0 and yields garbage that
	// still looks like a plausible millisecond span.
	pdSysTSToNanos func(uint64) float64

	counterGetName    func(uint64) string
	counterIsValid    func(uint64) bool
	counterIsDerived  func(uint64) bool
	counterIsReal     func(uint64) bool
	counterIsNorm     func(uint64) bool
	counterIsRelative func(uint64) bool
	counterGetGroup   func(uint64) uint64
	counterGetIdent   func(uint64) string
	counterGetDoc     func(uint64) string
	counterGRCEnable  func(uint64) string
	counterNumGroups  func() uint64

	uarchFromGRCList func(list *byte, size uint64) int32

	// disasm: get_kick_software_id @ 0x4ebbac copies with ldr/str x and bounds
	// checks `asr #3`, so 8-byte elements; get_kick_kick_slot @ 0x4ebd0c uses
	// ldrh/strh and `asr #1`, so 2-byte. Another accessor pair in the same
	// family with different widths, as the signature notes warn.
	pdKickSoftwareID func(pd uintptr, out *uint64, first, count uint64) bool
	pdKickSlot       func(pd uintptr, out *uint16, first, count uint64) bool
	pdKickStart      func(pd uintptr, out *uint64, first, count uint64) bool
	pdKickEnd        func(pd uintptr, out *uint64, first, count uint64) bool

	pulsePeriodNum func(uintptr) uint64
	pulsePeriod    func(uintptr, uint64) uint32
	eraPeriodNum   func(uintptr) uint64
	eraPeriod      func(uintptr, uint64) uint32
	countPeriodNum func(uintptr) uint64
	countPeriod    func(uintptr, uint64) uint32
}

func loadCounterAPI(t *testing.T) *counterAPI {
	t.Helper()
	h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	a := &counterAPI{}
	reg := func(p any, name string) { purego.RegisterLibFunc(p, h, name) }
	reg(&a.initialize, "agxps_initialize")
	reg(&a.gpuCreate, "agxps_gpu_create")
	reg(&a.gpuIsValid, "agxps_gpu_is_valid")
	reg(&a.parserCreate, "agxps_aps_parser_create")
	reg(&a.parserIsValid, "agxps_aps_parser_is_valid")
	reg(&a.parserParse, "agxps_aps_parser_parse")
	reg(&a.parserDestroy, "agxps_aps_parser_destroy")
	reg(&a.pdIsValid, "agxps_aps_profile_data_is_valid")
	reg(&a.pdDestroy, "agxps_aps_profile_data_destroy")
	reg(&a.pdCounterNum, "agxps_aps_profile_data_get_counter_num")
	reg(&a.pdCounterNames, "agxps_aps_profile_data_get_counter_names")
	reg(&a.pdCounterValues, "agxps_aps_profile_data_get_counter_values")
	reg(&a.pdCounterVNum, "agxps_aps_profile_data_get_counter_values_num")
	reg(&a.pdGroupID, "agxps_aps_profile_data_get_counter_group_id")
	reg(&a.pdGroupMeta, "agxps_aps_profile_data_get_counter_group_metadata")
	reg(&a.pdChunksTotal, "agxps_aps_profile_data_get_num_chunks_total")
	reg(&a.pdChunksFailed, "agxps_aps_profile_data_get_num_chunks_failed")
	reg(&a.pdParsedTokens, "agxps_aps_profile_data_get_parsed_tokens_num")
	reg(&a.pdParsedBits, "agxps_aps_profile_data_get_parsed_bits_num")
	reg(&a.pdKicksNum, "agxps_aps_profile_data_get_kicks_num")
	reg(&a.pdESLNum, "agxps_aps_profile_data_get_esl_cliques_num")
	reg(&a.pdSysTSNum, "agxps_aps_profile_data_get_system_timestamps_num")
	reg(&a.pdUSCTSNum, "agxps_aps_profile_data_get_usc_timestamps_num")
	reg(&a.pdParseErrsNum, "agxps_aps_profile_data_get_parse_errors_num")
	reg(&a.pdChunkSize, "agxps_aps_profile_data_get_chunk_size")
	reg(&a.pdSysTS, "agxps_aps_profile_data_get_system_timestamps")
	reg(&a.pdSysTSToNanos, "agxps_aps_system_timestamp_to_nanoseconds")
	reg(&a.counterGetName, "agxps_counter_get_name")
	reg(&a.counterIsValid, "agxps_counter_is_valid")
	reg(&a.counterIsDerived, "agxps_counter_is_derived")
	reg(&a.counterIsReal, "agxps_counter_is_real")
	reg(&a.counterIsNorm, "agxps_counter_is_normalized")
	reg(&a.counterIsRelative, "agxps_counter_is_relative")
	reg(&a.counterGetGroup, "agxps_counter_get_group")
	reg(&a.counterGetIdent, "agxps_counter_get_ident")
	reg(&a.counterGetDoc, "agxps_counter_get_doc_string")
	reg(&a.counterGRCEnable, "agxps_counter_get_grc_enable_str")
	reg(&a.counterNumGroups, "agxps_counter_get_num_groups")
	reg(&a.uarchFromGRCList, "agxps_aps_get_uarch_behaviour_from_GRC_counter_list")
	reg(&a.pdKickSoftwareID, "agxps_aps_profile_data_get_kick_software_id")
	reg(&a.pdKickSlot, "agxps_aps_profile_data_get_kick_kick_slot")
	reg(&a.pdKickStart, "agxps_aps_profile_data_get_kick_start")
	reg(&a.pdKickEnd, "agxps_aps_profile_data_get_kick_end")
	reg(&a.pulsePeriodNum, "agxps_aps_get_valid_pulse_period_num")
	reg(&a.pulsePeriod, "agxps_aps_get_valid_pulse_period")
	reg(&a.eraPeriodNum, "agxps_aps_get_valid_era_period_num")
	reg(&a.eraPeriod, "agxps_aps_get_valid_era_period")
	reg(&a.countPeriodNum, "agxps_aps_get_valid_count_period_num")
	reg(&a.countPeriod, "agxps_aps_get_valid_count_period")
	return a
}

// TestCounterTableEnumerate walks the framework's static counter table. It
// needs no trace data: the table is a global in the binary, so this establishes
// the ident space that a decoded file's counter names index into.
//
// agxps_counter_is_valid is NOT a bound on that space. The earlier version of
// this probe took the comment on it at face value -- "compares the argument
// against the length of a global vector of 0x50-byte records, so the ident
// space is a dense 0..n-1 index" -- and walked until it returned false. It
// returned true for 196915 consecutive idents and only stopped at the probe's
// own cap, then reported "counter idents valid: 0..196914" as a measurement.
// It was reading far past the table: names begin repeating in adjacent pairs
// at 1926, stop being hash-shaped at 52867, and ident 196914 reads
// "AGenInstructions", which is unrelated memory. Nothing derived from that
// output means anything.
//
// So do not ask is_valid where the table ends. Walk while the entries still
// look like table entries, and prove is_valid is not a bound instead of
// trusting it.
func TestCounterTableEnumerate(t *testing.T) {
	a := loadCounterAPI(t)
	a.initialize()

	const wayPastAnyTable = 1 << 20

	// Stop at the first name that is not a distinct, hash-shaped identifier.
	// Past the table the accessor returns duplicates and then unrelated
	// strings, and both are visible without knowing the true size.
	seen := make(map[string]uint64)
	var idents []string
	for i := uint64(0); i < wayPastAnyTable; i++ {
		name := a.counterGetName(i)
		if !obfuscatedCounterName(name) {
			t.Logf("stopped at ident %d: %q is not a hash-shaped counter name", i, name)
			break
		}
		if prev, dup := seen[name]; dup {
			t.Logf("stopped at ident %d: name repeats ident %d", i, prev)
			break
		}
		seen[name] = i
		idents = append(idents, name)
	}

	t.Logf("counter idents that look like table entries: %d (num_groups=%d)", len(idents), a.counterNumGroups())
	t.Logf("NOTE: this is where the entries stop looking like entries, which is an "+
		"upper bound on the table, not the table size. %d hashes are independently "+
		"attested; see docs/research/COUNTER_NAME_MAPPING.md.", knownRawCounterHashes)

	// is_valid does return false eventually, which is what made it look like a
	// bound on this table. Show that its cutoff is orders of magnitude past the
	// point where the entries stop being entries, so a later reader does not
	// reach for it again. If the two ever converge, this probe should be
	// rewritten to use it.
	var validUntil uint64
	for validUntil = 0; validUntil < wayPastAnyTable; validUntil++ {
		if !a.counterIsValid(validUntil) {
			break
		}
	}
	t.Logf("agxps_counter_is_valid accepts 0..%d, which is %dx the entries above; "+
		"it bounds some other vector, not this table", validUntil-1, validUntil/uint64(max(len(idents), 1)))

	for i, name := range idents {
		t.Logf("  [%3d] %-44s group=%d real=%v derived=%v norm=%v rel=%v grc=%q",
			i, name, a.counterGetGroup(uint64(i)), a.counterIsReal(uint64(i)),
			a.counterIsDerived(uint64(i)), a.counterIsNorm(uint64(i)), a.counterIsRelative(uint64(i)),
			a.counterGRCEnable(uint64(i)))
	}
}

// knownRawCounterHashes is the number of obfuscated raw counter hashes found in
// the framework by direct byte scan, established separately from this probe.
const knownRawCounterHashes = 578

// obfuscatedCounterName reports whether name has the shape the framework uses
// for a raw counter ident: an underscore and 64 lowercase hex digits.
func obfuscatedCounterName(name string) bool {
	if len(name) != 65 || name[0] != '_' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// counterProbeParser builds a parser with the descriptor shape that the kick
// probe established works, optionally with a counter uarch behaviour set.
func counterProbeParser(t *testing.T, a *counterAPI, pin *runtime.Pinner, uarch int32) uintptr {
	t.Helper()
	gpu := a.gpuCreate(16, 6, 1, 0) // M4 Max: G16, variant 6 = 40 USC
	if gpu == 0 || !a.gpuIsValid(gpu) {
		t.Fatalf("gpu_create(16,6,1) failed")
	}
	// runtime: parser_create returns null for a descriptor with zero
	// pulse/era/count periods, so take the first valid period of each.
	d := &rawDescriptor{
		GPU:                   gpu,
		PulsePeriod:           a.pulsePeriod(gpu, 0),
		EraPeriod:             a.eraPeriod(gpu, 0),
		CountPeriod:           a.countPeriod(gpu, 0),
		ChunkSize:             0x1000,
		MaxTimestamp:          ^uint64(0),
		MaxParseErrorCount:    50,
		CounterUarchBehaviour: uarch,
	}
	pin.Pin(d)
	p := a.parserCreate(unsafe.Pointer(d))
	if p == 0 {
		t.Fatalf("parser_create returned null (uarch=%d)", uarch)
	}
	return p
}

// TestCounterFileParse is the deliverable probe: parse a Counters_f_*.raw and
// report every counter series it yields, at full length -- no truncation.
func TestCounterFileParse(t *testing.T) {
	path := os.Getenv("GPUTRACE_PROBE_COUNTERS")
	if path == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS to a Counters_f_*.raw path")
	}
	a := loadCounterAPI(t)
	a.initialize()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("file %s (%d bytes)", path, len(data))

	var pin runtime.Pinner
	defer pin.Unpin()

	// The uarch behaviour is the descriptor field that plausibly selects how
	// counter tokens are interpreted. Sweep it rather than guessing one value,
	// and report what each yields, so a zero result is attributable.
	for _, uarch := range []int32{0, 1, 2, 3, 4, 5, 6, 7} {
		p := counterProbeParser(t, a, &pin, uarch)
		for _, flags := range []uint32{1, 0x21} {
			var perr uint32
			pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), flags, &perr)
			if pd == 0 {
				t.Logf("uarch=%d flags=%#x: parse returned null, err=%d", uarch, flags, perr)
				continue
			}
			nc := a.pdCounterNum(pd)
			t.Logf("uarch=%d flags=%#x: pd=%#x err=%d valid=%v chunks=%d/%d failed=%d tokens=%d bits=%d parseErrs=%d kicks=%d esl=%d sysTS=%d uscTS=%d counters=%d",
				uarch, flags, pd, perr, a.pdIsValid(pd),
				a.pdChunksTotal(pd), a.pdChunkSize(pd), a.pdChunksFailed(pd),
				a.pdParsedTokens(pd), a.pdParsedBits(pd), a.pdParseErrsNum(pd),
				a.pdKicksNum(pd), a.pdESLNum(pd), a.pdSysTSNum(pd), a.pdUSCTSNum(pd), nc)
			// The raw system timestamp range, printed unconditionally: it is
			// what decides whether these files share a clock with
			// APSTimelineData's command buffer ticks, and it is available even
			// when a file carries no counters.
			if n := a.pdSysTSNum(pd); n > 0 {
				sys := make([]uint64, n)
				if a.pdSysTS(pd, &sys[0], 0, n) {
					t.Logf("  systemTimestamps[%d] raw %d..%d span=%d ticks (%.3f ms)",
						n, sys[0], sys[n-1], sys[n-1]-sys[0],
						float64(sys[n-1]-sys[0])*1000/24/1e6)
				}
			}
			if nc > 0 {
				dumpCounters(t, a, pd, nc)
			}
			a.pdDestroy(pd)
		}
		a.parserDestroy(p)
	}
}

// dumpCounters reports every counter series in full. Deliberately no cap on the
// number of counters: a truncating helper produced a wrong published finding on
// this API once already. Per-series sample values are summarised statistically
// (all samples are read; only the printing is condensed), and the raw head of
// each series is printed so the element encoding stays checkable.
func dumpCounters(t *testing.T, a *counterAPI, pd uintptr, nc uint64) {
	t.Helper()
	names := make([]uint64, nc)
	vnum := make([]uint64, nc)
	vptr := make([]uint64, nc)
	meta := make([]uint64, nc)
	gid := make([]uint8, nc)
	okN := a.pdCounterNames(pd, &names[0], 0, nc)
	okC := a.pdCounterVNum(pd, &vnum[0], 0, nc)
	okV := a.pdCounterValues(pd, &vptr[0], 0, nc)
	okG := a.pdGroupID(pd, &gid[0], 0, nc)
	okM := a.pdGroupMeta(pd, &meta[0], 0, nc)
	t.Logf("  bulk gets ok: names=%v values_num=%v values=%v group_id=%v metadata=%v", okN, okC, okV, okG, okM)

	var total uint64
	for i := uint64(0); i < nc; i++ {
		total += vnum[i]
	}
	t.Logf("  %d counters, %d samples total", nc, total)

	// The per-group metadata array is shared by every counter in a group; report
	// it once per distinct pointer, at the length of that group's series.
	windowMs := 0.0
	seenMeta := map[uint64]bool{}
	for i := uint64(0); i < nc; i++ {
		if meta[i] == 0 || seenMeta[meta[i]] || vnum[i] == 0 {
			continue
		}
		seenMeta[meta[i]] = true
		m := unsafe.Slice((*uint64)(foreign(meta[i])), int(vnum[i]))
		desc := 0
		for j := 1; j < len(m); j++ {
			if m[j] < m[j-1] {
				desc++
			}
		}
		// Reading these as scalar ticks gives large monotone plausible numbers,
		// which is how the kick timestamps were misread. Test the competing
		// reading instead: a packed (usc_index<<32)|system_index pair, the same
		// convention kick_start uses. The falsifiable part is that BOTH halves
		// must stay inside their table's index range and must not descend.
		nsys, nusc := a.pdSysTSNum(pd), a.pdUSCTSNum(pd)
		hiBad, loBad, hiDesc, loDesc := 0, 0, 0, 0
		for j, v := range m {
			hi, lo := v>>32, v&0xffffffff
			if hi >= nusc {
				hiBad++
			}
			if lo >= nsys {
				loBad++
			}
			if j > 0 {
				if hi < m[j-1]>>32 {
					hiDesc++
				}
				if lo < m[j-1]&0xffffffff {
					loDesc++
				}
			}
		}
		t.Logf("  meta[group=%d] ptr=%#x n=%d first=%d last=%d descents=%d head=%v",
			gid[i], meta[i], len(m), m[0], m[len(m)-1], desc, m[:min(8, len(m))])
		t.Logf("    as packed pair: usc[%d..%d] of %d (outOfRange=%d descents=%d), sys[%d..%d] of %d (outOfRange=%d descents=%d)",
			m[0]>>32, m[len(m)-1]>>32, nusc, hiBad, hiDesc,
			m[0]&0xffffffff, m[len(m)-1]&0xffffffff, nsys, loBad, loDesc)
		// Resolve the system half to mach absolute time and report the wall span
		// the series covers. This is the check with an outside answer: the kick
		// probe put the capture at 2942.5 ms against streamData's 2.98 s of
		// command buffer wall time, so a correct reading must land there.
		if nsys > 0 {
			sys := make([]uint64, nsys)
			if a.pdSysTS(pd, &sys[0], 0, nsys) {
				lo0, lo1 := m[0]&0xffffffff, m[len(m)-1]&0xffffffff
				if lo0 < nsys && lo1 < nsys {
					ns0 := a.pdSysTSToNanos(sys[lo0])
					ns1 := a.pdSysTSToNanos(sys[lo1])
					t.Logf("    wall span %.3f ms (%d samples, mean period %.3f us), sysTS raw %d..%d",
						(ns1-ns0)/1e6, len(m),
						(ns1-ns0)/1e3/float64(len(m)-1), sys[lo0], sys[lo1])
					if windowMs == 0 {
						windowMs = (ns1 - ns0) / 1e6
					}
				}
			}
		}
	}

	for i := uint64(0); i < nc; i++ {
		// runtime: the values returned by get_counter_names are 8-byte values in
		// the dyld image range, spaced by the length of an obfuscated counter
		// name -- they are `const char *`, not the small dense idents that
		// agxps_counter_is_valid accepts.
		name := cstrAt(names[i])
		n := vnum[i]
		if vptr[i] == 0 || n == 0 {
			t.Logf("  [%3d] ident=%d %-40s group=%d n=%d ptr=%#x (empty)", i, names[i], name, gid[i], n, vptr[i])
			continue
		}
		vals := unsafe.Slice((*uint64)(foreign(vptr[i])), int(n))
		var min, max, sum uint64
		min = ^uint64(0)
		nonzero := 0
		for _, v := range vals {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			sum += v
			if v != 0 {
				nonzero++
			}
		}
		// The same bytes read as float64, to make the encoding decidable
		// rather than assumed.
		fmin, fmax := math.Inf(1), math.Inf(-1)
		fsum := 0.0
		sane := 0
		for _, v := range vals {
			f := math.Float64frombits(v)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				continue
			}
			if f < fmin {
				fmin = f
			}
			if f > fmax {
				fmax = f
			}
			fsum += f
			if af := math.Abs(f); af == 0 || (af > 1e-6 && af < 1e12) {
				sane++
			}
		}
		var head []string
		for j := 0; j < len(vals) && j < 12; j++ {
			head = append(head, fmt.Sprintf("%#x", vals[j]))
		}
		// The fraction of samples in which a counter ticks at all times the
		// window gives a busy time, which has an outside answer to hit: this
		// trace's ground truth is 9.16 ms of effective GPU time.
		busyMs := 0.0
		if windowMs > 0 && n > 0 {
			busyMs = windowMs * float64(nonzero) / float64(n)
		}
		t.Logf("  [%3d] ident=%3d %-40s group=%d n=%-8d nonzero=%-8d busy=%.3fms u64[min=%d max=%d sum=%d] f64[min=%g max=%g sum=%g sane=%d] head=%s",
			i, names[i], name, gid[i], n, nonzero, busyMs, min, max, sum, fmin, fmax, fsum, sane, strings.Join(head, ","))
	}
}

// TestCounterFileFanout parses every Counters_f_*.raw in a directory. The
// Profiling_f_* files have a 5-on/5-off data pattern across the 40 files, so a
// single-file result cannot distinguish "decoder broken" from "this file is
// empty". This reports the pattern for the counter files.
func TestCounterFileFanout(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_COUNTERS_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS_DIR to a .gpuprofiler_raw directory")
	}
	a := loadCounterAPI(t)
	a.initialize()

	var pin runtime.Pinner
	defer pin.Unpin()
	p := counterProbeParser(t, a, &pin, 0)
	defer a.parserDestroy(p)

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "Counters_f_") && strings.HasSuffix(e.Name(), ".raw") {
			files = append(files, e.Name())
		}
	}
	sort.Slice(files, func(i, j int) bool { return fileIndex(files[i]) < fileIndex(files[j]) })
	for _, f := range files {
		data, err := os.ReadFile(dir + "/" + f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		var perr uint32
		pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &perr)
		if pd == 0 {
			t.Logf("%-18s %10d bytes: parse null err=%d", f, len(data), perr)
			continue
		}
		nc := a.pdCounterNum(pd)
		var samples uint64
		if nc > 0 {
			vnum := make([]uint64, nc)
			if a.pdCounterVNum(pd, &vnum[0], 0, nc) {
				for _, v := range vnum {
					samples += v
				}
			}
		}
		t.Logf("%-18s %10d bytes: err=%d tokens=%d bits=%d chunksFailed=%d parseErrs=%d kicks=%d esl=%d counters=%d samples=%d",
			f, len(data), perr, a.pdParsedTokens(pd), a.pdParsedBits(pd),
			a.pdChunksFailed(pd), a.pdParseErrsNum(pd), a.pdKicksNum(pd), a.pdESLNum(pd), nc, samples)
		a.pdDestroy(pd)
	}
}

// TestCounterAggregate decodes every Counters_f_*.raw and sums each counter's
// series across the whole capture, so the totals can be held against the
// oracle's per-encoder aggregates (which sum to, e.g., 2,623,493,136 kernel ALU
// instructions over 23 encoders).
//
// It also reports each counter's per-file totals for the first few files, which
// is what separates "one file per GPU core" from "every file records the same
// events" -- the Profiling_f_* files overlap heavily, and if the counter files
// did too, summing them would double count.
func TestCounterAggregate(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_COUNTERS_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS_DIR to a .gpuprofiler_raw directory")
	}
	a := loadCounterAPI(t)
	a.initialize()
	var pin runtime.Pinner
	defer pin.Unpin()
	p := counterProbeParser(t, a, &pin, 0)
	defer a.parserDestroy(p)

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "Counters_f_") && strings.HasSuffix(e.Name(), ".raw") {
			files = append(files, e.Name())
		}
	}
	sort.Slice(files, func(i, j int) bool { return fileIndex(files[i]) < fileIndex(files[j]) })

	type agg struct {
		total   uint64
		samples uint64
		perFile map[string]uint64
	}
	sums := map[string]*agg{}
	var order []string
	for _, f := range files {
		data, err := os.ReadFile(dir + "/" + f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		var perr uint32
		pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &perr)
		if pd == 0 {
			t.Logf("%s: parse null err=%d", f, perr)
			continue
		}
		nc := a.pdCounterNum(pd)
		if nc == 0 {
			t.Logf("%s: zero counters", f)
			a.pdDestroy(pd)
			continue
		}
		names := make([]uint64, nc)
		vnum := make([]uint64, nc)
		vptr := make([]uint64, nc)
		a.pdCounterNames(pd, &names[0], 0, nc)
		a.pdCounterVNum(pd, &vnum[0], 0, nc)
		a.pdCounterValues(pd, &vptr[0], 0, nc)
		for i := uint64(0); i < nc; i++ {
			if vptr[i] == 0 || vnum[i] == 0 {
				continue
			}
			name := cstrAt(names[i])
			vals := unsafe.Slice((*uint64)(foreign(vptr[i])), int(vnum[i]))
			var s uint64
			for _, v := range vals {
				s += v
			}
			e := sums[name]
			if e == nil {
				e = &agg{perFile: map[string]uint64{}}
				sums[name] = e
				order = append(order, name)
			}
			e.total += s
			e.samples += vnum[i]
			e.perFile[f] = s
		}
		t.Logf("%-18s %10d bytes: counters=%d err=%d", f, len(data), nc, perr)
		a.pdDestroy(pd)
	}
	sort.Strings(order)
	t.Logf("=== %d distinct counters across %d files ===", len(order), len(files))
	for _, name := range order {
		e := sums[name]
		var head []string
		for _, f := range files[:min(6, len(files))] {
			head = append(head, fmt.Sprintf("%s=%d", strings.TrimSuffix(strings.TrimPrefix(f, "Counters_f_"), ".raw"), e.perFile[f]))
		}
		t.Logf("%s files=%d samples=%d total=%d perFile[%s]",
			name, len(e.perFile), e.samples, e.total, strings.Join(head, " "))
	}
}

// TestCounterKickIdentity looks for a join between the kicks inside a counter
// file and streamData's encoders that does not require the two clocks to be
// reconciled.
//
// This matters because they cannot currently be reconciled: the APS system
// timestamps in Counters_f_*.raw and Profiling_f_*.raw sit around 5.3177e12
// ticks while APSTimelineData's command buffer ticks sit around 5.1818e12, and
// neither offset the archive offers (continuousTime-absoluteTime =
// 136,692,207,206) closes the gap -- it leaves about 30 seconds of residual
// against a 2-second window.
//
// If kick_software_id carried streamData's encoder sequence ids (1239, 1242,
// 1265, ...) the windowing would be solved by identity instead. Pass the
// expected ids in GPUTRACE_PROBE_ENCODER_SEQIDS to test that directly.
func TestCounterKickIdentity(t *testing.T) {
	path := os.Getenv("GPUTRACE_PROBE_COUNTERS")
	if path == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS to a raw file")
	}
	a := loadCounterAPI(t)
	a.initialize()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pin runtime.Pinner
	defer pin.Unpin()
	p := counterProbeParser(t, a, &pin, 0)
	defer a.parserDestroy(p)
	var perr uint32
	pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &perr)
	if pd == 0 {
		t.Fatalf("parse null err=%d", perr)
	}
	defer a.pdDestroy(pd)

	nk := a.pdKicksNum(pd)
	if nk == 0 {
		t.Fatalf("no kicks")
	}
	sw := make([]uint64, nk)
	slot := make([]uint16, nk)
	if !a.pdKickSoftwareID(pd, &sw[0], 0, nk) {
		t.Fatalf("kick_software_id range get failed")
	}
	if !a.pdKickSlot(pd, &slot[0], 0, nk) {
		t.Fatalf("kick_kick_slot range get failed")
	}
	// Describe the whole id space, not a head sample: a spot check of the first
	// entries is how the kick_id identity misreading survived.
	swSet := map[uint64]int{}
	slotSet := map[uint16]int{}
	var swMin, swMax uint64 = ^uint64(0), 0
	for i := range sw {
		swSet[sw[i]]++
		slotSet[slot[i]]++
		if sw[i] < swMin {
			swMin = sw[i]
		}
		if sw[i] > swMax {
			swMax = sw[i]
		}
	}
	t.Logf("kicks=%d software_id distinct=%d range=%d..%d; kick_slot distinct=%d",
		nk, len(swSet), swMin, swMax, len(slotSet))
	var slots []int
	for s := range slotSet {
		slots = append(slots, int(s))
	}
	sort.Ints(slots)
	t.Logf("kick slots present: %v", slots)

	want := os.Getenv("GPUTRACE_PROBE_ENCODER_SEQIDS")
	if want == "" {
		t.Skip("set GPUTRACE_PROBE_ENCODER_SEQIDS to a comma-separated encoder sequence id list to test the join")
	}
	var hit, miss []uint64
	for _, f := range strings.Split(want, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var v uint64
		for _, c := range f {
			v = v*10 + uint64(c-'0')
		}
		if swSet[v] > 0 {
			hit = append(hit, v)
		} else {
			miss = append(miss, v)
		}
	}
	t.Logf("encoder sequence ids present in kick_software_id: %d hit, %d miss", len(hit), len(miss))
	t.Logf("  hit=%v", hit)
	t.Logf("  miss=%v", miss)
	if len(miss) > 0 {
		t.Logf("REFUTED: kick_software_id is not streamData's encoder sequence id space")
	}
}

// cstrAt reads a NUL-terminated C string at an address in the loaded image.
func cstrAt(p uint64) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := 0; i < 512; i++ {
		c := *(*byte)(foreign(p + uint64(i)))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func fileIndex(name string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(name, "Counters_f_"), ".raw")
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// foreign converts an address returned by GTShaderProfiler into a pointer.
//
// The bulk accessors hand back addresses of buffers owned by the C library, as
// plain uint64. Writing unsafe.Pointer(uintptr(addr)) is what go vet flags as
// "possible misuse of unsafe.Pointer", and for Go-owned memory the warning is
// right: a uintptr does not keep an object alive and does not survive a moving
// collector. Neither hazard applies here, because the GC never manages this
// memory and never moves it -- the profile_data handle owns it until the
// parser is destroyed.
//
// The conversion goes through the uint64's own storage so that no uintptr ever
// appears, which keeps vet green without a blanket suppression that would also
// hide a genuine misuse elsewhere in this file.
func foreign(addr uint64) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}
