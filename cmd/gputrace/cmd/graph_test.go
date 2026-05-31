package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestWriteGraphOutputStdoutSentinels(t *testing.T) {
	for _, outputPath := range []string{"/dev/stdout", "-"} {
		t.Run(outputPath, func(t *testing.T) {
			cmd := &cobra.Command{}
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)

			stdout, err := captureStdout(t, func() error {
				return writeGraphOutput(cmd, outputPath, "digraph G {}\n")
			})
			if err != nil {
				t.Fatalf("writeGraphOutput: %v", err)
			}
			if got, want := stdout, "digraph G {}\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if got := stderr.String(); got != "" {
				t.Fatalf("stderr = %q, want empty", got)
			}
		})
	}
}

func TestWriteGraphOutputDefaultUsesCommandStdout(t *testing.T) {
	cmd := &cobra.Command{}
	var commandStdout bytes.Buffer
	cmd.SetOut(&commandStdout)

	osStdout, err := captureStdout(t, func() error {
		return writeGraphOutput(cmd, "", "digraph G {}\n")
	})
	if err != nil {
		t.Fatalf("writeGraphOutput: %v", err)
	}
	if osStdout != "" {
		t.Fatalf("os stdout = %q, want empty", osStdout)
	}
	if got, want := commandStdout.String(), "digraph G {}\n"; got != want {
		t.Fatalf("command stdout = %q, want %q", got, want)
	}
}

func TestWriteGraphOutputFileStatusUsesStderr(t *testing.T) {
	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	outputPath := filepath.Join(t.TempDir(), "graph.dot")
	stdout, err := captureStdout(t, func() error {
		return writeGraphOutput(cmd, outputPath, "digraph G {}\n")
	})
	if err != nil {
		t.Fatalf("writeGraphOutput: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if got, want := string(data), "digraph G {}\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "Graph written to "+outputPath+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
