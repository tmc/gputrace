package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPprofCmd(t *testing.T) {
	// Setup temporary directory for output
	tmpDir, err := os.MkdirTemp("", "gputrace-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Path to a valid trace file in the repo
	tracePath := "../../../testdata/traces/mlx-lm-generate_tokens_8_to_9.gputrace"

	// Check if trace exists
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s", tracePath)
	}

	tests := []struct {
		name string
		args []string
		check func(t *testing.T, outputDir string)
	}{
		{
			name: "default",
			args: []string{"pprof", tracePath, "-o", filepath.Join(tmpDir, "output.pprof")},
			check: func(t *testing.T, outputDir string) {
				if _, err := os.Stat(filepath.Join(outputDir, "output.pprof")); os.IsNotExist(err) {
					t.Error("expected output.pprof to exist")
				}
			},
		},
		{
			name: "all",
			args: []string{"pprof", tracePath, "--all", "--prefix", filepath.Join(tmpDir, "all_output")},
			check: func(t *testing.T, outputDir string) {
				files := []string{
					"all_output.gpu.pprof",
					"all_output.combined.pprof",
					"all_output.txt",
				}
				for _, f := range files {
					if _, err := os.Stat(filepath.Join(outputDir, f)); os.IsNotExist(err) {
						t.Errorf("expected %s to exist", f)
					}
				}
			},
		},
		{
			name: "text",
			args: []string{"pprof", tracePath, "--text", "-o", filepath.Join(tmpDir, "output.txt")},
			check: func(t *testing.T, outputDir string) {
				if _, err := os.Stat(filepath.Join(outputDir, "output.txt")); os.IsNotExist(err) {
					t.Error("expected output.txt to exist")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset flags
			output = ""
			prefix = ""
			all = false
			verbose = false
			textReport = false
			showStats = false

			// Create command
			cmd := rootCmd
			cmd.SetArgs(tt.args)

			// Run command
			if err := cmd.Execute(); err != nil {
				t.Fatalf("command failed: %v", err)
			}

			// Check results
			tt.check(t, tmpDir)
		})
	}
}
