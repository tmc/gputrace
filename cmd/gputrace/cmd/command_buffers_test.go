package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestWriteCommandBuffersJSON(t *testing.T) {
	out := []commandBufferJSON{
		{
			Index:  1,
			Label:  "cb",
			Offset: "0x00000020",
			Encoders: []commandBufferEncoderJSON{
				{Index: 2, Label: "encoder"},
			},
			Calls:           3,
			PipelineRecords: 3,
			Dispatches:      4,
		},
	}

	var buf bytes.Buffer
	if err := writeCommandBuffersJSON(&buf, out); err != nil {
		t.Fatalf("writeCommandBuffersJSON: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("JSON output missing trailing newline: %q", buf.String())
	}

	var got []commandBufferJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output did not decode: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Index != 1 || len(got[0].Encoders) != 1 {
		t.Fatalf("decoded command buffers = %+v", got)
	}
}

func TestRunCommandBuffersJSONUsesCommandOutput(t *testing.T) {
	tracePath := testCommandBuffersTracePath(t)
	restoreCommandBuffersGlobals(t)
	cmdBuffersJSON = true

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	stdout, err := captureStdout(t, func() error {
		return runCommandBuffers(command, []string{tracePath})
	})
	if err != nil {
		t.Fatalf("runCommandBuffers: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}

	var got []commandBufferJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("command output is invalid JSON: %v\n%s", err, out.String())
	}
	if len(got) == 0 {
		t.Fatalf("JSON command buffers = 0, want at least 1:\n%s", out.String())
	}
}

func testCommandBuffersTracePath(t *testing.T) string {
	t.Helper()

	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}
	return tracePath
}

func restoreCommandBuffersGlobals(t *testing.T) {
	t.Helper()

	oldVerbose := cmdBuffersVerbose
	oldDetailed := cmdBuffersDetailed
	oldJSON := cmdBuffersJSON
	t.Cleanup(func() {
		cmdBuffersVerbose = oldVerbose
		cmdBuffersDetailed = oldDetailed
		cmdBuffersJSON = oldJSON
	})

	cmdBuffersVerbose = false
	cmdBuffersDetailed = false
	cmdBuffersJSON = false
}
