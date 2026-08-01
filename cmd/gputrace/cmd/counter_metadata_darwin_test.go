//go:build darwin

package cmd

import (
	"slices"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func TestApplyXcodeCounterMetadata(t *testing.T) {
	graph := &counter.GPUCounterGraph{
		Path: "/Applications/Xcode-rc.app/GPUCounterGraph.plist",
		Counters: map[string]counter.CounterMetadata{
			"ALU Utilization": {Unit: "Percentage of Peak ALU Performance"},
		},
		TimelineGroups: []counter.TimelineGroup{
			{Name: "ALU", Counters: []string{"ALU Utilization"}},
			{Name: "Secondary", Counters: []string{"ALU Utilization"}},
		},
	}
	tracks := []CounterTrack{
		{Name: "ALU Utilization", Unit: "%"},
		{Name: "GPU Cycles", Unit: "cycles"},
	}

	got := applyXcodeCounterMetadataFromGraph(tracks, graph)
	if got[0].Unit != "Percentage of Peak ALU Performance" {
		t.Fatalf("ALU unit = %q", got[0].Unit)
	}
	if got[0].XcodeCatalogPath != graph.Path {
		t.Fatalf("ALU catalog path = %q, want %q", got[0].XcodeCatalogPath, graph.Path)
	}
	if want := []string{"ALU", "Secondary"}; !slices.Equal(got[0].XcodeGroups, want) {
		t.Fatalf("ALU groups = %q, want %q", got[0].XcodeGroups, want)
	}
	if got[1].Unit != "cycles" || got[1].XcodeCatalogPath != "" || len(got[1].XcodeGroups) != 0 {
		t.Fatalf("unmatched archive track changed: %+v", got[1])
	}
}
