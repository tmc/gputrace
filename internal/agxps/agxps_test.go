//go:build darwin

package agxps

import (
	"testing"
)

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

func TestESLCliqueFunctionsAvailable(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	if _, err := ESLCliqueTimings(0); err == nil {
		t.Fatal("ESLCliqueTimings(0) succeeded, want invalid profile data error")
	}

	if trace := ESLCliqueInstructionTrace(0, 0); trace != 0 {
		t.Fatalf("ESLCliqueInstructionTrace(0, 0) = %#x, want 0", trace)
	}

	stats := TraceInstructionStats(0)
	if stats != (InstructionTraceStats{}) {
		t.Fatalf("TraceInstructionStats(0) = %+v, want zero stats", stats)
	}
}

func TestParserFunctionsAvailable(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	p := &Parser{}
	if p.IsValid() {
		t.Fatal("zero Parser reported valid")
	}

	if _, err := p.Parse(nil); err == nil {
		t.Fatal("Parse(nil) succeeded, want empty data error")
	}
}

func TestGPUCreation(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	// Test GPU creation for various generations
	gpuGens := []struct {
		name    string
		gen     uint32
		variant uint32
		rev     uint32
	}{
		{"M1", 13, 0, 0},
		{"M2", 14, 0, 0},
		{"M3", 15, 0, 0},
		{"A17", 16, 0, 0},
	}

	t.Log("Testing GPU creation...")
	for _, g := range gpuGens {
		gpu, err := NewGPU(g.gen, g.variant, g.rev)
		if err != nil {
			t.Logf("  %s (gen=%d): failed - %v", g.name, g.gen, err)
			continue
		}
		defer gpu.Destroy()

		// A handle here is not a working GPU. gpu_create returns one that
		// reports valid=true for an unsupported triple, with no backing GPU
		// description, and every parser_create against it returns NULL. Say
		// "handle" rather than "created!", which read as a success.
		t.Logf("  %s (gen=%d): handle name=%q valid=%v (validity does not imply usable)",
			g.name, g.gen, gpu.Name(), gpu.IsValid())
	}
}

func TestParserWithGPU(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	// agxps_initialize returns 1 for SUCCESS. This used to read the 1 as an
	// errno and explain it away as "expected outside Xcode", which is how a
	// working call spent months looking like a broken one.
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Create GPU for M2 (gen=14) which we know works
	gpu, err := NewGPU(14, 0, 0)
	if err != nil {
		t.Skipf("Failed to create GPU: %v", err)
	}
	defer gpu.Destroy()
	t.Logf("Created GPU: gen=%d name=%q", gpu.Gen(), gpu.Name())

	// Parser creation is skipped here, but not for the reason this test used to
	// give. The old comment blamed a missing Metal device context for three
	// symptoms that all have concrete causes, recorded in
	// docs/research/agxps-signatures.yaml:
	//
	//   - "agxps_initialize returns error 1 outside Xcode" -- 1 is success.
	//   - "descriptor_create crashes (SIGSEGV at 0x28)" -- it returns a
	//     104-byte struct by value through x8, which purego cannot set, so the
	//     first store (stur q0, [x8, #0x28]) faults at 0x28. It also takes no
	//     arguments, and this package passes it one. A caller can skip it
	//     entirely: it only installs defaults.
	//   - "period queries return 0" -- parser_create returns NULL for a
	//     descriptor with zero pulse/era/count periods, which is exactly what
	//     descriptor_create leaves. Real periods come from
	//     agxps_aps_get_valid_*_period.
	//
	// A working parse of a 58 MB Profiling_f_*.raw runs in
	// rawprobe_manual_test.go with no Xcode process involved, which is what
	// disproves the Metal-context story.
	t.Log("parser creation exercised in rawprobe_manual_test.go, not here")
}
