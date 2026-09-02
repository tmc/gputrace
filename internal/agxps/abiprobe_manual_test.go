//go:build darwin

package agxps

import (
	"os"
	"testing"

	"github.com/ebitengine/purego"
)

// Exploratory ABI probe. Manual: it dlopens Xcode's GTShaderProfiler and calls
// into it, so it is skipped unless GPUTRACE_ABI_PROBE is set.

func probeHandle(t *testing.T) uintptr {
	t.Helper()
	if os.Getenv("GPUTRACE_ABI_PROBE") == "" {
		t.Skip("set GPUTRACE_ABI_PROBE=1 to run the exploratory ABI probes")
	}
	h, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skipf("dlopen GTShaderProfiler: %v", err)
	}
	return h
}

func TestProbeDlsymExportTrie(t *testing.T) {
	h := probeHandle(t)
	names := []string{
		"agxps_initialize",
		"agxps_gpu_create",
		"agxps_gpu_get_rev",
		"agxps_gpu_get_rev_with_aps_fallback",
		"agxps_aps_gpu_find_supported_revision",
		"agxps_aps_gpu_is_supported",
		"agxps_aps_descriptor_create",
		"agxps_aps_parser_create",
		"agxps_aps_parser_parse",
		"agxps_aps_timing_analyzer_create",
		"agxps_aps_timing_analyzer_get_num_commands",
		"agxps_aps_timing_analyzer_get_work_cliques_average_duration",
		"agxps_aps_clique_time_stats_create",
		"agxps_aps_clique_instruction_trace_get_execution_events_num",
	}
	for _, n := range names {
		sym, err := purego.Dlsym(h, n)
		if err != nil || sym == 0 {
			t.Errorf("dlsym %s: sym=%#x err=%v", n, sym, err)
			continue
		}
		t.Logf("dlsym %-60s = %#x", n, sym)
	}
}

func TestProbeInitializeArity(t *testing.T) {
	h := probeHandle(t)
	sym, err := purego.Dlsym(h, "agxps_initialize")
	if err != nil {
		t.Fatal(err)
	}
	var zeroArg func() int32
	var fourArg func(uintptr, uint64, uintptr, uint64) int32
	purego.RegisterFunc(&zeroArg, sym)
	purego.RegisterFunc(&fourArg, sym)
	t.Logf("agxps_initialize() [0-arg decl]        = %d", zeroArg())
	t.Logf("agxps_initialize(0,0,0,0) [4-arg decl] = %d", fourArg(0, 0, 0, 0))
}

func TestProbeGPUCreateExactFlag(t *testing.T) {
	h := probeHandle(t)
	sym := func(n string) uintptr {
		s, err := purego.Dlsym(h, n)
		if err != nil {
			t.Fatalf("dlsym %s: %v", n, err)
		}
		return s
	}
	var initialize func(uintptr, uint64, uintptr, uint64) int32
	var create4 func(gen, variant, rev, exact uint32) uintptr
	var create3 func(gen, variant, rev uint32) uintptr
	var destroy func(uintptr)
	var getRev func(uintptr) uint32
	var getRevFallback func(uintptr) uint32
	var isSupported3 func(gen, variant, rev uint32) bool
	purego.RegisterFunc(&initialize, sym("agxps_initialize"))
	purego.RegisterFunc(&create4, sym("agxps_gpu_create"))
	purego.RegisterFunc(&create3, sym("agxps_gpu_create"))
	purego.RegisterFunc(&destroy, sym("agxps_gpu_destroy"))
	purego.RegisterFunc(&getRev, sym("agxps_gpu_get_rev"))
	purego.RegisterFunc(&getRevFallback, sym("agxps_gpu_get_rev_with_aps_fallback"))
	purego.RegisterFunc(&isSupported3, sym("agxps_aps_gpu_is_supported"))
	initialize(0, 0, 0, 0)

	// Find a triple where the requested revision is not itself supported but
	// the gen/variant pair is, so the exact flag decides the effective rev.
	type row struct{ gen, variant, rev, exact0, exact1 uint32 }
	var rows []row
	for gen := uint32(0); gen < 42 && len(rows) < 8; gen++ {
		for variant := uint32(0); variant < 6 && len(rows) < 8; variant++ {
			for rev := uint32(0); rev < 6 && len(rows) < 8; rev++ {
				g0 := create4(gen, variant, rev, 0)
				if g0 == 0 {
					continue
				}
				g1 := create4(gen, variant, rev, 1)
				r0, r1 := getRevFallback(g0), getRevFallback(g1)
				base := getRev(g0)
				if r0 != r1 {
					rows = append(rows, row{gen, variant, rev, r0, r1})
					t.Logf("gen=%d variant=%d rev=%d: get_rev=%d fallback(exact=0)=%d fallback(exact=1)=%d supported=%v",
						gen, variant, rev, base, r0, r1, isSupported3(gen, variant, rev))
				}
				destroy(g0)
				if g1 != 0 {
					destroy(g1)
				}
			}
		}
	}
	if len(rows) == 0 {
		t.Log("no triple found where the exact flag changes the effective revision")
	}

	// Does the 3-arg declaration leave x3 nondeterministic?
	r := rows
	if len(r) > 0 {
		v := r[0]
		for i := 0; i < 5; i++ {
			g := create3(v.gen, v.variant, v.rev)
			t.Logf("3-arg create(gen=%d,variant=%d,rev=%d) call %d: fallback rev=%d (exact=0 gives %d, exact=1 gives %d)",
				v.gen, v.variant, v.rev, i, getRevFallback(g), v.exact0, v.exact1)
			destroy(g)
		}
	}
}

func TestProbeGPUFormatNameIsConstant(t *testing.T) {
	h := probeHandle(t)
	sym := func(n string) uintptr {
		s, err := purego.Dlsym(h, n)
		if err != nil {
			t.Fatalf("dlsym %s: %v", n, err)
		}
		return s
	}
	var initialize func(uintptr, uint64, uintptr, uint64) int32
	var create func(gen, variant, rev, exact uint32) uintptr
	var destroy func(uintptr)
	var formatName func(uintptr, *byte, uint64) int32
	var isSupported func(gen, variant, rev uint32) bool
	purego.RegisterFunc(&initialize, sym("agxps_initialize"))
	purego.RegisterFunc(&create, sym("agxps_gpu_create"))
	purego.RegisterFunc(&destroy, sym("agxps_gpu_destroy"))
	purego.RegisterFunc(&formatName, sym("agxps_gpu_format_name"))
	purego.RegisterFunc(&isSupported, sym("agxps_aps_gpu_is_supported"))
	initialize(0, 0, 0, 0)

	name := func(g uintptr) string {
		buf := make([]byte, 64)
		formatName(g, &buf[0], uint64(len(buf)))
		for i, b := range buf {
			if b == 0 {
				return string(buf[:i])
			}
		}
		return string(buf)
	}
	t.Logf("format_name(NULL) = %q", name(0))
	seen := map[string]int{}
	for gen := uint32(0); gen < 42; gen++ {
		for variant := uint32(0); variant < 6; variant++ {
			for rev := uint32(0); rev < 6; rev++ {
				g := create(gen, variant, rev, 0)
				if g == 0 {
					continue
				}
				n := name(g)
				if seen[n] == 0 {
					t.Logf("first handle producing %q: gen=%d variant=%d rev=%d supported=%v",
						n, gen, variant, rev, isSupported(gen, variant, rev))
				}
				seen[n]++
				destroy(g)
			}
		}
	}
	t.Logf("distinct names across all creatable handles: %d %v", len(seen), seen)

	supported := 0
	for gen := uint32(0); gen < 64; gen++ {
		for variant := uint32(0); variant < 16; variant++ {
			for rev := uint32(0); rev < 16; rev++ {
				if isSupported(gen, variant, rev) {
					supported++
				}
			}
		}
	}
	t.Logf("supported triples in gen<64 variant<16 rev<16: %d", supported)
}
