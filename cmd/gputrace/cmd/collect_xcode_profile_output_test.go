//go:build darwin

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestXcodeProfileStatusWriter(t *testing.T) {
	oldJSON := collectProfileOpts.json
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
	})

	collectProfileOpts.json = false
	if got := xcodeProfileStatusWriter(); got != os.Stdout {
		t.Fatalf("plain status writer = %v, want stdout", got)
	}

	collectProfileOpts.json = true
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
	oldJSON := collectProfileOpts.json
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
	})
	collectProfileOpts.json = true

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
	oldJSON := collectProfileOpts.json
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
	})
	collectProfileOpts.json = false

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

func TestHiddenXcodeProfileUtilityCommandsRejectJSONBeforeRunE(t *testing.T) {
	oldJSON := collectProfileOpts.json
	oldPreRunE := collectXcodeProfileCmd.PersistentPreRunE
	oldSilenceUsage := rootCmd.SilenceUsage
	oldSilenceErrors := rootCmd.SilenceErrors
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
		collectXcodeProfileCmd.PersistentPreRunE = oldPreRunE
		rootCmd.SilenceUsage = oldSilenceUsage
		rootCmd.SilenceErrors = oldSilenceErrors
		rootCmd.SetArgs(nil)
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	tests := []struct {
		name string
		args []string
	}{
		{name: "send-key", args: []string{"escape"}},
		{name: "check-goto-folder"},
		{name: "debug-file-browser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, _, err := collectXcodeProfileCmd.Find([]string{tt.name})
			if err != nil {
				t.Fatal(err)
			}
			if command == nil || command.Name() != tt.name {
				t.Fatalf("command = %#v, want %q", command, tt.name)
			}

			preRan := false
			collectXcodeProfileCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
				preRan = true
				return errors.New("persistent pre-run called")
			}

			ran := false
			oldRunE := command.RunE
			command.RunE = func(cmd *cobra.Command, args []string) error {
				ran = true
				return errors.New("runE called")
			}
			defer func() {
				command.RunE = oldRunE
			}()

			collectProfileOpts.json = false
			args := append([]string{"xcode-profile", "--json", tt.name}, tt.args...)
			rootCmd.SetArgs(args)

			err = rootCmd.Execute()
			if err == nil {
				t.Fatal("command returned nil error")
			}
			if got, want := err.Error(), tt.name+" does not support --json"; got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
			if preRan {
				t.Fatal("persistent pre-run ran after JSON rejection")
			}
			if ran {
				t.Fatal("RunE ran after JSON rejection")
			}
		})
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
