//go:build darwin

package cmd

import "testing"

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
