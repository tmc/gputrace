package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunCaptureWithDepsValidatesAndMergesArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	cpuPath := filepath.Join(tmpDir, "custom.pprof")
	gpuPath := filepath.Join(tmpDir, "trace.gputrace")
	outputPath := filepath.Join(tmpDir, "merged.pprof")

	cfg := captureConfig{
		args:       []string{"app", "arg"},
		cpuProfile: cpuPath,
		gpuTrace:   gpuPath,
		output:     outputPath,
	}

	var gotArgs []string
	var gotEnv []string
	run := func(args []string, env []string) error {
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)

		if err := os.WriteFile(cpuPath, []byte("profile"), 0600); err != nil {
			return err
		}
		return os.Mkdir(gpuPath, 0755)
	}

	var merged bool
	merge := func(cpu, gpu, output string) error {
		merged = true
		if cpu != cpuPath {
			t.Fatalf("cpu path = %q, want %q", cpu, cpuPath)
		}
		if gpu != gpuPath {
			t.Fatalf("gpu path = %q, want %q", gpu, gpuPath)
		}
		if output != outputPath {
			t.Fatalf("output path = %q, want %q", output, outputPath)
		}
		return nil
	}

	if err := runCaptureWithDeps(cfg, captureDeps{
		run:      run,
		validate: validateCaptureOutputs,
		merge:    merge,
	}); err != nil {
		t.Fatalf("runCaptureWithDeps returned error: %v", err)
	}

	if !reflect.DeepEqual(gotArgs, cfg.args) {
		t.Fatalf("args = %v, want %v", gotArgs, cfg.args)
	}
	if !containsEnv(gotEnv, "MTL_CAPTURE_ENABLED=1") {
		t.Fatalf("env did not enable Metal capture: %v", gotEnv)
	}
	if !containsEnv(gotEnv, "GPUPROFILER_TRACE_DESTINATION="+gpuPath) {
		t.Fatalf("env did not set GPU trace destination %q: %v", gpuPath, gotEnv)
	}
	if !merged {
		t.Fatal("merge was not called")
	}
}

func TestRunCaptureWithDepsFailsBeforeMergeWhenCPUProfileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cpuPath := filepath.Join(tmpDir, "missing.pprof")
	gpuPath := filepath.Join(tmpDir, "trace.gputrace")

	run := func(_ []string, _ []string) error {
		return os.Mkdir(gpuPath, 0755)
	}
	merge := func(_, _, _ string) error {
		t.Fatal("merge should not be called")
		return nil
	}

	err := runCaptureWithDeps(captureConfig{
		args:       []string{"app"},
		cpuProfile: cpuPath,
		gpuTrace:   gpuPath,
		output:     filepath.Join(tmpDir, "merged.pprof"),
	}, captureDeps{
		run:      run,
		validate: validateCaptureOutputs,
		merge:    merge,
	})
	if err == nil {
		t.Fatal("expected missing CPU profile error")
	}
	if !strings.Contains(err.Error(), "cpu profile") || !strings.Contains(err.Error(), "was not produced") {
		t.Fatalf("error = %q, want missing CPU profile context", err)
	}
}

func TestRunCaptureWithDepsWrapsCommandFailure(t *testing.T) {
	runErr := errors.New("boom")
	var validated bool
	var merged bool

	err := runCaptureWithDeps(captureConfig{
		args:       []string{"app"},
		cpuProfile: "cpu.pprof",
		gpuTrace:   "trace.gputrace",
		output:     "merged.pprof",
	}, captureDeps{
		run: func(_ []string, _ []string) error {
			return runErr
		},
		validate: func(_, _ string) error {
			validated = true
			return nil
		},
		merge: func(_, _, _ string) error {
			merged = true
			return nil
		},
	})

	if !errors.Is(err, runErr) {
		t.Fatalf("error = %v, want wrapped run error %v", err, runErr)
	}
	if validated {
		t.Fatal("validate should not be called after command failure")
	}
	if merged {
		t.Fatal("merge should not be called after command failure")
	}
}

func TestProfileOutputPathIsStdout(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: true},
		{name: "dash", path: "-", want: true},
		{name: "dev stdout", path: "/dev/stdout", want: true},
		{name: "file", path: "merged.pprof", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := profileOutputPathIsStdout(tt.path); got != tt.want {
				t.Fatalf("profileOutputPathIsStdout(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func containsEnv(env []string, want string) bool {
	for _, got := range env {
		if got == want {
			return true
		}
	}
	return false
}
