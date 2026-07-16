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

		name := gpu.Name()
		supported := gpu.IsSupported()
		t.Logf("  %s (gen=%d): created! name=%q valid=%v supported=%v",
			g.name, g.gen, name, gpu.IsValid(), supported)
	}
}

func TestParserWithGPU(t *testing.T) {
	err := Init()
	if err != nil {
		t.Skipf("Skipping test - GTShaderProfiler not available: %v", err)
	}
	defer Close()

	// Try agxps_initialize (returns error 1 outside Xcode/Metal context)
	if err := Initialize(); err != nil {
		t.Logf("Initialize returned error (expected outside Xcode): %v", err)
		t.Log("Note: agxps library requires Metal device context or Xcode runtime")
	}

	// Create GPU for M2 (gen=14) which we know works
	gpu, err := NewGPU(14, 0, 0)
	if err != nil {
		t.Skipf("Failed to create GPU: %v", err)
	}
	defer gpu.Destroy()
	t.Logf("Created GPU: gen=%d name=%q", gpu.Gen(), gpu.Name())

	// Note: Parser creation crashes outside Xcode/Metal context
	// The agxps_aps_descriptor_create function requires proper initialization
	// which depends on Metal device context or Xcode-specific runtime.
	//
	// Known limitations (from CE95 testing):
	// - agxps_initialize returns error 1 outside Xcode
	// - agxps_aps_descriptor_create crashes (SIGSEGV at 0x28) without init
	// - Period queries return 0 without proper context
	//
	// Recommendation: Use ObjC GTShaderProfilerStreamDataProcessor to parse
	// streamData, then use C API query functions on the resulting profile data.
	t.Log("Skipping parser creation - requires Metal/Xcode context")
	t.Log("Use GTShaderProfilerStreamDataProcessor (ObjC) for parsing streamData")
}
