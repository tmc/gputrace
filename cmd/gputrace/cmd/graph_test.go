package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateGraphOptionsAcceptsKnownValues(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		graphType string
	}{
		{name: "dot hierarchy", format: "dot", graphType: "hierarchy"},
		{name: "mermaid flow", format: "mermaid", graphType: "flow"},
		{name: "dot resources", format: "dot", graphType: "resources"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateGraphOptions(tt.format, tt.graphType)
			if err != nil {
				t.Fatalf("validateGraphOptions: %v", err)
			}
			if got.format != tt.format {
				t.Fatalf("format = %q, want %q", got.format, tt.format)
			}
			if got.graphType != tt.graphType {
				t.Fatalf("graphType = %q, want %q", got.graphType, tt.graphType)
			}
		})
	}
}

func TestRunGraphValidatesOptionsBeforeTraceIO(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		graphType string
		want      string
	}{
		{
			name:      "invalid format",
			format:    "json",
			graphType: "hierarchy",
			want:      `invalid graph format "json" (must be dot or mermaid)`,
		},
		{
			name:      "invalid type",
			format:    "dot",
			graphType: "timeline",
			want:      `invalid graph type "timeline" (must be hierarchy, flow, or resources)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldFormat := graphFormat
			oldType := graphType
			graphFormat = tt.format
			graphType = tt.graphType
			t.Cleanup(func() {
				graphFormat = oldFormat
				graphType = oldType
			})

			missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
			err := runGraph(nil, []string{missingTrace})
			if err == nil {
				t.Fatal("runGraph succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

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
