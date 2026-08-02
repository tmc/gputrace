//go:build darwin

package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExactTraceWindow(t *testing.T) {
	windows := []xcodeAXWindow{
		{Element: 1, Document: "/tmp/other.gputrace"},
		{Element: 2, Document: "/tmp/target.gputrace"},
	}

	window, err := exactTraceWindow(windows, strings.ToLower(filepath.Clean("/tmp/target.gputrace")))
	if err != nil || window != 2 {
		t.Fatalf("exactTraceWindow(target) = %d, %v, want 2, nil", window, err)
	}

	window, err = exactTraceWindow(windows, strings.ToLower(filepath.Clean("/tmp/missing.gputrace")))
	if err == nil || window != 0 {
		t.Fatalf("exactTraceWindow(missing) = %d, %v, want 0, error", window, err)
	}
}

func TestWindowSnapshotContainsTarget(t *testing.T) {
	windows := []xcodeAXWindow{
		{Title: "Other", Document: "/tmp/other.gputrace"},
		{Title: "Target", Document: "/tmp/target.gputrace"},
	}
	if !windowSnapshotContainsTarget(windows, "Target", "/tmp/target.gputrace", 2) {
		t.Fatal("document-bound target reported absent")
	}
	if windowSnapshotContainsTarget(windows[:1], "Target", "/tmp/target.gputrace", 2) {
		t.Fatal("closed document-bound target reported present")
	}
	if !windowSnapshotContainsTarget(windows, "target", "", 2) {
		t.Fatal("title-bound target reported absent")
	}
	if windowSnapshotContainsTarget(windows[:1], "target", "", 2) {
		t.Fatal("closed title-bound target reported present")
	}
	if !windowSnapshotContainsTarget(windows, "", "", 2) {
		t.Fatal("untitled target should remain present while window count is unchanged")
	}
	if windowSnapshotContainsTarget(windows[:1], "", "", 2) {
		t.Fatal("untitled target should be absent after window count decreases")
	}
}
