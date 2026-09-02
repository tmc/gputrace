package optimize

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/gpuevent"
)

func TestSuggestCoversEveryFindingKind(t *testing.T) {
	findings := []gpuevent.Finding{
		{Kind: gpuevent.FindingDominance, Subject: "k1", Evidence: []string{"grid 100x1x1 x block 32x8x1"}, Hypothesis: "dominates measured GPU time; reduce launch count"},
		{Kind: gpuevent.FindingLaunchShape, Subject: "k2"},
		{Kind: gpuevent.FindingLongTail, Subject: "k3"},
		{Kind: gpuevent.FindingTransferHeavy, Subject: "(all transfers)"},
	}
	entries := Suggest(findings)
	kinds := map[gpuevent.FindingKind]bool{}
	for _, e := range entries {
		kinds[e.FindingKind] = true
		if e.Action == "" || e.VerifyWith == "" {
			t.Errorf("entry missing action or verify: %+v", e)
		}
	}
	for _, kind := range []gpuevent.FindingKind{
		gpuevent.FindingDominance, gpuevent.FindingLaunchShape,
		gpuevent.FindingLongTail, gpuevent.FindingTransferHeavy,
	} {
		if !kinds[kind] {
			t.Errorf("no suggestion for %v", kind)
		}
	}
}

func TestSuggestDominanceByBound(t *testing.T) {
	memory := Suggest([]gpuevent.Finding{{
		Kind:       gpuevent.FindingDominance,
		Subject:    "copy",
		Evidence:   []string{"grid 4x1x1 x block 256x1x1"},
		Hypothesis: "bandwidth limitation; consider wider vectorized loads",
	}})
	foundCoalesce := false
	for _, e := range memory {
		if strings.Contains(e.Action, "coalescing") {
			foundCoalesce = true
			if e.Bound != gpuevent.BoundMemory {
				t.Errorf("memory action bound = %v", e.Bound)
			}
		}
	}
	if !foundCoalesce {
		t.Error("memory-bound dominance did not suggest coalescing")
	}

	latency := Suggest([]gpuevent.Finding{{
		Kind:       gpuevent.FindingDominance,
		Subject:    "tiny",
		Evidence:   []string{"grid 1x1x1 x block 32x1x1"},
		Hypothesis: "launches too few threads to fill the device",
	}})
	if len(latency) != 1 || !strings.Contains(latency[0].Action, "parallelism") {
		t.Errorf("latency-bound dominance suggestions = %+v", latency)
	}

	compute := Suggest([]gpuevent.Finding{{
		Kind:       gpuevent.FindingDominance,
		Subject:    "hot",
		Evidence:   []string{"grid 112x1x1 x block 32x8x1"},
		Hypothesis: "dominates measured GPU time; reduce launch count",
	}})
	foundTile := false
	for _, e := range compute {
		if strings.Contains(e.Action, "tile") {
			foundTile = true
		}
	}
	if !foundTile {
		t.Error("compute-bound dominance did not suggest tiling")
	}
}

func TestRenderSuggestions(t *testing.T) {
	entries := Suggest([]gpuevent.Finding{{Kind: gpuevent.FindingTransferHeavy}})
	out := RenderSuggestions(entries)
	if !strings.Contains(out, "verify:") || !strings.Contains(out, "pinned") {
		t.Errorf("rendered suggestions =\n%s", out)
	}
}
