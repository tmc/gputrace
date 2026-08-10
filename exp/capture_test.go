// Copyright © 2026 gputrace authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

package exp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDylib(t *testing.T) {
	tmpDir := t.TempDir()
	dylibPath := filepath.Join(tmpDir, "libgputrace_capture.dylib")

	if err := BuildDylib(dylibPath); err != nil {
		t.Fatalf("BuildDylib() unexpected error = %v", err)
	}

	info, err := os.Stat(dylibPath)
	if err != nil {
		t.Fatalf("os.Stat(dylibPath) error = %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("BuildDylib() produced empty file")
	}
}

func TestCommandWithOptions(t *testing.T) {
	tests := []struct {
		name       string
		dylibPath  string
		opts       Options
		targetCmd  string
		args       []string
		wantEnvKey string
	}{
		{
			name:       "default options",
			dylibPath:  "/tmp/test.dylib",
			opts:       Options{},
			targetCmd:  "python3",
			args:       []string{"-c", "pass"},
			wantEnvKey: "DYLD_INSERT_LIBRARIES=/tmp/test.dylib",
		},
		{
			name:      "custom output file",
			dylibPath: "/tmp/test.dylib",
			opts: Options{
				OutputFile: "events.json",
			},
			targetCmd:  "echo",
			args:       []string{"test"},
			wantEnvKey: "GPUTRACE_OUTPUT_FILE=events.json",
		},
		{
			name:      "frame count limit",
			dylibPath: "/tmp/test.dylib",
			opts: Options{
				FrameCount: 5,
			},
			targetCmd:  "echo",
			args:       []string{"test"},
			wantEnvKey: "GPUTRACE_FRAME_COUNT=5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := CommandWithOptions(tt.dylibPath, tt.opts, tt.targetCmd, tt.args...)
			found := false
			for _, env := range cmd.Env {
				if strings.Contains(env, tt.wantEnvKey) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("CommandWithOptions() env missing %q, got: %v", tt.wantEnvKey, cmd.Env)
			}
		})
	}
}
