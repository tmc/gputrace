package metallib

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"testing"
)

const testSourceArchive = "QlpoOTFBWSZTWVZXKZcAAINfgsyQSGH1DQABAAD+r98KAACICCAAkoiaJpoNNNAAAAMQSiaKZD1Gg9TID1DQbUNJxxVQQKniAhq8rUa81eRAQUPsrXzmvWyWN9tKXWCRUQm5Dnu4QZRndIaa72ZaRTBffpnfH3YQJFIKQ0f3Na+HS5CCiqN2j0Z+wFqUQhLSUBYcxBBh1tSDryIgfxdyRThQkFZXKZc="

func TestEmbeddedSources(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString(testSourceArchive)
	if err != nil {
		t.Fatal(err)
	}
	const off = 64
	data := make([]byte, off+len(compressed))
	copy(data[8:], "HSRD")
	data[12] = 16
	putUint64(data[14:22], off)
	putUint64(data[22:30], uint64(len(compressed)))
	copy(data[off:], compressed)

	files, err := (&File{Data: data}).EmbeddedSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "kernels/test.metal" {
		t.Fatalf("files = %#v", files)
	}
	want := "#include <metal_stdlib>\nusing namespace metal;\nkernel void k() {}\n"
	if string(files[0].Data) != want {
		t.Fatalf("source = %q, want %q", files[0].Data, want)
	}
}

func TestEmbeddedSourcesRefusesInvalidRange(t *testing.T) {
	data := make([]byte, 32)
	copy(data, "HSRD")
	data[4] = 16
	putUint64(data[6:14], uint64(len(data)))
	putUint64(data[14:22], 1)
	_, err := (&File{Data: data}).EmbeddedSources()
	if !errors.Is(err, ErrNoSourceArchive) {
		t.Fatalf("error = %v, want ErrNoSourceArchive", err)
	}
}

func TestEmbeddedSourcesUsesDeclaredSection(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString(testSourceArchive)
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte("HSRD\x10\x00not a valid range"), compressed...)
	off := len(data) - len(compressed)
	files, err := (&File{
		Data: data,
		Header: Header{
			Sources:     uint64(off),
			SourcesSize: uint64(len(compressed)),
		},
	}).EmbeddedSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "kernels/test.metal" {
		t.Fatalf("files = %#v", files)
	}
}

func TestReadSourceArchiveRefusesDuplicateNames(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for range 2 {
		if err := tw.WriteHeader(&tar.Header{Name: "same.metal", Mode: 0644, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceArchive(&buf); err == nil {
		t.Fatal("readSourceArchive accepted duplicate names")
	}
}

func TestEmbeddedSourcesFromFile(t *testing.T) {
	name := os.Getenv("GPUTRACE_TEST_METALLIB")
	if name == "" {
		t.Skip("GPUTRACE_TEST_METALLIB is not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	files, err := lib.EmbeddedSources()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if bytes.Contains(file.Data, []byte("#include <metal_stdlib>")) {
			t.Logf("decoded %d files; Metal source %q", len(files), file.Name)
			return
		}
	}
	t.Fatalf("decoded %d files without Metal source", len(files))
}

func TestDebugRecordsFromFile(t *testing.T) {
	name := os.Getenv("GPUTRACE_TEST_METALLIB")
	if name == "" {
		t.Skip("GPUTRACE_TEST_METALLIB is not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	functions, err := lib.ListFunctionMetadata()
	if err != nil {
		t.Fatal(err)
	}
	records := lib.ListDebugRecords()
	if len(records) == 0 {
		t.Fatal("no debug records")
	}
	t.Logf("decoded %d functions and %d unbound debug records", len(functions), len(records))
	pairs, err := lib.ListFunctionDebug()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != len(functions) {
		t.Fatalf("decoded %d function/debug pairs, want %d", len(pairs), len(functions))
	}
	t.Logf("decoded %d structurally bound function/debug pairs", len(pairs))
}

func putUint64(p []byte, v uint64) {
	for i := range 8 {
		p[i] = byte(v >> (8 * i))
	}
}
