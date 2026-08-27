package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCaptureCheckResolvesThroughPath pins the resolution rule: --check must
// name the binary a capture would actually launch. Passing the bare argv[0] to
// codesign silently checks a same-named file in the working directory instead,
// so the verdict tracked the caller's cwd rather than PATH.
func TestCaptureCheckResolvesThroughPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign-based capture check is darwin-only")
	}
	want, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on PATH")
	}

	// A decoy of the same name in the working directory must not be consulted.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out bytes.Buffer
	cmd := newCaptureCommand(&captureOptions{})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--check", "python3"})
	// The verdict itself depends on the host's python3 and is not asserted;
	// only that the command names the PATH-resolved binary and does not fail
	// with a codesign lookup error against the decoy.
	err = cmd.Execute()
	got := out.String()
	if err != nil && !strings.Contains(got, "not capturable") {
		t.Fatalf("--check python3 = %v, output %q; want a verdict", err, got)
	}
	if !strings.Contains(got, want) {
		t.Errorf("--check python3 output %q does not name the PATH-resolved %q", got, want)
	}
}
