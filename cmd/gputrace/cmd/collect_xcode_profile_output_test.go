package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestXcodeProfileStatusWriter(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})

	collectProfileJSON = false
	if got := xcodeProfileStatusWriter(); got != os.Stdout {
		t.Fatalf("plain status writer = %v, want stdout", got)
	}

	collectProfileJSON = true
	if got := xcodeProfileStatusWriter(); got != os.Stderr {
		t.Fatalf("JSON status writer = %v, want stderr", got)
	}
}

func TestEncodeXcodeProfileActionJSON(t *testing.T) {
	var buf bytes.Buffer
	err := encodeXcodeProfileJSON(&buf, xcodeProfileActionOutput{
		Success: true,
		Action:  "run",
		Input:   "input.gputrace",
		Output:  "output.gputrace",
	})
	if err != nil {
		t.Fatalf("encodeXcodeProfileJSON failed: %v", err)
	}

	var got xcodeProfileActionOutput
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !got.Success || got.Action != "run" || got.Input != "input.gputrace" || got.Output != "output.gputrace" {
		t.Fatalf("decoded output = %+v", got)
	}
}

func TestWriteXcodeProfileActionOutputJSON(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})
	collectProfileJSON = true

	out, err := captureStdout(t, func() error {
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "xcode-export-memory",
			Target: "trace.gputrace",
		})
	})
	if err != nil {
		t.Fatalf("writeXcodeProfileActionOutput: %v", err)
	}

	var got xcodeProfileActionOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !got.Success || got.Action != "xcode-export-memory" || got.Target != "trace.gputrace" {
		t.Fatalf("decoded output = %+v", got)
	}
}

func TestWriteXcodeProfileActionOutputPlainNoop(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})
	collectProfileJSON = false

	out, err := captureStdout(t, func() error {
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "xcode-export-memory",
			Target: "trace.gputrace",
		})
	})
	if err != nil {
		t.Fatalf("writeXcodeProfileActionOutput: %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}
