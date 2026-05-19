package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

func TestPprofCmd(t *testing.T) {
	// Setup temporary directory for output
	tmpDir, err := os.MkdirTemp("", "gputrace-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Path to a small canonical trace fixture in the repo.
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"

	// Check if trace exists
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. Run 'make fetch-testdata' to fetch test assets.", tracePath)
	}

	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, outputDir string)
	}{
		{
			name: "default",
			args: []string{"pprof", tracePath, "-o", filepath.Join(tmpDir, "output.pprof")},
			check: func(t *testing.T, outputDir string) {
				path := filepath.Join(outputDir, "output.pprof")
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Error("expected output.pprof to exist")
				}
				assertProfileSampleTotal(t, path, "simd_groups", 1)
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

func assertProfileSampleTotal(t *testing.T, path, sampleType string, min int64) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open profile: %v", err)
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	index := -1
	for i, sample := range prof.SampleType {
		if sample.Type == sampleType {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("missing sample type %q", sampleType)
	}
	var total int64
	for _, sample := range prof.Sample {
		if index < len(sample.Value) {
			total += sample.Value[index]
		}
	}
	if total < min {
		t.Fatalf("sample type %q total = %d, want at least %d", sampleType, total, min)
	}
}
