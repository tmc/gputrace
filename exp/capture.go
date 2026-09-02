//go:build darwin

// Copyright © 2026 gputrace authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

package exp

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed capture_src.objc
var captureSource []byte

// Options configures a captured command execution.
type Options struct {
	// OutputFile specifies the file path for the JSON event stream.
	// Defaults to "gputrace_events.json" if empty.
	OutputFile string

	// FrameCount limits the maximum frames/present boundaries to capture.
	FrameCount uint64

	// Disabled controls whether capture interposing is inactive.
	Disabled bool
}

// BuildDylib compiles the embedded Objective-C interposer into a dynamic library at dstPath.
func BuildDylib(dstPath string) error {
	tmpDir, err := os.MkdirTemp("", "gputrace-build-*")
	if err != nil {
		return fmt.Errorf("create build temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	srcPath := filepath.Join(tmpDir, "capture.m")
	if err := os.WriteFile(srcPath, captureSource, 0644); err != nil {
		return fmt.Errorf("write capture source: %w", err)
	}

	cmd := exec.Command("clang", "-dynamiclib", "-framework", "Metal", "-framework", "Foundation", "-o", dstPath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile capture dylib: %w (output: %s)", err, string(out))
	}
	return nil
}

// Command creates an *exec.Cmd configured to run the target binary with Metal interposing injected.
// dylibPath should point to a compiled libgputrace_capture.dylib (built via BuildDylib).
func Command(dylibPath, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("DYLD_INSERT_LIBRARIES=%s", dylibPath))
	cmd.Env = append(cmd.Env, "GPUTRACE_CAPTURE_ENABLED=1")
	return cmd
}

// CommandWithOptions creates an *exec.Cmd with specific capture options.
func CommandWithOptions(dylibPath string, opts Options, name string, args ...string) *exec.Cmd {
	cmd := Command(dylibPath, name, args...)
	if opts.Disabled {
		cmd.Env = append(cmd.Env, "GPUTRACE_CAPTURE_ENABLED=0")
	}
	if opts.OutputFile != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GPUTRACE_OUTPUT_FILE=%s", opts.OutputFile))
	}
	if opts.FrameCount > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GPUTRACE_FRAME_COUNT=%d", opts.FrameCount))
	}
	return cmd
}
