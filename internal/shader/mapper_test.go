package shader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexTraceBundleSources(t *testing.T) {
	dir := t.TempDir()
	source := `#include <metal_stdlib>
using namespace metal;

kernel void source_backed_kernel(device float *out [[buffer(0)]],
                                 uint tid [[thread_position_in_grid]]) {
	out[tid] = 1;
}
`
	if err := os.WriteFile(filepath.Join(dir, "AABBCCDDEEFF0011"), []byte(source), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MTLBuffer-1-0"), []byte(source), 0666); err != nil {
		t.Fatal(err)
	}

	mapper := NewShaderSourceMapper()
	if err := mapper.IndexTraceBundleSources(dir); err != nil {
		t.Fatal(err)
	}
	file, line := mapper.SourceLocation("source_backed_kernel")
	if file == "" {
		t.Fatal("source_backed_kernel was not indexed")
	}
	if got := filepath.Base(file); got != "AABBCCDDEEFF0011" {
		t.Fatalf("source file = %q, want sidecar", got)
	}
	if line != 4 {
		t.Fatalf("line = %d, want 4", line)
	}
}

func TestIndexTraceBundleSourcesUsesHostName(t *testing.T) {
	dir := t.TempDir()
	source := `#include <metal_stdlib>
using namespace metal;

[[host_name("specialized_kernel_float16")]]
[[kernel]] void templated_kernel(device half *out [[buffer(0)]],
                                 uint tid [[thread_position_in_grid]]) {
	out[tid] = 1;
}
`
	if err := os.WriteFile(filepath.Join(dir, "CCDDEEFF00112233"), []byte(source), 0666); err != nil {
		t.Fatal(err)
	}

	mapper := NewShaderSourceMapper()
	if err := mapper.IndexTraceBundleSources(dir); err != nil {
		t.Fatal(err)
	}
	file, line := mapper.SourceLocation("specialized_kernel_float16")
	if file == "" {
		t.Fatal("host_name kernel was not indexed")
	}
	if got := filepath.Base(file); got != "CCDDEEFF00112233" {
		t.Fatalf("source file = %q, want sidecar", got)
	}
	if line != 5 {
		t.Fatalf("line = %d, want 5", line)
	}
}
