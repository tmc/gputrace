//go:build darwin

package cmd

import "testing"

func TestParityTracePathFindsSingleBundle(t *testing.T) {
	path, err := parityTracePath("../../../testdata/traces/06-six-encoders")
	if err != nil {
		t.Fatal(err)
	}
	if want := "06-six-encoders-run1.gputrace"; len(path) < len(want) || path[len(path)-len(want):] != want {
		t.Fatalf("parityTracePath = %q, want suffix %q", path, want)
	}
}
