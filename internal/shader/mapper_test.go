package shader

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/metallib"
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

func TestIndexSource(t *testing.T) {
	mapper := NewShaderSourceMapper()
	const source = `#include <metal_stdlib>
using namespace metal;

kernel void archived_kernel(device float *out [[buffer(0)]],
                            uint tid [[thread_position_in_grid]]) {
	out[tid] = 1;
}
`
	if err := mapper.IndexSource("capture/store0", source); err != nil {
		t.Fatal(err)
	}
	file, line := mapper.SourceLocation("archived_kernel")
	if file != "capture/store0" || line != 4 {
		t.Fatalf("SourceLocation = %q, %d, want capture/store0, 4", file, line)
	}
}

func TestIndexMetallibFromFile(t *testing.T) {
	name := os.Getenv("GPUTRACE_TEST_METALLIB")
	if name == "" {
		t.Skip("GPUTRACE_TEST_METALLIB is not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := metallib.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	mapper := NewShaderSourceMapper("does-not-exist")
	matched, unmatched, err := mapper.IndexMetallib(lib)
	if err != nil {
		t.Fatal(err)
	}
	if matched == 0 || unmatched == 0 {
		t.Fatalf("matched, unmatched = %d, %d; want both nonzero", matched, unmatched)
	}
	t.Logf("matched %d functions to embedded source; %d remain unmatched", matched, unmatched)
}

func TestIndexMetallibBindsExactArchivedSource(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString("QlpoOTFBWSZTWVZXKZcAAINfgsyQSGH1DQABAAD+r98KAACICCAAkoiaJpoNNNAAAAMQSiaKZD1Gg9TID1DQbUNJxxVQQKniAhq8rUa81eRAQUPsrXzmvWyWN9tKXWCRUQm5Dnu4QZRndIaa72ZaRTBffpnfH3YQJFIKQ0f3Na+HS5CCiqN2j0Z+wFqUQhLSUBYcxBBh1tSDryIgfxdyRThQkFZXKZc=")
	if err != nil {
		t.Fatal(err)
	}
	function := append(shaderTaggedField("NAME", append([]byte("k"), 0)), []byte("ENDT")...)
	functionTable := make([]byte, 8)
	binary.LittleEndian.PutUint32(functionTable[:4], 1)
	binary.LittleEndian.PutUint32(functionTable[4:], uint32(len(function)))
	functionTable = append(functionTable, function...)
	debugPayload := make([]byte, 4)
	binary.LittleEndian.PutUint32(debugPayload, 3)
	debugPayload = append(debugPayload, []byte("kernels/test.metal\x00")...)
	debug := shaderTaggedField("DEBI", debugPayload)
	debug = append(debug, shaderTaggedField("DEPF", []byte("test.air\x00"))...)
	private := make([]byte, 4)
	binary.LittleEndian.PutUint32(private, uint32(4+len(debug)+4))
	private = append(private, debug...)
	private = append(private, []byte("ENDT")...)
	data := append(append(append([]byte{}, functionTable...), private...), compressed...)
	lib := &metallib.File{
		Data: data,
		Header: metallib.Header{
			FunctionTable:       0,
			PrivateMetadata:     uint64(len(functionTable)),
			PrivateMetadataSize: uint64(len(private)),
			Sources:             uint64(len(functionTable) + len(private)),
			SourcesSize:         uint64(len(compressed)),
		},
	}
	mapper := NewShaderSourceMapper("does-not-exist")
	matched, unmatched, err := mapper.IndexMetallib(lib)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 || unmatched != 0 {
		t.Fatalf("matched, unmatched = %d, %d, want 1, 0", matched, unmatched)
	}
	file, line := mapper.SourceLocation("k")
	if file != "kernels/test.metal" || line != 3 {
		t.Fatalf("SourceLocation = %q, %d", file, line)
	}
	if source, ok := mapper.SourceText(file); !ok || source == "" {
		t.Fatal("archived source text not retained")
	}
}

func TestIndexMetallibWithoutSourceArchiveIsUnmatched(t *testing.T) {
	function := append(shaderTaggedField("NAME", append([]byte("k"), 0)), []byte("ENDT")...)
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[:4], 1)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(function)))
	data = append(data, function...)
	mapper := NewShaderSourceMapper("does-not-exist")
	matched, unmatched, err := mapper.IndexMetallib(&metallib.File{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 || unmatched != 1 {
		t.Fatalf("matched, unmatched = %d, %d, want 0, 1", matched, unmatched)
	}
}

func shaderTaggedField(tag string, payload []byte) []byte {
	field := make([]byte, 6+len(payload))
	copy(field, tag)
	binary.LittleEndian.PutUint16(field[4:6], uint16(len(payload)))
	copy(field[6:], payload)
	return field
}
