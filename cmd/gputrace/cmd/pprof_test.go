package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
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
			resetPprofTestFlags()

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

func TestPprofSourceLinesDisclosesSyntheticTimingFallback(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"

	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
	}

	resetPprofTestFlags()
	t.Cleanup(resetPprofTestFlags)

	outputPath := filepath.Join(tmpDir, "source.pprof")
	rootCmd.SetArgs([]string{"pprof", tracePath, "--source-lines", "-o", outputPath})

	stdout, err := captureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatal("expected source.pprof to exist")
	}
	for _, want := range []string{
		"Timing source: synthetic fallback",
		"no real profiler or encoder label timing found",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestPprofStdoutContainsOnlyProfile(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"

	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
	}

	resetPprofTestFlags()
	t.Cleanup(resetPprofTestFlags)

	rootCmd.SetArgs([]string{"pprof", tracePath, "-o", "/dev/stdout"})

	stdout, err := captureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	for _, bad := range []string{
		"GPU profile written",
		"Pprof: Found",
		"No stats provided",
	} {
		if strings.Contains(stdout, bad) {
			t.Fatalf("stdout contains status text %q", bad)
		}
	}
	if _, err := profile.Parse(bytes.NewReader([]byte(stdout))); err != nil {
		t.Fatalf("stdout is not a clean pprof profile: %v", err)
	}
}

func TestPprofSourceLinesStdoutContainsOnlyProfile(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"

	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
	}

	resetPprofTestFlags()
	t.Cleanup(resetPprofTestFlags)

	rootCmd.SetArgs([]string{"pprof", tracePath, "--source-lines", "-o", "/dev/stdout"})

	stdout, err := captureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	for _, bad := range []string{
		"Timing source:",
		"Source-lines pprof written",
		"go tool pprof",
	} {
		if strings.Contains(stdout, bad) {
			t.Fatalf("stdout contains status text %q", bad)
		}
	}
	if _, err := profile.Parse(bytes.NewReader([]byte(stdout))); err != nil {
		t.Fatalf("stdout is not a clean source-lines pprof profile: %v", err)
	}
}

func TestPprofStatusWriterUsesStderrForStdoutOutput(t *testing.T) {
	tests := []struct {
		name string
		path string
		want *os.File
	}{
		{name: "file", path: "gpu.pprof", want: os.Stdout},
		{name: "stdout", path: "/dev/stdout", want: os.Stderr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pprofStatusWriter(tt.path); got != tt.want {
				t.Fatalf("pprofStatusWriter(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatSourceLineTimingNotice(t *testing.T) {
	tests := []struct {
		name   string
		source sourceLineTimingSource
		count  int
		want   string
	}{
		{
			name:   "profiler",
			source: sourceLineTimingProfiler,
			count:  2,
			want:   "Timing source: profiler .gpuprofiler_raw data (2 encoders)\n",
		},
		{
			name:   "encoder labels",
			source: sourceLineTimingEncoderLabels,
			count:  1,
			want:   "Timing source: encoder label timing data (1 encoder)\n",
		},
		{
			name:   "synthetic",
			source: sourceLineTimingSynthetic,
			count:  0,
			want:   "Timing source: synthetic fallback (0 encoders; no real profiler or encoder label timing found)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSourceLineTimingNotice(tt.source, tt.count); got != tt.want {
				t.Fatalf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

func resetPprofTestFlags() {
	output = ""
	prefix = ""
	all = false
	verbose = false
	textReport = false
	showStats = false
	searchPaths = nil
	sourceLines = false
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}

	os.Stdout = writer
	outputCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		outputCh <- buf.String()
	}()

	runErr := run()
	_ = writer.Close()
	os.Stdout = oldStdout
	out := <-outputCh
	_ = reader.Close()

	return out, runErr
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
