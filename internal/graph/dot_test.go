package graph

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotLabelEscapesDynamicText(t *testing.T) {
	got := dotLabel("kernel \"main\"\npath\\buffer")
	want := `kernel \"main\"\npath\\buffer`
	if got != want {
		t.Fatalf("dotLabel() = %q, want %q", got, want)
	}
}

func TestFlowGraphsDoNotInventDispatchesOrOwnership(t *testing.T) {
	tr := testResourceTrace()
	tests := []struct {
		name string
		gen  Generator
	}{
		{name: "dot", gen: NewDOTGenerator()},
		{name: "mermaid", gen: NewMermaidGenerator()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tt.gen.Generate(&out, tr, &Config{Type: "flow"}); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "Observed CS-label order only") {
				t.Fatalf("flow output missing provenance warning:\n%s", got)
			}
			for _, bad := range []string{"MultipleEncoders_6", "_d0", "_d1", "_d2"} {
				if strings.Contains(got, bad) {
					t.Fatalf("flow output contains synthetic structure %q:\n%s", bad, got)
				}
			}
		})
	}
}

func TestHierarchyGraphsLabelOwnershipAsHeuristic(t *testing.T) {
	tr := testResourceTrace()
	tr.Path = t.TempDir()
	if err := os.WriteFile(filepath.Join(tr.Path, "capture"), tr.CaptureData, 0o644); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	tests := []Generator{NewDOTGenerator(), NewMermaidGenerator()}
	for _, gen := range tests {
		var out bytes.Buffer
		if err := gen.Generate(&out, tr, &Config{Type: "hierarchy"}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got := out.String(); !strings.Contains(got, "ownership of CS labels is heuristic") {
			t.Fatalf("hierarchy output missing heuristic warning:\n%s", got)
		}
	}
}

func TestSanitizeIDReplacesGraphvizDelimiters(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "simple_add", want: "simple_add"},
		{in: `shader "main"/0.1`, want: "shader__main__0_1"},
		{in: "", want: "node"},
	}

	for _, tt := range tests {
		if got := sanitizeID(tt.in); got != tt.want {
			t.Fatalf("sanitizeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
