package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveScreenshotOutputPathRejectsStdout(t *testing.T) {
	for _, path := range []string{"-", "/dev/stdout"} {
		t.Run(path, func(t *testing.T) {
			_, err := resolveScreenshotOutputPath(path, time.Time{})
			if err == nil {
				t.Fatal("resolveScreenshotOutputPath returned nil error")
			}
			if !strings.Contains(err.Error(), "not stdout") {
				t.Fatalf("error = %q, want stdout context", err)
			}
		})
	}
}

func TestResolveScreenshotOutputPath(t *testing.T) {
	when := time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC)

	got, err := resolveScreenshotOutputPath("", when)
	if err != nil {
		t.Fatalf("default output path: %v", err)
	}
	if want := "/tmp/xcode-screenshot-20260531-010203.png"; got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}

	got, err = resolveScreenshotOutputPath("trace.png", when)
	if err != nil {
		t.Fatalf("relative output path: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("relative path resolved to %q, want absolute path", got)
	}
	if filepath.Base(got) != "trace.png" {
		t.Fatalf("resolved path = %q, want basename trace.png", got)
	}
}
