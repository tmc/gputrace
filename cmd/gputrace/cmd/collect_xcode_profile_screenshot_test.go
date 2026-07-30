//go:build darwin

package cmd

import (
	"encoding/json"
	"os"
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
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "tmp", "xcode-screenshot-20260531-010203.png"); got != want {
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

func TestVerifyScreenshotFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.png")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyScreenshotFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyScreenshotFile(path); err == nil {
		t.Fatal("non-PNG screenshot returned nil error")
	}
}

func TestTriggerScreenRecordingTCCJSONOutput(t *testing.T) {
	oldJSON := collectProfileOpts.json
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
	})
	collectProfileOpts.json = true

	out, err := captureStdout(t, triggerScreenRecordingTCC)
	if err != nil {
		t.Fatalf("triggerScreenRecordingTCC: %v", err)
	}

	var got xcodeProfileActionOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !got.Success || got.Action != "screenshot-no-prompt" {
		t.Fatalf("decoded output = %+v", got)
	}
}
