package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRejectXcodeProfileJSON(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})

	collectProfileJSON = false
	if err := rejectXcodeProfileJSON("debug-file-browser"); err != nil {
		t.Fatalf("rejectXcodeProfileJSON plain mode = %v, want nil", err)
	}

	collectProfileJSON = true
	out, err := captureStdout(t, func() error {
		return rejectXcodeProfileJSON("debug-file-browser")
	})
	if err == nil {
		t.Fatal("rejectXcodeProfileJSON JSON mode returned nil error")
	}
	if !strings.Contains(err.Error(), "debug-file-browser does not support --json") {
		t.Fatalf("error = %q, want unsupported JSON context", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestResolveXcodeProfileTraceOutputPathRejectsStdout(t *testing.T) {
	for _, path := range []string{"-", "/dev/stdout"} {
		t.Run(path, func(t *testing.T) {
			_, err := resolveXcodeProfileTraceOutputPath(path)
			if err == nil {
				t.Fatal("resolveXcodeProfileTraceOutputPath returned nil error")
			}
			if !strings.Contains(err.Error(), "not stdout") {
				t.Fatalf("error = %q, want stdout context", err)
			}
		})
	}
}

func TestResolveXcodeProfileTraceOutputPath(t *testing.T) {
	if got, err := resolveXcodeProfileTraceOutputPath(""); err != nil || got != "" {
		t.Fatalf("empty path = %q, %v; want empty nil", got, err)
	}

	got, err := resolveXcodeProfileTraceOutputPath("trace-perfdata.gputrace")
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("resolved path = %q, want absolute path", got)
	}
	if filepath.Base(got) != "trace-perfdata.gputrace" {
		t.Fatalf("resolved path = %q, want basename trace-perfdata.gputrace", got)
	}
}

func TestDefaultXcodeProfileOutputPath(t *testing.T) {
	if got, want := defaultXcodeProfileOutputPath("/tmp/trace.gputrace"), "/tmp/trace-perfdata.gputrace"; got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
}
