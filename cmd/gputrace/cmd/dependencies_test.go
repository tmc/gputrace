package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestWriteDependencyGraphDOTEscapesLabels(t *testing.T) {
	graph := &trace.DependencyGraph{
		Nodes: []trace.DependencyNode{
			{ID: 0, Label: "encode \"first\"\npass"},
			{ID: 1, Label: "consume\\second"},
		},
		Edges: []trace.DependencyEdge{
			{From: 0, To: 1, Buffer: "buffer \"main\"\n0", Hazard: trace.HazardRAW},
		},
	}

	var out bytes.Buffer
	if err := writeDependencyGraphDOT(&out, graph); err != nil {
		t.Fatalf("writeDependencyGraphDOT: %v", err)
	}

	for _, want := range []string{
		`n0 [label="encode \"first\"\npass"];`,
		`n1 [label="consume\\second"];`,
		`n0 -> n1 [label="buffer \"main\"\n0 (RAW)"];`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("DOT output missing %q:\n%s", want, out.String())
		}
	}
}
