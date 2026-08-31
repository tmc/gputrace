package analysis

import (
	"strings"
	"testing"
)

func TestBufferAccessSuppressesAdviceWhenAttributionIncomplete(t *testing.T) {
	analysis := &BufferAccessAnalysis{
		BufferAccesses: map[uint64]*BufferAccessInfo{
			1: {Address: 1, AccessCount: 1},
		},
		BindingGroups: map[int]*BindingGroupInfo{
			0: {CSOrdinal: 0, BufferCount: 1},
		},
		TotalBuffers:        1,
		ExpectedEncoders:    4,
		AttributedGroups:    1,
		AttributionComplete: false,
		AttributionNote:     "test attribution gap",
	}

	out := FormatBufferAccessReport(analysis, false)
	if !strings.Contains(out, "Attribution:     incomplete") ||
		!strings.Contains(out, "Optimization advice withheld because encoder attribution is incomplete") {
		t.Fatalf("report missing incomplete-attribution warning:\n%s", out)
	}
	for _, bad := range []string{"patterns appear well-optimized", "could be removed", "potential memory reuse"} {
		if strings.Contains(out, bad) {
			t.Fatalf("report contains unsupported advice %q:\n%s", bad, out)
		}
	}
}

// Binding groups are keyed by CS ordinal. A reader who takes those keys for
// encoder indices builds a join that mislabels which kernel touched which
// buffer, so the report has to say what the keys are and the note has to say
// the two populations are not mapped.
func TestFormatBufferAccessReportDoesNotOfferGroupsAsEncoders(t *testing.T) {
	analysis := &BufferAccessAnalysis{
		BufferAccesses: map[uint64]*BufferAccessInfo{
			0x1000: {Address: 0x1000, AccessCount: 8, GroupOrdinals: []int{3, 6, 9}, IsShared: true},
		},
		BindingGroups: map[int]*BindingGroupInfo{
			3: {CSOrdinal: 3, BufferCount: 8, UniqueBuffers: []uint64{0x1000}},
			6: {CSOrdinal: 6, BufferCount: 8, UniqueBuffers: []uint64{0x1000}},
		},
		TotalBuffers:     1,
		SharedBuffers:    1,
		ExpectedEncoders: 3,
		AttributedGroups: 2,
		AttributionNote:  "binding groups (by CS ordinal): 2; trace-reported compute encoders: 3",
	}
	analysis.computeStatistics()

	report := FormatBufferAccessReport(analysis, true)
	for _, want := range []string{
		"Binding Groups:",
		"not encoder index",
		"CS ordinal 3:",
		"would mislabel which kernel",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	// "Encoder 3" is the old rendering and is the thing that invited the join.
	if strings.Contains(report, "Encoder 3") {
		t.Errorf("report still renders a CS ordinal as an encoder:\n%s", report)
	}
}
