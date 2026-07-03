package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

func TestWriteEncodersJSON(t *testing.T) {
	var out bytes.Buffer
	if err := writeEncodersJSON(&out, testEncoders()); err != nil {
		t.Fatalf("writeEncodersJSON: %v", err)
	}

	want := `[
  {
    "index": 0,
    "label": "kernel_a",
    "address": "0x10"
  },
  {
    "index": 7,
    "label": "",
    "address": "0x20"
  }
]
`
	if got := out.String(); got != want {
		t.Fatalf("json output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteEncodersText(t *testing.T) {
	tests := []struct {
		name               string
		commandBufferCount int
		commandBuffers     []encodersCommandBufferSummary
		want               string
	}{
		{
			name: "normal",
			want: "2 encoders:\n" +
				"    0: kernel_a\n" +
				"    7: (unlabeled) 0x20\n",
		},
		{
			name:               "verbose",
			commandBufferCount: 2,
			commandBuffers: []encodersCommandBufferSummary{
				{index: 0, encoderCount: 1},
				{index: 1, encoderCount: 2},
			},
			want: "2 encoders:\n" +
				"    0: kernel_a\n" +
				"    7: (unlabeled) 0x20\n" +
				"\n" +
				"2 command buffers (1.0 encoders/buffer avg)\n" +
				"  CB 0: 1 encoders\n" +
				"  CB 1: 2 encoders\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := writeEncodersText(&out, testEncoders(), tt.commandBufferCount, tt.commandBuffers)
			if err != nil {
				t.Fatalf("writeEncodersText: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Fatalf("text output mismatch\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestRunEncodersJSONUsesCommandOutput(t *testing.T) {
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	opts := &encodersOptions{json: true}

	stdout, err := captureStdout(t, func() error {
		return runEncoders(command, []string{testEncodersTracePath(t)}, opts)
	})
	if err != nil {
		t.Fatalf("runEncoders: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("command output missing trailing newline: %q", got)
	}

	var got []struct {
		Index   int    `json:"index"`
		Label   string `json:"label"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("command output is invalid json: %v\n%s", err, out.String())
	}
	if len(got) == 0 {
		t.Fatalf("json encoders = 0, want at least 1:\n%s", out.String())
	}
}

func TestRunEncodersTextUsesCommandOutput(t *testing.T) {
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	opts := &encodersOptions{}

	stdout, err := captureStdout(t, func() error {
		return runEncoders(command, []string{testEncodersTracePath(t)}, opts)
	})
	if err != nil {
		t.Fatalf("runEncoders: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}
	if got := out.String(); !strings.Contains(got, " encoders:\n") {
		t.Fatalf("command output = %q, want encoder header", got)
	}
}

func testEncoders() []*gputrace.ComputeEncoder {
	return []*gputrace.ComputeEncoder{
		{Index: 0, Label: "kernel_a", Address: 0x10},
		{Index: 7, Address: 0x20},
	}
}

func testEncodersTracePath(t *testing.T) string {
	t.Helper()

	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}
	return tracePath
}
