package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunTreeTextUsesCommandOutput(t *testing.T) {
	tracePath := testCommandBuffersTracePath(t)
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	stdout, err := captureStdout(t, func() error {
		return runTree(command, []string{tracePath}, &treeOptions{groupBy: "encoder", limit: 1})
	})
	if err != nil {
		t.Fatalf("runTree: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}
	if !strings.Contains(out.String(), "GPU Timeline") {
		t.Fatalf("command output missing semantic topology:\n%s", out.String())
	}
}

func TestRunTreeRecordsUsesRecordOrderView(t *testing.T) {
	tracePath := testCommandBuffersTracePath(t)
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	if err := runTree(command, []string{tracePath}, &treeOptions{groupBy: "encoder", records: true, limit: 1}); err != nil {
		t.Fatalf("runTree: %v", err)
	}
	if !strings.Contains(out.String(), "record-order view") {
		t.Fatalf("command output missing record-order label:\n%s", out.String())
	}
}
