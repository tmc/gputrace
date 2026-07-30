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
	if !strings.Contains(out.String(), "decoded subset") {
		t.Fatalf("command output missing provenance label:\n%s", out.String())
	}
}
