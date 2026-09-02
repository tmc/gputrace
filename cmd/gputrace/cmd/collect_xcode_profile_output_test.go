//go:build darwin

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

func TestXcodeProfileJSONErrorsAreReportedOnceAndReturnError(t *testing.T) {
	tests := []string{
		"check-status",
		"list-windows",
		"performance-show",
		"performance-summary",
	}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			command := &cobra.Command{
				Use:           name,
				SilenceErrors: true,
				SilenceUsage:  true,
				RunE: func(cmd *cobra.Command, args []string) error {
					return outputJSONError("NOT_AVAILABLE", name+" failed", "try again")
				},
			}

			stdout, err := captureStdout(t, command.Execute)
			if err == nil {
				t.Fatal("Execute returned nil error")
			}
			if !ErrorAlreadyReported(err) {
				t.Fatalf("ErrorAlreadyReported(%T) = false, want true", err)
			}

			dec := json.NewDecoder(strings.NewReader(stdout))
			var got JSONError
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("decode JSON error: %v\n%s", err, stdout)
			}
			var extra interface{}
			if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("stdout contains more than one JSON value: %s", stdout)
			}
			if !got.Error || got.Code != "NOT_AVAILABLE" || got.Message != name+" failed" || got.Suggestion != "try again" {
				t.Fatalf("JSON error = %+v", got)
			}
		})
	}
}

func TestOrdinaryErrorIsNotAlreadyReported(t *testing.T) {
	if ErrorAlreadyReported(errors.New("ordinary failure")) {
		t.Fatal("ordinary error reported as already written")
	}
}

func TestXcodeProfileMacgoForwardsChildExitStatus(t *testing.T) {
	config := xcodeProfileMacgoConfig()
	if !config.ForceDirectExecution {
		t.Fatal("ForceDirectExecution = false; LaunchServices would lose the child command exit status")
	}
}

func TestXcodeProfileCommandErrorsReachExecute(t *testing.T) {
	oldPreRunE := collectXcodeProfileCmd.PersistentPreRunE
	oldSilenceErrors := rootCmd.SilenceErrors
	oldSilenceUsage := rootCmd.SilenceUsage
	t.Cleanup(func() {
		collectXcodeProfileCmd.PersistentPreRunE = oldPreRunE
		rootCmd.SilenceErrors = oldSilenceErrors
		rootCmd.SilenceUsage = oldSilenceUsage
		rootCmd.SetArgs(nil)
	})
	collectXcodeProfileCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "wait-profile unbound target",
			args: []string{"xcode-profile", "wait-profile", "trace.gputrace"},
			want: `selected Xcode window is not bound to requested trace "trace.gputrace"`,
		},
		{
			name: "show-performance unavailable verbose",
			args: []string{"xcode-profile", "show-performance", "--verbose"},
			want: "Show Performance button not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, _, err := rootCmd.Find(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			oldRunE := command.RunE
			command.RunE = func(cmd *cobra.Command, args []string) error {
				return errors.New(tt.want)
			}
			defer func() {
				command.RunE = oldRunE
			}()

			rootCmd.SetArgs(tt.args)
			err = rootCmd.Execute()
			if err == nil {
				t.Fatal("Execute returned nil error")
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
			if ErrorAlreadyReported(err) {
				t.Fatal("ordinary command error marked as already reported")
			}
		})
	}
}

func TestXcodeWindowSelectionBinding(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		title     string
		document  string
		wantBound bool
		wantText  string
	}{
		{
			name:      "exact document",
			requested: "/Users/test/trace.gputrace",
			title:     "Summary",
			document:  "/Users/test/trace.gputrace",
			wantBound: true,
			wantText:  "exactly matches",
		},
		{
			name:      "document basename",
			requested: "trace.gputrace",
			title:     "Summary",
			document:  "/Users/test/trace.gputrace",
			wantBound: true,
			wantText:  "trace filename",
		},
		{
			name:      "title basename",
			requested: "/Users/test/trace.gputrace",
			title:     "trace.gputrace — Summary",
			wantBound: true,
			wantText:  "window title",
		},
		{
			name:      "untitled unbound",
			requested: "/Users/test/trace.gputrace",
			title:     "Summary",
			wantBound: false,
			wantText:  "no title or AXDocument match",
		},
		{
			name:      "unspecified target",
			title:     "Summary",
			wantBound: true,
			wantText:  "no trace was requested",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newXcodeWindowSelection(tt.requested, tt.title, tt.document)
			if got.Bound != tt.wantBound {
				t.Fatalf("Bound = %t, want %t: %+v", got.Bound, tt.wantBound, got)
			}
			if !strings.Contains(got.Evidence, tt.wantText) {
				t.Fatalf("Evidence = %q, want %q", got.Evidence, tt.wantText)
			}
		})
	}
}

func TestUnboundCompletionIsNotReportedAsComplete(t *testing.T) {
	output := StatusOutput{
		Status:   "complete",
		Phase:    profilingPhase("complete"),
		Evidence: profilingStatusEvidence("complete"),
	}
	selection := newXcodeWindowSelection(
		"/Users/test/python.gputrace",
		"Summary",
		"/Users/test/go.gputrace",
	)
	applyStatusSelection(&output, selection)

	if output.Status != "unknown" || output.Phase != "unbound" || output.TargetBound {
		t.Fatalf("status output = %+v, want unknown unbound target", output)
	}
	if !strings.Contains(output.Evidence, `refusing to attribute detected "complete"`) {
		t.Fatalf("Evidence = %q, want refusal context", output.Evidence)
	}
	if err := requireBoundSelection(selection); err == nil {
		t.Fatal("requireBoundSelection accepted an unbound requested trace")
	}
}

func TestStatusTextIncludesTargetAndEvidence(t *testing.T) {
	output := StatusOutput{
		Status:           "running",
		Phase:            profilingPhase("running"),
		Evidence:         "AXDocument exactly matches; profiling indicator detected",
		RequestedTrace:   "trace.gputrace",
		SelectedTitle:    "Summary",
		SelectedDocument: "/Users/test/trace.gputrace",
		TargetBound:      true,
	}
	var text strings.Builder
	writeStatusText(&text, output)
	for _, want := range []string{
		"Status: running",
		"Phase: performance profiling running",
		"Requested trace: trace.gputrace",
		"Selected document: /Users/test/trace.gputrace",
		"Selected window: Summary",
		"Target bound: true",
		"Evidence:",
	} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text lacks %q:\n%s", want, text.String())
		}
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

func TestRequireExportedTrace(t *testing.T) {
	dir := t.TempDir()
	if err := requireExportedTrace(dir); err != nil {
		t.Fatalf("existing output: %v", err)
	}

	missing := filepath.Join(dir, "missing.gputrace")
	err := requireExportedTrace(missing)
	if err == nil {
		t.Fatal("missing output returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "output not found at expected location") || !strings.Contains(got, missing) {
		t.Fatalf("error = %q, want missing output path and context", got)
	}
}

func TestExistingExportCandidatesReportsAlternateWithoutMovingIt(t *testing.T) {
	dir := t.TempDir()
	requested := filepath.Join(dir, "requested.gputrace")
	alternate := filepath.Join(dir, "raw-basename.gputrace")
	if err := os.Mkdir(alternate, 0o755); err != nil {
		t.Fatalf("mkdir alternate: %v", err)
	}

	got := existingExportCandidates([]string{requested, alternate, alternate}, requested)
	if len(got) != 1 || got[0] != alternate {
		t.Fatalf("existingExportCandidates = %q, want [%q]", got, alternate)
	}
	if _, err := os.Stat(alternate); err != nil {
		t.Fatalf("alternate output was not preserved: %v", err)
	}
}
