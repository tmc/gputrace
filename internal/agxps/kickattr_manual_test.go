//go:build darwin

package agxps

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The kick accessors are struct-of-array bulk copies whose element widths were
// read off the disassembly of GTShaderProfiler (x86_64 slice):
//
//	kick_start        vec at pd+0x08/0x10, sarq $3  -> 8 bytes
//	kick_end          vec at pd+0x20/0x28, sarq $3  -> 8 bytes
//	kick_software_id  vec at pd+0x38/0x40, sarq $3  -> 8 bytes
//	kick_telemetry    vec at pd+0x50/0x58, sarq $3  -> 8 bytes
//	kick_id           vec at pd+0x68/0x70, sarq $2  -> 4 bytes
//	kick_data_master  vec at pd+0x80/0x88, sarq $2  -> 4 bytes
//	kick_kick_slot    vec at pd+0x98/0xa0, sarq $1  -> 2 bytes
//	kick_missing_end  bitset at pd+0xb0, count at pd+0xb8, one bool out per kick
//
// kick_id's 4 bytes agrees with the independent sentinel measurement recorded
// in agxps-signatures.yaml, which is the cross-check that the disassembly is
// being read correctly.
type kickAPI struct {
	initialize    func() int32
	gpuCreate     func(gen, variant, rev uint32, exact uint32) uintptr
	pulsePeriod   func(uintptr, uint64) uint32
	eraPeriod     func(uintptr, uint64) uint32
	countPeriod   func(uintptr, uint64) uint32
	parserCreate  func(unsafe.Pointer) uintptr
	parserParse   func(parser uintptr, data unsafe.Pointer, size uint64, flags uint32, errOut *uint32) uintptr
	parserDestroy func(uintptr)
	pdDestroy     func(uintptr)
	kicksNum      func(uintptr) uint64
	getU64        map[string]func(pd uintptr, out *uint64, first, count uint64) bool
	getU32        map[string]func(pd uintptr, out *uint32, first, count uint64) bool
	getU16        map[string]func(pd uintptr, out *uint16, first, count uint64) bool
	getBool       func(pd uintptr, out *bool, first, count uint64) bool
}

func loadKickAPI(t *testing.T) *kickAPI {
	t.Helper()
	h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	a := &kickAPI{
		getU64: map[string]func(uintptr, *uint64, uint64, uint64) bool{},
		getU32: map[string]func(uintptr, *uint32, uint64, uint64) bool{},
		getU16: map[string]func(uintptr, *uint16, uint64, uint64) bool{},
	}
	purego.RegisterLibFunc(&a.initialize, h, "agxps_initialize")
	purego.RegisterLibFunc(&a.gpuCreate, h, "agxps_gpu_create")
	purego.RegisterLibFunc(&a.pulsePeriod, h, "agxps_aps_get_valid_pulse_period")
	purego.RegisterLibFunc(&a.eraPeriod, h, "agxps_aps_get_valid_era_period")
	purego.RegisterLibFunc(&a.countPeriod, h, "agxps_aps_get_valid_count_period")
	purego.RegisterLibFunc(&a.parserCreate, h, "agxps_aps_parser_create")
	purego.RegisterLibFunc(&a.parserParse, h, "agxps_aps_parser_parse")
	purego.RegisterLibFunc(&a.parserDestroy, h, "agxps_aps_parser_destroy")
	purego.RegisterLibFunc(&a.pdDestroy, h, "agxps_aps_profile_data_destroy")
	purego.RegisterLibFunc(&a.kicksNum, h, "agxps_aps_profile_data_get_kicks_num")
	for _, n := range []string{"start", "end", "software_id", "telemetry"} {
		var f func(uintptr, *uint64, uint64, uint64) bool
		purego.RegisterLibFunc(&f, h, "agxps_aps_profile_data_get_kick_"+n)
		a.getU64[n] = f
	}
	for _, n := range []string{"id", "data_master"} {
		var f func(uintptr, *uint32, uint64, uint64) bool
		purego.RegisterLibFunc(&f, h, "agxps_aps_profile_data_get_kick_"+n)
		a.getU32[n] = f
	}
	var f16 func(uintptr, *uint16, uint64, uint64) bool
	purego.RegisterLibFunc(&f16, h, "agxps_aps_profile_data_get_kick_kick_slot")
	a.getU16["kick_slot"] = f16
	purego.RegisterLibFunc(&a.getBool, h, "agxps_aps_profile_data_get_kick_missing_end")
	return a
}

// histogram reports every distinct value and its count. It never truncates.
func histogram[T comparable](vs []T) map[T]int {
	m := make(map[T]int, len(vs))
	for _, v := range vs {
		m[v]++
	}
	return m
}

func fmtHist[T comparable](m map[T]int, order func(a, b T) bool) string {
	keys := make([]T, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return order(keys[i], keys[j]) })
	s := fmt.Sprintf("%d distinct:", len(keys))
	for _, k := range keys {
		s += fmt.Sprintf(" %v=%d", k, m[k])
	}
	return s
}

// TestKickAttributionFields dumps every kick-level field on profile_data for
// every Profiling_f_*.raw in a trace, looking for a field that partitions
// kicks by submitter. Set GPUTRACE_PROBE_DIR to a .gpuprofiler_raw directory.
func TestKickAttributionFields(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_DIR to a .gpuprofiler_raw directory")
	}
	files, err := filepath.Glob(filepath.Join(dir, "Profiling_f_*.raw"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Profiling_f_*.raw in %s: %v", dir, err)
	}
	sort.Strings(files)
	if only := os.Getenv("GPUTRACE_PROBE_LIMIT"); only != "" {
		var n int
		fmt.Sscan(only, &n)
		if n > 0 && n < len(files) {
			files = files[:n]
		}
	}

	a := loadKickAPI(t)
	a.initialize()
	gpu := a.gpuCreate(16, 6, 1, 0)
	if gpu == 0 {
		t.Fatal("gpu_create(16,6,1) returned null")
	}
	d := &rawDescriptor{
		GPU:                gpu,
		PulsePeriod:        a.pulsePeriod(gpu, 0),
		EraPeriod:          a.eraPeriod(gpu, 0),
		CountPeriod:        a.countPeriod(gpu, 0),
		ChunkSize:          0x1000,
		MaxTimestamp:       ^uint64(0),
		MaxParseErrorCount: 50,
	}
	var pinner runtime.Pinner
	pinner.Pin(d)
	defer pinner.Unpin()
	totalSW := map[uint64]int{}
	totalID := map[uint32]int{}
	totalDM := map[uint32]int{}
	totalSlot := map[uint16]int{}
	var totalKicks int

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			t.Logf("%s: empty", filepath.Base(path))
			continue
		}
		p := a.parserCreate(unsafe.Pointer(d))
		if p == 0 {
			t.Errorf("%s: parser_create returned null", filepath.Base(path))
			continue
		}
		var perr uint32
		pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &perr)
		if pd == 0 {
			t.Logf("%s: parse failed err=%d", filepath.Base(path), perr)
			a.parserDestroy(p)
			continue
		}
		nk := a.kicksNum(pd)
		if nk == 0 {
			t.Logf("%s: %d bytes, 0 kicks (expected for the off half of the 5-on/5-off pattern)", filepath.Base(path), len(data))
			a.pdDestroy(pd)
			a.parserDestroy(p)
			continue
		}
		sw := make([]uint64, nk)
		tel := make([]uint64, nk)
		st := make([]uint64, nk)
		id := make([]uint32, nk)
		dm := make([]uint32, nk)
		slot := make([]uint16, nk)
		miss := make([]bool, nk)
		okSW := a.getU64["software_id"](pd, &sw[0], 0, nk)
		okTel := a.getU64["telemetry"](pd, &tel[0], 0, nk)
		a.getU64["start"](pd, &st[0], 0, nk)
		a.getU32["id"](pd, &id[0], 0, nk)
		okDM := a.getU32["data_master"](pd, &dm[0], 0, nk)
		okSlot := a.getU16["kick_slot"](pd, &slot[0], 0, nk)
		okMiss := a.getBool(pd, &miss[0], 0, nk)

		t.Logf("%s: %d bytes, kicks=%d err=%d (ok sw=%v tel=%v dm=%v slot=%v miss=%v)",
			filepath.Base(path), len(data), nk, perr, okSW, okTel, okDM, okSlot, okMiss)
		t.Logf("  data_master: %s", fmtHist(histogram(dm), func(a, b uint32) bool { return a < b }))
		t.Logf("  kick_id:     %s", fmtHist(histogram(id), func(a, b uint32) bool { return a < b }))
		t.Logf("  kick_slot:   %s", fmtHist(histogram(slot), func(a, b uint16) bool { return a < b }))
		swh := histogram(sw)
		if len(swh) <= 64 {
			t.Logf("  software_id: %s", fmtHist(swh, func(a, b uint64) bool { return a < b }))
		} else {
			lo, hi := sw[0], sw[0]
			for _, v := range sw {
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			t.Logf("  software_id: %d distinct over %d kicks, min=%d max=%d first16=%v", len(swh), nk, lo, hi, sw[:min(16, len(sw))])
		}
		telh := histogram(tel)
		if len(telh) <= 64 {
			t.Logf("  telemetry:   %s", fmtHist(telh, func(a, b uint64) bool { return a < b }))
		} else {
			t.Logf("  telemetry:   %d distinct over %d kicks, first16=%#x", len(telh), nk, tel[:min(16, len(tel))])
		}
		// software_id is all-distinct, so any grouping must live in a
		// sub-field. Split it both ways and report every distinct value.
		hi := make([]uint32, nk)
		lo := make([]uint32, nk)
		for i, v := range sw {
			hi[i] = uint32(v >> 32)
			lo[i] = uint32(v)
		}
		hih, loh := histogram(hi), histogram(lo)
		if len(hih) <= 64 {
			t.Logf("  sw hi32:     %s", fmtHist(hih, func(a, b uint32) bool { return a < b }))
		} else {
			t.Logf("  sw hi32:     %d distinct", len(hih))
		}
		if len(loh) <= 64 {
			t.Logf("  sw lo32:     %s", fmtHist(loh, func(a, b uint32) bool { return a < b }))
		} else {
			t.Logf("  sw lo32:     %d distinct", len(loh))
		}
		// Cross-tabulate whichever side is small against data_master.
		if len(hih) <= 64 {
			cross := map[[2]uint32]int{}
			for i := range sw {
				cross[[2]uint32{hi[i], dm[i]}]++
			}
			t.Logf("  sw hi32 x data_master: %s", fmtHist(cross, func(a, b [2]uint32) bool {
				if a[0] != b[0] {
					return a[0] < b[0]
				}
				return a[1] < b[1]
			}))
		}
		if len(loh) <= 64 {
			cross := map[[2]uint32]int{}
			for i := range sw {
				cross[[2]uint32{lo[i], dm[i]}]++
			}
			t.Logf("  sw lo32 x data_master: %s", fmtHist(cross, func(a, b [2]uint32) bool {
				if a[0] != b[0] {
					return a[0] < b[0]
				}
				return a[1] < b[1]
			}))
		}

		// Does streamData -- which only the traced process wrote -- contain
		// any of these ids? If it did, "is this kick ours?" would reduce to a
		// membership test and reproduce Xcode's External Process split.
		if sd, err := os.ReadFile(filepath.Join(dir, "streamData")); err == nil {
			hits := map[uint32]int{}
			tried := map[uint32]int{}
			for i, v := range sw {
				if tried[dm[i]] >= 200 {
					continue
				}
				tried[dm[i]]++
				var b [8]byte
				for j := 0; j < 8; j++ {
					b[j] = byte(v >> (8 * j))
				}
				if bytes.Index(sd, b[:]) >= 0 {
					hits[dm[i]]++
				}
			}
			t.Logf("  streamData contains software_id (LE64), by data_master: tried=%v hits=%v", tried, hits)
		}

		nmiss := 0
		for _, m := range miss {
			if m {
				nmiss++
			}
		}
		t.Logf("  missing_end: %d of %d", nmiss, nk)

		for k, v := range swh {
			totalSW[k] += v
		}
		for k, v := range histogram(id) {
			totalID[k] += v
		}
		for k, v := range histogram(dm) {
			totalDM[k] += v
		}
		for k, v := range histogram(slot) {
			totalSlot[k] += v
		}
		totalKicks += int(nk)
		a.pdDestroy(pd)
		a.parserDestroy(p)
	}
	t.Logf("TOTAL kicks=%d", totalKicks)
	t.Logf("TOTAL kick_id:     %s", fmtHist(totalID, func(a, b uint32) bool { return a < b }))
	t.Logf("TOTAL data_master: %s", fmtHist(totalDM, func(a, b uint32) bool { return a < b }))
	t.Logf("TOTAL kick_slot:   %s", fmtHist(totalSlot, func(a, b uint16) bool { return a < b }))
	t.Logf("TOTAL software_id: %s", fmtHist(totalSW, func(a, b uint64) bool { return a < b }))
	softwareIDHigh := make(map[uint32]int)
	softwareIDLow := make(map[uint32]int)
	for softwareID, count := range totalSW {
		softwareIDHigh[uint32(softwareID>>32)] += count
		softwareIDLow[uint32(softwareID)] += count
	}
	t.Logf("TOTAL software_id high32: %s", fmtHist(softwareIDHigh, func(a, b uint32) bool { return a < b }))
	t.Logf("TOTAL software_id low32:  %s", fmtHist(softwareIDLow, func(a, b uint32) bool { return a < b }))
}
