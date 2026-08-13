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
	if !strings.Contains(out.String(), "GPU execution tree") {
		t.Fatalf("command output missing semantic topology:\n%s", out.String())
	}
}

func TestWriteTreeTopologySummarizesKernels(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:     "CB#0",
			Category: "command_buffer",
			Args:     map[string]interface{}{"index": 0},
		}},
		Encoders: []EncoderInfo{{Index: 0, Label: "attention", Duration: 30}},
		Kernels: []KernelInfo{
			{Name: "copy", Encoder: 0, Duration: 10},
			{Name: "copy", Encoder: 0, Duration: 20},
		},
	}
	var out bytes.Buffer
	if err := writeTreeTopology(&out, timeline, -1, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "copy ×2") {
		t.Fatalf("output does not summarize repeated kernels:\n%s", got)
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
