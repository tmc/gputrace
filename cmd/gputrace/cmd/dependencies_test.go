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

func TestWriteDependencyGraphDOTLimit(t *testing.T) {
	graph := &trace.DependencyGraph{
		Nodes: []trace.DependencyNode{
			{ID: 0, Label: "first"},
			{ID: 1, Label: "second"},
			{ID: 2, Label: "third"},
		},
		Edges: []trace.DependencyEdge{
			{From: 0, To: 1, Buffer: "a", Hazard: trace.HazardRAW},
			{From: 1, To: 2, Buffer: "b", Hazard: trace.HazardRAW},
		},
	}

	var out bytes.Buffer
	if err := writeDependencyGraphDOTLimited(&out, graph, 2); err != nil {
		t.Fatalf("writeDependencyGraphDOTLimited: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "1 nodes and their incident edges omitted") {
		t.Fatalf("limited DOT missing omission notice:\n%s", got)
	}
	if strings.Contains(got, "n2 [") || strings.Contains(got, "n1 -> n2") {
		t.Fatalf("limited DOT references omitted node:\n%s", got)
	}
}
