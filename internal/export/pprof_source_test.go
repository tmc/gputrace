package export

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestToPprofWithSourceFormatsKernelCount(t *testing.T) {
	prof, err := ToPprofWithSource(&trace.Trace{
		Path:        "test.gputrace",
		KernelNames: []string{"add", "mul"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("ToPprofWithSource failed: %v", err)
	}

	comments := strings.Join(prof.Comments, "\n")
	if !strings.Contains(comments, "Kernels: 2") {
		t.Fatalf("comments missing decimal kernel count:\n%q", comments)
	}
	if strings.ContainsRune(comments, rune(2)) {
		t.Fatalf("comments contain raw kernel count rune:\n%q", comments)
	}
}
