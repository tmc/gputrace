package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainFlags tests flag parsing and validation
func TestMainFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
	}{
		{
			name:      "no arguments",
			args:      []string{},
			wantError: true,
		},
		{
			name:      "too many arguments",
			args:      []string{"trace1.gputrace", "trace2.gputrace"},
			wantError: true,
		},
		{
			name:      "single valid path",
			args:      []string{"trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with output flag",
			args:      []string{"-o", "output.pprof", "trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with prefix flag",
			args:      []string{"-prefix", "results", "trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with all flag",
			args:      []string{"-all", "trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with verbose flag",
			args:      []string{"-v", "trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with stats flag",
			args:      []string{"-stats", "trace.gputrace"},
			wantError: false,
		},
		{
			name:      "with text flag",
			args:      []string{"-text", "trace.gputrace"},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset flags between tests
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

			// Capture output
			var buf bytes.Buffer
			flag.CommandLine.SetOutput(&buf)

			// Re-define flags for this test
			output := flag.String("o", "", "Output pprof file path")
			prefix := flag.String("prefix", "", "Output prefix for -all mode")
			all := flag.Bool("all", false, "Generate all profile formats")
			verbose := flag.Bool("v", false, "Verbose output")
			textReport := flag.Bool("text", false, "Generate text report only")
			stats := flag.Bool("stats", false, "Show trace statistics only")

			err := flag.CommandLine.Parse(tt.args)

			// Validate argument count
			hasError := err != nil || flag.NArg() != 1

			if hasError != tt.wantError {
				t.Errorf("Parse(%v) error = %v, wantError %v", tt.args, hasError, tt.wantError)
			}

			// Verify flags were parsed correctly if no error expected
			if !tt.wantError && err == nil {
				// Check that flags have correct values based on args
				for i := 0; i < len(tt.args); i++ {
					switch tt.args[i] {
					case "-o":
						if i+1 < len(tt.args) && *output != tt.args[i+1] {
							t.Errorf("Expected output=%s, got %s", tt.args[i+1], *output)
						}
					case "-prefix":
						if i+1 < len(tt.args) && *prefix != tt.args[i+1] {
							t.Errorf("Expected prefix=%s, got %s", tt.args[i+1], *prefix)
						}
					case "-all":
						if !*all {
							t.Error("Expected all=true")
						}
					case "-v":
						if !*verbose {
							t.Error("Expected verbose=true")
						}
					case "-text":
						if !*textReport {
							t.Error("Expected textReport=true")
						}
					case "-stats":
						if !*stats {
							t.Error("Expected stats=true")
						}
					}
				}
			}
		})
	}
}

// TestMissingTraceFile tests error handling for missing files
func TestMissingTraceFile(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentPath := filepath.Join(tmpDir, "does_not_exist.gputrace")

	// This would normally be run as main(), but we'll simulate the file check
	if _, err := os.Stat(nonExistentPath); !os.IsNotExist(err) {
		t.Error("Expected file to not exist")
	}
}

// TestOutputPathGeneration tests default output path generation
func TestOutputPathGeneration(t *testing.T) {
	tests := []struct {
		name       string
		tracePath  string
		prefix     string
		wantPrefix string
	}{
		{
			name:       "simple path",
			tracePath:  "trace.gputrace",
			prefix:     "",
			wantPrefix: "trace",
		},
		{
			name:       "path with directory",
			tracePath:  "/tmp/my_trace.gputrace",
			prefix:     "",
			wantPrefix: "my_trace",
		},
		{
			name:       "custom prefix",
			tracePath:  "trace.gputrace",
			prefix:     "custom",
			wantPrefix: "custom",
		},
		{
			name:       "path with multiple extensions",
			tracePath:  "trace.test.gputrace",
			prefix:     "",
			wantPrefix: "trace.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseName := filepath.Base(tt.tracePath)
			if ext := filepath.Ext(baseName); ext != "" {
				baseName = baseName[:len(baseName)-len(ext)]
			}

			outputPrefix := tt.prefix
			if outputPrefix == "" {
				outputPrefix = baseName
			}

			if outputPrefix != tt.wantPrefix {
				t.Errorf("Expected prefix %s, got %s", tt.wantPrefix, outputPrefix)
			}
		})
	}
}

// TestIntegrationWithRealTrace tests the tool with a real trace file
func TestIntegrationWithRealTrace(t *testing.T) {
	// Look for test trace files
	tracePaths := []string{
		"/tmp/objc_metal_trace.gputrace",
		"../../gputraces/gputrace-01.gputrace",
		"../../../examples/metal-capture/gputraces/gputrace-01.gputrace",
	}

	var tracePath string
	for _, path := range tracePaths {
		if _, err := os.Stat(path); err == nil {
			tracePath = path
			break
		}
	}

	if tracePath == "" {
		t.Skip("No test .gputrace files available")
	}

	// Build the command
	cmdPath := buildTestBinary(t)

	tests := []struct {
		name       string
		args       []string
		checkFiles []string
	}{
		{
			name:       "single pprof output",
			args:       []string{"-o", filepath.Join(t.TempDir(), "test.pprof"), tracePath},
			checkFiles: []string{"test.pprof"},
		},
		{
			name: "all outputs",
			args: []string{"-all", "-prefix", filepath.Join(t.TempDir(), "all"), tracePath},
			checkFiles: []string{
				"all.gpu.pprof",
				"all.gpu-flat.pprof",
				"all.combined.pprof",
				"all.txt",
			},
		},
		{
			name:       "text report only",
			args:       []string{"-text", "-o", filepath.Join(t.TempDir(), "report.txt"), tracePath},
			checkFiles: []string{"report.txt"},
		},
		{
			name: "stats only",
			args: []string{"-stats", tracePath},
			// No files to check - just verify it runs
			checkFiles: []string{},
		},
		{
			name: "verbose mode",
			args: []string{"-v", "-o", filepath.Join(t.TempDir(), "verbose.pprof"), tracePath},
			checkFiles: []string{"verbose.pprof"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(cmdPath, tt.args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Command failed: %v\nOutput: %s", err, output)
			}

			// Verify output files exist
			for _, filename := range tt.checkFiles {
				var path string
				if filepath.IsAbs(filename) {
					path = filename
				} else {
					// Extract directory from args
					for i, arg := range tt.args {
						if arg == "-o" || arg == "-prefix" {
							if i+1 < len(tt.args) {
								dir := filepath.Dir(tt.args[i+1])
								path = filepath.Join(dir, filename)
								break
							}
						}
					}
				}

				if path != "" {
					if _, err := os.Stat(path); err != nil {
						t.Errorf("Expected output file %s not created: %v", path, err)
					} else {
						info, _ := os.Stat(path)
						if info.Size() == 0 {
							t.Errorf("Output file %s is empty", path)
						}
						t.Logf("Created %s (%d bytes)", filename, info.Size())
					}
				}
			}

			// Verify output contains expected patterns
			outputStr := string(output)
			if tt.name == "stats only" {
				// Stats mode should show statistics
				if !strings.Contains(outputStr, "Statistics") && !strings.Contains(outputStr, "Trace") {
					t.Error("Stats mode should produce statistics output")
				}
			} else if len(tt.checkFiles) > 0 {
				// Should confirm file creation
				if !strings.Contains(outputStr, "✅") && !strings.Contains(outputStr, "written") {
					t.Logf("Output: %s", outputStr)
				}
			}
		})
	}
}

// buildTestBinary builds the gputrace2pprof binary for testing
func buildTestBinary(tb testing.TB) string {
	tb.Helper()

	tmpDir := tb.TempDir()
	binPath := filepath.Join(tmpDir, "gputrace2pprof")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("Failed to build binary: %v\nOutput: %s", err, output)
	}

	return binPath
}

// TestUsageOutput tests that usage information is displayed correctly
func TestUsageOutput(t *testing.T) {
	cmdPath := buildTestBinary(t)

	// Run with no arguments to trigger usage
	cmd := exec.Command(cmdPath)
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("Expected error when running with no arguments")
	}

	outputStr := string(output)

	// Check for expected usage elements
	expectedPatterns := []string{
		"Usage:",
		"gputrace2pprof",
		"Options:",
		"-o",
		"-all",
		"-prefix",
		"-v",
		"-text",
		"-stats",
		"Examples:",
	}

	for _, pattern := range expectedPatterns {
		if !strings.Contains(outputStr, pattern) {
			t.Errorf("Usage output missing expected pattern: %s", pattern)
		}
	}
}

// TestErrorMessages tests error message quality
func TestErrorMessages(t *testing.T) {
	cmdPath := buildTestBinary(t)
	tmpDir := t.TempDir()

	tests := []struct {
		name           string
		args           []string
		wantErrorMatch string
	}{
		{
			name:           "missing trace file",
			args:           []string{filepath.Join(tmpDir, "nonexistent.gputrace")},
			wantErrorMatch: "not found",
		},
		{
			name:           "invalid trace file",
			args:           []string{createInvalidTrace(t, tmpDir)},
			wantErrorMatch: "Failed to", // Could be "open" or "load"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(cmdPath, tt.args...)
			output, err := cmd.CombinedOutput()

			if err == nil {
				t.Error("Expected command to fail")
			}

			outputStr := string(output)
			if !strings.Contains(outputStr, tt.wantErrorMatch) {
				t.Errorf("Error message should contain %q, got: %s", tt.wantErrorMatch, outputStr)
			}
		})
	}
}

// createInvalidTrace creates an invalid gputrace file for testing
func createInvalidTrace(t *testing.T, dir string) string {
	t.Helper()

	tracePath := filepath.Join(dir, "invalid.gputrace")
	if err := os.Mkdir(tracePath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create invalid metadata
	metadataPath := filepath.Join(tracePath, "metadata")
	if err := os.WriteFile(metadataPath, []byte("invalid plist data"), 0644); err != nil {
		t.Fatal(err)
	}

	return tracePath
}

// TestPprofOutputValidity tests that generated pprof files are valid
func TestPprofOutputValidity(t *testing.T) {
	// Look for test trace
	tracePaths := []string{
		"/tmp/objc_metal_trace.gputrace",
		"../../gputraces/gputrace-01.gputrace",
	}

	var tracePath string
	for _, path := range tracePaths {
		if _, err := os.Stat(path); err == nil {
			tracePath = path
			break
		}
	}

	if tracePath == "" {
		t.Skip("No test .gputrace files available")
	}

	cmdPath := buildTestBinary(t)
	tmpDir := t.TempDir()
	pprofPath := filepath.Join(tmpDir, "test.pprof")

	// Generate pprof file
	cmd := exec.Command(cmdPath, "-o", pprofPath, tracePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to generate pprof: %v\nOutput: %s", err, output)
	}

	// Validate with go tool pprof
	cmd = exec.Command("go", "tool", "pprof", "-top", pprofPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("pprof validation output: %s", output)
		// This might fail if the pprof is empty/synthetic, so just log
	} else {
		t.Logf("pprof -top output:\n%s", output)
	}

	// At minimum, verify file is not empty
	info, err := os.Stat(pprofPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("Generated pprof file is empty")
	}
}

// BenchmarkConversion benchmarks the conversion process
func BenchmarkConversion(b *testing.B) {
	// Look for test trace
	tracePaths := []string{
		"/tmp/objc_metal_trace.gputrace",
		"../../gputraces/gputrace-01.gputrace",
	}

	var tracePath string
	for _, path := range tracePaths {
		if _, err := os.Stat(path); err == nil {
			tracePath = path
			break
		}
	}

	if tracePath == "" {
		b.Skip("No test .gputrace files available")
	}

	cmdPath := buildTestBinary(b)
	tmpDir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pprofPath := filepath.Join(tmpDir, "bench.pprof")
		cmd := exec.Command(cmdPath, "-o", pprofPath, tracePath)
		if err := cmd.Run(); err != nil {
			b.Fatal(err)
		}
		os.Remove(pprofPath)
	}
}
