package perfettosql

import (
	"bytes"
	"strings"
	"testing"
)

func TestModuleDefinesStableViews(t *testing.T) {
	for _, name := range []string{
		"gputrace_capture",
		"gputrace_semantic_node",
		"gputrace_semantic_link",
		"gputrace_dispatch",
		"gputrace_pipeline",
		"gputrace_counter_series",
		"gputrace_unmatched",
	} {
		if !strings.Contains(Module, "CREATE PERFETTO VIEW "+name+" AS") {
			t.Errorf("module does not define %s", name)
		}
	}
	var out bytes.Buffer
	if err := Write(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != Module {
		t.Fatal("Write output differs from Module")
	}
}
