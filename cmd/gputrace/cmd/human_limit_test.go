package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteLimitedLines(t *testing.T) {
	var out bytes.Buffer
	if err := writeLimitedLines(&out, "one\ntwo\nthree\n", 2, "rows"); err != nil {
		t.Fatalf("writeLimitedLines: %v", err)
	}
	if got, want := out.String(), "one\ntwo\n... 1 more rows omitted (use --all)\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestFormatBuffersTableLimitAndWriter(t *testing.T) {
	buffers := []BufferInfo{
		{ID: "1", Filename: "MTLBuffer-1-0", Size: 1},
		{ID: "2", Filename: "MTLBuffer-2-0", Size: 2},
		{ID: "3", Filename: "MTLBuffer-3-0", Size: 3},
	}
	var out bytes.Buffer
	if err := formatBuffersTable(&out, buffers, nil, 2); err != nil {
		t.Fatalf("formatBuffersTable: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "3 buffers, 6 B") ||
		!strings.Contains(got, "MTLBuffer-1-0") ||
		!strings.Contains(got, "MTLBuffer-2-0") ||
		strings.Contains(got, "MTLBuffer-3-0") ||
		!strings.Contains(got, "... 1 more buffers omitted (use --all)") {
		t.Fatalf("unexpected limited table:\n%s", got)
	}
}
