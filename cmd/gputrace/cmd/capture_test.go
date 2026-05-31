//go:build darwin

package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCaptureOutputPathRejectsStdout(t *testing.T) {
	for _, path := range []string{"-", "/dev/stdout"} {
		t.Run(path, func(t *testing.T) {
			_, err := resolveCaptureOutputPath(path)
			if err == nil {
				t.Fatal("resolveCaptureOutputPath returned nil error")
			}
			if !strings.Contains(err.Error(), "not stdout") {
				t.Fatalf("error = %q, want stdout context", err)
			}
		})
	}
}

func TestResolveCaptureOutputPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		base string
	}{
		{name: "default", base: "capture.gputrace"},
		{name: "adds extension", path: "trace", base: "trace.gputrace"},
		{name: "keeps extension", path: "trace.gputrace", base: "trace.gputrace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCaptureOutputPath(tt.path)
			if err != nil {
				t.Fatalf("resolveCaptureOutputPath: %v", err)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("resolved path = %q, want absolute path", got)
			}
			if filepath.Base(got) != tt.base {
				t.Fatalf("resolved path = %q, want basename %q", got, tt.base)
			}
		})
	}
}
