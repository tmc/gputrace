//go:build darwin

package agxps

import (
	"testing"

	"github.com/ebitengine/purego"
)

// TestSymbolsResolveByDlsym refutes, for this binary, the claim that the
// agxps_* symbols are absent from GTShaderProfiler's export trie and must be
// reached through a UUID-pinned image offset.
//
// It matters which is true. An offset resolver that does not verify the binary
// UUID keeps working after an OS update by calling whatever now lives at that
// address, and returns plausible numbers from the wrong function. dlsym cannot
// do that: it either finds the name or fails. This asserts the names resolve,
// so the offset question stays closed and nobody reintroduces an offset table.
func TestSymbolsResolveByDlsym(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	handle, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	for _, name := range []string{
		"agxps_initialize",
		"agxps_gpu_create",
		"agxps_gpu_get_rev",
		"agxps_gpu_get_rev_with_aps_fallback",
		"agxps_aps_gpu_is_supported",
		"agxps_aps_parser_create",
		"agxps_aps_parser_parse",
		"agxps_aps_profile_data_get_counter_names",
		"agxps_aps_profile_data_get_kick_id",
	} {
		sym, err := purego.Dlsym(handle, name)
		if err != nil || sym == 0 {
			t.Errorf("dlsym %s: sym=%#x err=%v", name, sym, err)
		}
	}
	// loadCounterShapeAPI resolves every symbol this package calls and returns
	// an error naming the first that is missing.
	if _, err := loadCounterShapeAPI(); err != nil {
		t.Fatalf("loadCounterShapeAPI: %v", err)
	}
}

func TestInit(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	if !IsLoaded() {
		t.Fatal("Library not loaded after Init")
	}
}

func TestESLCliqueReferencesRejectsZeroHandle(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	if _, err := eslCliqueReferences(0); err == nil {
		t.Fatal("eslCliqueReferences(0) succeeded, want invalid profile data error")
	}
	if _, err := kickReferences(0); err == nil {
		t.Fatal("kickReferences(0) succeeded, want invalid profile data error")
	}
}

func TestParserFunctionsAvailable(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	p := &parser{}
	if p.IsValid() {
		t.Fatal("zero parser reported valid")
	}

	if _, err := p.Parse(nil); err == nil {
		t.Fatal("Parse(nil) succeeded, want empty data error")
	}
}

// TestGPUSupportedTripleCount pins agxps_aps_gpu_is_supported to the three
// uint32 scalars it actually takes.
//
// The count is the check. A brute-force scan of the triple space finds exactly
// 53 supported triples, matching the length of the static initializer array
// that populates the set (docs/research/agxps-signatures.yaml,
// agxps_aps_gpu_is_supported, comparator at 0x4eeee0). The single-handle
// declaration the generated binding uses puts a pointer in the generation
// register and leaves the variant and revision registers unset, which makes the
// lookup miss for every input and yields 0. Any other wrong shape moves the
// count off 53 as well, so this fails loudly where a spot check on one triple
// would not: "supported" answers are individually plausible either way.
func TestGPUSupportedTripleCount(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		t.Fatalf("loadCounterShapeAPI: %v", err)
	}
	// The comparator table is indexed gen*42+variant*6+rev, so gen<64,
	// variant<16, rev<16 covers it with room to spare.
	got := 0
	for gen := uint32(0); gen < 64; gen++ {
		for variant := uint32(0); variant < 16; variant++ {
			for rev := uint32(0); rev < 16; rev++ {
				if a.gpuIsSupported(gen, variant, rev) {
					got++
				}
			}
		}
	}
	if want := 53; got != want {
		t.Fatalf("supported triples = %d, want %d; agxps_aps_gpu_is_supported is being called with the wrong argument shape", got, want)
	}
}

// TestGPUCreateExactFlagIsLoadBearing pins the fourth argument of
// agxps_gpu_create.
//
// The generated binding declares three arguments, which leaves x3 holding
// whatever the trampoline last did. x3 is a bool tested with `tbnz w23, #0x0`
// at 0x49b5a8: when set, the revision fallback at 0x49b5b8 is skipped and the
// effective revision at +0xc keeps the requested value. This asserts that the
// flag changes the result for at least one triple, so a future regeneration
// that drops the parameter again cannot pass. It would fail if the flag were
// inert, which is exactly the claim being made.
func TestGPUCreateExactFlagIsLoadBearing(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		t.Fatalf("loadCounterShapeAPI: %v", err)
	}
	differed := 0
	for gen := uint32(0); gen < 42; gen++ {
		for variant := uint32(0); variant < 6; variant++ {
			for rev := uint32(0); rev < 6; rev++ {
				lenient := a.gpuCreate(gen, variant, rev, 0)
				if lenient == 0 {
					continue
				}
				strict := a.gpuCreate(gen, variant, rev, 1)
				if strict != 0 && a.gpuEffectiveRev(lenient) != a.gpuEffectiveRev(strict) {
					differed++
				}
				a.gpuDestroy(lenient)
				if strict != 0 {
					a.gpuDestroy(strict)
				}
			}
		}
	}
	if differed == 0 {
		t.Fatal("agxps_gpu_create produced the same effective revision for exact=0 and exact=1 on every triple; the fourth argument is not reaching the callee")
	}
	t.Logf("exact flag changed the effective revision for %d triples", differed)
}

// TestGPUNameIsNotADeviceName records that GPU.Name identifies nothing.
//
// agxps_gpu_format_name picks between two string literals on a NULL test and
// passes no part of the triple to the formatter, so every non-nil handle
// formats to the same constant. The test exists so that a reader who sees
// name="AppleGPU" logged next to a gen/variant/rev does not read it as the
// framework confirming the triple.
func TestGPUNameIsNotADeviceName(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	triples := [][3]uint32{{13, 0, 0}, {14, 0, 0}, {15, 0, 0}, {16, 0, 0}}
	names := map[string][][3]uint32{}
	for _, tr := range triples {
		gpu, err := NewGPU(tr[0], tr[1], tr[2], false)
		if err != nil {
			t.Logf("gen=%d variant=%d rev=%d: %v", tr[0], tr[1], tr[2], err)
			continue
		}
		names[gpu.Name()] = append(names[gpu.Name()], tr)
		t.Logf("gen=%d variant=%d rev=%d: name=%q supported=%v", tr[0], tr[1], tr[2], gpu.Name(), gpu.IsSupported())
		gpu.Destroy()
	}
	if len(names) == 0 {
		t.Skip("no GPU handles could be created")
	}
	if len(names) != 1 {
		t.Fatalf("agxps_gpu_format_name produced %d distinct names %v; it was established to produce exactly one", len(names), names)
	}
	for name := range names {
		if name != "AppleGPU" {
			t.Fatalf("agxps_gpu_format_name = %q, want the constant %q", name, "AppleGPU")
		}
	}
}
