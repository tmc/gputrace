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

func TestWriteBufferAccessJSON(t *testing.T) {
	analysis := testBufferAccessAnalysis()

	var out bytes.Buffer
	if err := writeBufferAccessJSON(&out, analysis); err != nil {
		t.Fatalf("writeBufferAccessJSON: %v", err)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("json output does not end with newline: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "  \"buffer_accesses\": {\n") {
		t.Fatalf("json output is not indented:\n%s", got)
	}

	var got gputrace.BufferAccessAnalysis
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json output is invalid: %v\n%s", err, out.String())
	}
	if got.TotalBuffers != 1 || got.SharedBuffers != 1 || !got.AliasingDetected {
		t.Fatalf("json analysis = %+v", got)
	}
	info, ok := got.BufferAccesses[0x1000]
	if !ok || info.AccessCount != 2 {
		t.Fatalf("buffer accesses = %+v", got.BufferAccesses)
	}
}

func TestRunBufferAccessJSONUsesCommandOutput(t *testing.T) {
	restoreBufferAccessGlobals(t)
	bufferAccessJSON = true

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	stdout, err := captureStdout(t, func() error {
		return runBufferAccess(command, []string{testBufferAccessTracePath(t)})
	})
	if err != nil {
		t.Fatalf("runBufferAccess: %v", err)
	}
	if stdout != "" {
		t.Fatalf("os stdout = %q, want empty", stdout)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("command output missing trailing newline: %q", got)
	}

	var got gputrace.BufferAccessAnalysis
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("command output is invalid json: %v\n%s", err, out.String())
	}
	if got.BufferAccesses == nil || got.EncoderAccesses == nil {
		t.Fatalf("json analysis maps were nil: %+v", got)
	}
}

func testBufferAccessAnalysis() *gputrace.BufferAccessAnalysis {
	return &gputrace.BufferAccessAnalysis{
		BufferAccesses: map[uint64]*gputrace.BufferAccessInfo{
			0x1000: {
				Address:     0x1000,
				AccessCount: 2,
				EncoderIDs:  []int{1, 2},
				FirstAccess: 4,
				LastAccess:  8,
				IsShared:    true,
			},
		},
		EncoderAccesses: map[int]*gputrace.EncoderAccessInfo{
			1: {
				EncoderID:     1,
				BufferCount:   1,
				UniqueBuffers: []uint64{0x1000},
				RecordIndices: []int{4},
			},
		},
		TotalBuffers:     1,
		SharedBuffers:    1,
		AliasingDetected: true,
		AliasingInstances: []gputrace.BufferAlias{
			{
				Address:  0x1000,
				Encoders: []int{1, 2},
				Indices:  []int{4, 8},
			},
		},
	}
}

func testBufferAccessTracePath(t *testing.T) string {
	t.Helper()

	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}
	return tracePath
}

func restoreBufferAccessGlobals(t *testing.T) {
	t.Helper()

	oldVerbose := bufferAccessVerbose
	oldJSON := bufferAccessJSON
	t.Cleanup(func() {
		bufferAccessVerbose = oldVerbose
		bufferAccessJSON = oldJSON
	})

	bufferAccessVerbose = false
	bufferAccessJSON = false
}
