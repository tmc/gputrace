package timing

import (
	"strings"
	"testing"
)

func callTimings(counts ...int) []*KernelTiming {
	var out []*KernelTiming
	for i, n := range counts {
		out = append(out, &KernelTiming{Name: string(rune('a' + i)), InvocationCount: n})
	}
	return out
}

// The default must not filter. A cost-ranked table with rows removed is still
// presented as a ranking, which is the same defect the marker exists to fix.
func TestFilterMinCallsIsOffByDefault(t *testing.T) {
	timings := callTimings(1, 1, 56)
	for _, min := range []int{0, 1} {
		kept, dropped := FilterMinCalls(timings, min)
		if dropped != 0 || len(kept) != len(timings) {
			t.Errorf("min=%d dropped %d, kept %d; want everything kept", min, dropped, len(kept))
		}
	}
}

func TestFilterMinCallsDropsAndCounts(t *testing.T) {
	kept, dropped := FilterMinCalls(callTimings(1, 2, 56), 2)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d rows, want 2", len(kept))
	}
	for _, kt := range kept {
		if kt.InvocationCount < 2 {
			t.Errorf("kept a row with %d calls", kt.InvocationCount)
		}
	}
}

// Filtering breaks the share column, and the table has to say so itself rather
// than rely on the reader recalling which flag they passed.
func TestMinCallsNoteReportsTheDropAndTheBrokenShare(t *testing.T) {
	note := MinCallsNote(2, 3, 20)
	for _, want := range []string{"--min-calls 2", "dropped 3 of 20", "the shares above"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to contain %q", note, want)
		}
	}
	if MinCallsNote(2, 0, 20) != "" {
		t.Error("a filter that dropped nothing produced a note")
	}
}

func TestLowSampleFootnoteOnlyWhenMarked(t *testing.T) {
	if got := LowSampleFootnote(callTimings(2, 56)); got != "" {
		t.Errorf("footnote = %q, want none when no row is marked", got)
	}
	got := LowSampleFootnote(callTimings(1, 56))
	if !strings.Contains(got, LowSampleMarker) || !strings.Contains(got, "1 of 2") {
		t.Errorf("footnote = %q, want the marker and the marked count", got)
	}
}
