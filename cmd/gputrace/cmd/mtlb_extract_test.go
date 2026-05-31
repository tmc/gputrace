package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMTLBExtractStatusWriterUsesStderrForStdoutOutput(t *testing.T) {
	tests := []struct {
		name string
		path string
		want *os.File
	}{
		{name: "file", path: "kernels.metallib", want: os.Stdout},
		{name: "stdout", path: "/dev/stdout", want: os.Stderr},
		{name: "dash", path: "-", want: os.Stderr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mtlbExtractStatusWriter(tt.path); got != tt.want {
				t.Fatalf("mtlbExtractStatusWriter(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCopyFileStdoutWritesOnlyFileBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "library.metallib")
	data := []byte("MTLB\x00\x01\npayload")
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return copyFile(src, "/dev/stdout")
	})
	if err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if got := []byte(out); string(got) != string(data) {
		t.Fatalf("stdout = %q, want %q", got, data)
	}
}

func TestCopyFileDashWritesOnlyFileBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "library.metallib")
	data := []byte("MTLB\x00\x01\npayload")
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return copyFile(src, "-")
	})
	if err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if got := []byte(out); string(got) != string(data) {
		t.Fatalf("stdout = %q, want %q", got, data)
	}
}
