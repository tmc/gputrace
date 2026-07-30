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
		EncoderAccesses: map[int]*EncoderAccessInfo{
			0: {EncoderID: 0, BufferCount: 1},
		},
		TotalBuffers:        1,
		ExpectedEncoders:    4,
		AttributedEncoders:  1,
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
