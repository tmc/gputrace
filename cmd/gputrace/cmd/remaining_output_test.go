package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestMTLBHumanCommandsUseCommandOutput(t *testing.T) {
	tracePath := testCommandBuffersTracePath(t)
	tests := []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{name: "root", run: func(cmd *cobra.Command) error {
			return runMTLB(cmd, []string{tracePath}, &mtlbOptions{})
		}},
		{name: "list", run: func(cmd *cobra.Command) error {
			return runMTLBList(cmd, []string{tracePath}, &mtlbListOptions{})
		}},
		{name: "info", run: func(cmd *cobra.Command) error {
			return runMTLBInfo(cmd, []string{tracePath}, &mtlbInfoOptions{})
		}},
		{name: "stats", run: func(cmd *cobra.Command) error {
			return runMTLBStats(cmd, []string{tracePath}, &mtlbStatsOptions{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)
			stdout, err := captureStdout(t, func() error { return tt.run(cmd) })
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if stdout != "" {
				t.Fatalf("os stdout = %q, want empty", stdout)
			}
			if out.Len() == 0 {
				t.Fatal("command output is empty")
			}
		})
	}
}

func TestBufferResourcesUsesCommandOutput(t *testing.T) {
	tracePath := testCommandBuffersTracePath(t)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	opts := &buffersCommandOptions{
		sort:          "size",
		format:        "table",
		inspectBytes:  256,
		inspectFormat: "hex",
		resources:     true,
		limit:         defaultHumanLimit,
	}
	stdout, err := captureStdout(t, func() error {
		return runBuffers(cmd, []string{tracePath}, opts)
	})
	if err != nil {
		t.Fatalf("runBuffers: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}
	if out.Len() == 0 {
		t.Fatal("command output is empty")
	}
}
