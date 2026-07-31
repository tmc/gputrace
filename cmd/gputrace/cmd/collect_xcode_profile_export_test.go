//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func writeStandaloneExportFixture(t *testing.T, name, uuid string, full bool) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), name+".gputrace")
	profilerDir := filepath.Join(bundle, name+".gputrace.gpuprofiler_raw")
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>(uuid)</key><string>` + uuid + `</string></dict></plist>`
	files := map[string]string{
		"metadata": metadata,
		filepath.Join(filepath.Base(profilerDir), "streamData"): "profiler",
	}
	if full {
		files["capture"] = "capture"
		files["MTLBuffer-1-0"] = "raw resource"
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(bundle, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return bundle
}

func TestFinalizeStandaloneExportRequiresBoundIdentity(t *testing.T) {
	output := writeStandaloneExportFixture(t, "output", "same", true)
	var status bytes.Buffer
	_, err := finalizeStandaloneExport(&status, "", output)
	if err == nil || !strings.Contains(err.Error(), "no AXDocument binding") {
		t.Fatalf("error = %v, want unbound identity error", err)
	}
	if strings.Contains(status.String(), "Exported to:") {
		t.Fatalf("unbound export printed success:\n%s", status.String())
	}
}

func TestFinalizeStandaloneExportRejectsAndPreservesProfilerOnly(t *testing.T) {
	input := writeStandaloneExportFixture(t, "input", "same", true)
	output := writeStandaloneExportFixture(t, "output", "same", false)
	var status bytes.Buffer
	payload, err := finalizeStandaloneExport(&status, input, output)
	if err == nil || !strings.Contains(err.Error(), "not self-contained") {
		t.Fatalf("error = %v, want self-contained rejection", err)
	}
	if payload.Class != "profiler-only" || !payload.HasProfilerStream {
		t.Fatalf("payload = %+v, want usable profiler-only", payload)
	}
	if !strings.Contains(status.String(), "profiler-only (not self-contained)") {
		t.Fatalf("status missing payload classification:\n%s", status.String())
	}
	if strings.Contains(status.String(), "Exported to:") {
		t.Fatalf("rejected export printed success:\n%s", status.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("rejected profiler-only output was not preserved: %v", err)
	}
}

func TestFinalizeStandaloneExportAcceptsFullPayloadFields(t *testing.T) {
	input := writeStandaloneExportFixture(t, "input", "same", true)
	output := writeStandaloneExportFixture(t, "output", "same", true)
	var status bytes.Buffer
	payload, err := finalizeStandaloneExport(&status, input, output)
	if err != nil {
		t.Fatalf("finalizeStandaloneExport: %v", err)
	}
	if !strings.Contains(status.String(), "full and self-contained") {
		t.Fatalf("status missing full classification:\n%s", status.String())
	}

	action := xcodeProfileActionOutput{Action: "export", Target: input, Output: output}
	applyXcodePayload(&action, payload)
	if action.PayloadClass != "full" ||
		action.SelfContained == nil || !*action.SelfContained ||
		action.ProfilerTimingAvailable == nil || !*action.ProfilerTimingAvailable ||
		action.StructuralAnalysisAvailable == nil || !*action.StructuralAnalysisAvailable {
		t.Fatalf("payload action fields = %+v", action)
	}
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"payload_class":"full"`,
		`"self_contained":true`,
		`"profiler_timing_available":true`,
		`"structural_analysis_available":true`,
	} {
		if !bytes.Contains(data, []byte(field)) {
			t.Fatalf("action JSON missing %s: %s", field, data)
		}
	}
}

func TestStandaloneExportTargetRequiresUniqueDocumentBinding(t *testing.T) {
	trace := "/Users/tmc/tmp/trace.gputrace"
	window, doc, err := standaloneExportTarget([]xcodeAXWindow{
		{Element: 1, Title: "Source", Document: "/Users/tmc/project/main.swift"},
		{Element: 2, Title: "Performance", Document: trace},
	})
	if err != nil {
		t.Fatal(err)
	}
	if window != 2 || doc != trace {
		t.Fatalf("target = (%d, %q)", window, doc)
	}

	_, _, err = standaloneExportTarget([]xcodeAXWindow{
		{Element: 2, Document: trace},
		{Element: 3, Document: "/Users/tmc/tmp/other.gputrace"},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple .gputrace windows") {
		t.Fatalf("ambiguous target error = %v", err)
	}
}

func TestStandaloneExportRecoveryFlagsRequireCompleteIdentity(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "mode only", args: []string{"--recover-untitled"}},
		{name: "check only", args: []string{"--check-recovery"}},
		{name: "finalize only", args: []string{"--finalize-workload"}},
		{name: "source only", args: []string{"--source", "/trace.gputrace"}},
		{name: "missing app", args: []string{"--recover-untitled", "--source", "/trace.gputrace", "--xcode-pid", "81051"}},
		{name: "missing pid", args: []string{"--recover-untitled", "--source", "/trace.gputrace", "--xcode-app", "/Applications/Xcode.app"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			standaloneExportFlags(cmd)
			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}
			_, err := standaloneExportRecoveryFromFlags(cmd)
			if err == nil || !strings.Contains(err.Error(), "requires --recover-untitled") {
				t.Fatalf("error = %v, want incomplete recovery flags", err)
			}
		})
	}
}

func TestStandaloneExportRecoveryFlagsRejectCheckAndFinalize(t *testing.T) {
	cmd := &cobra.Command{}
	standaloneExportFlags(cmd)
	err := cmd.ParseFlags([]string{
		"--recover-untitled",
		"--check-recovery",
		"--finalize-workload",
		"--source", "/trace.gputrace",
		"--xcode-pid", "81051",
		"--xcode-app", "/Applications/Xcode.app",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = standaloneExportRecoveryFromFlags(cmd)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive flags", err)
	}
}

func TestStandaloneExportRecoveryFlagsPreserveDeadPIDForSentinel(t *testing.T) {
	source := writeStandaloneExportFixture(t, "source", "SOURCE-UUID", true)
	cmd := &cobra.Command{}
	standaloneExportFlags(cmd)
	err := cmd.ParseFlags([]string{
		"--recover-untitled",
		"--source", source,
		"--xcode-pid", "987654",
		"--xcode-app", "/Applications/Xcode.app",
	})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := standaloneExportRecoveryFromFlags(cmd)
	if err != nil {
		t.Fatalf("parse recovery flags: %v", err)
	}
	if recovery.Identity.PID != 987654 || recovery.Identity.AppPath != "/Applications/Xcode.app" {
		t.Fatalf("identity = %+v", recovery.Identity)
	}
}

func TestStandaloneRecoveryTarget(t *testing.T) {
	recovery := standaloneExportRecovery{
		Enabled:    true,
		SourcePath: "/Users/tmc/tmp/raw.gputrace",
		SourceUUID: "RAW-UUID",
		Identity: xcodeProcessIdentity{
			PID:     81051,
			AppPath: "/Applications/Xcode.app",
		},
	}
	eligible := standaloneRecoveryWindow{
		xcodeAXWindow:   xcodeAXWindow{Element: 11},
		PID:             81051,
		PerformanceView: true,
	}
	tests := []struct {
		name    string
		windows []standaloneRecoveryWindow
		want    uintptr
		wantErr string
	}{
		{name: "unique", windows: []standaloneRecoveryWindow{eligible}, want: 11},
		{name: "duplicate AX representation", windows: []standaloneRecoveryWindow{eligible, eligible}, want: 11},
		{name: "none", wantErr: "no untitled Performance window"},
		{
			name: "wrong pid",
			windows: []standaloneRecoveryWindow{{
				xcodeAXWindow:   xcodeAXWindow{Element: 12},
				PID:             74001,
				PerformanceView: true,
			}},
			wantErr: "no untitled Performance window",
		},
		{
			name: "document bound",
			windows: []standaloneRecoveryWindow{{
				xcodeAXWindow:   xcodeAXWindow{Element: 13, Document: "/Users/tmc/tmp/other.gputrace"},
				PID:             81051,
				PerformanceView: true,
			}},
			wantErr: "no untitled Performance window",
		},
		{
			name: "titled",
			windows: []standaloneRecoveryWindow{{
				xcodeAXWindow:   xcodeAXWindow{Element: 14, Title: "Other"},
				PID:             81051,
				PerformanceView: true,
			}},
			wantErr: "no untitled Performance window",
		},
		{
			name: "no performance evidence",
			windows: []standaloneRecoveryWindow{{
				xcodeAXWindow: xcodeAXWindow{Element: 15},
				PID:           81051,
			}},
			wantErr: "no untitled Performance window",
		},
		{
			name: "ambiguous",
			windows: []standaloneRecoveryWindow{
				eligible,
				{
					xcodeAXWindow:   xcodeAXWindow{Element: 16},
					PID:             81051,
					PerformanceView: true,
				},
			},
			wantErr: "multiple untitled Performance windows",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := standaloneRecoveryTarget(test.windows, recovery)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Element != test.want {
				t.Fatalf("window = %d, want %d", got.Element, test.want)
			}
		})
	}
}

func TestValidateStandaloneRecoveryIdentity(t *testing.T) {
	identity := xcodeProcessIdentity{PID: 81051, AppPath: "/Applications/Xcode.app"}
	if err := validateStandaloneRecoveryIdentity(identity, "/Applications/Xcode.app"); err != nil {
		t.Fatal(err)
	}
	err := validateStandaloneRecoveryIdentity(identity, "/Applications/Xcode-rc.app")
	if err == nil || !strings.Contains(err.Error(), "not requested app") {
		t.Fatalf("error = %v, want cross-app rejection", err)
	}
	for _, actual := range []string{"", " \t"} {
		err := validateStandaloneRecoveryIdentity(identity, actual)
		if err == nil || !strings.Contains(err.Error(), "PID 81051 is not running") {
			t.Fatalf("validate dead PID with %q: %v", actual, err)
		}
		if strings.Contains(err.Error(), "runs from .") {
			t.Fatalf("dead PID rendered cleaned empty path: %v", err)
		}
	}
}

func TestNormalizeStandaloneRecoveryFailureReportsExitWithoutIPS(t *testing.T) {
	recovery := standaloneExportRecovery{
		Identity: xcodeProcessIdentity{PID: 987654, AppPath: "/Applications/Xcode.app"},
	}
	scope := newXcodeCrashScope(recovery.Identity.AppPath, time.Now())
	scope.allowRebind = false
	scope.bind(recovery.Identity)
	original := fmt.Errorf("reacquire recovery Xcode: process not running")
	err := normalizeStandaloneRecoveryFailureWithGrace(
		context.Background(), scope, recovery, original, time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "PID 987654 exited") ||
		!strings.Contains(err.Error(), "no matching DiagnosticReport") {
		t.Fatalf("error = %v, want explicit exit without report", err)
	}
	if !errors.Is(err, original) {
		t.Fatalf("error does not wrap original: %v", err)
	}
}

func TestNormalizeStandaloneRecoveryFailureReturnsCrashReport(t *testing.T) {
	recovery := standaloneExportRecovery{
		Identity: xcodeProcessIdentity{PID: 987654, AppPath: "/Applications/Xcode.app"},
	}
	scope := newXcodeCrashScope(recovery.Identity.AppPath, time.Now())
	scope.allowRebind = false
	scope.bind(recovery.Identity)
	report := xcodeCrashReport{
		Path:      "/Users/tmc/Library/Logs/DiagnosticReports/Xcode.ips",
		PID:       recovery.Identity.PID,
		AppPath:   recovery.Identity.AppPath,
		Exception: "EXC_BAD_ACCESS",
		Signal:    "SIGBUS",
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(report)
	err := normalizeStandaloneRecoveryFailureWithGrace(
		ctx, scope, recovery, fmt.Errorf("window disappeared"), time.Second,
	)
	var got xcodeCrashReport
	if !errors.As(err, &got) || got.Path != report.Path {
		t.Fatalf("error = %T %v, want crash report", err, err)
	}
}

func TestFindElementAtDepth(t *testing.T) {
	tests := []struct {
		name     string
		tree     map[uintptr][]uintptr
		pruned   map[uintptr]bool
		target   uintptr
		maxDepth int
		want     uintptr
	}{
		{
			name:     "root",
			target:   1,
			maxDepth: 4,
			want:     1,
		},
		{
			name: "depth four",
			tree: map[uintptr][]uintptr{
				1: {2},
				2: {3},
				3: {4},
				4: {5},
			},
			target:   5,
			maxDepth: 4,
			want:     5,
		},
		{
			name: "reject depth five",
			tree: map[uintptr][]uintptr{
				1: {2},
				2: {3},
				3: {4},
				4: {5},
				5: {6},
			},
			target:   6,
			maxDepth: 4,
		},
		{
			name: "prune outline",
			tree: map[uintptr][]uintptr{
				1: {2},
				2: {3},
			},
			pruned:   map[uintptr]bool{2: true},
			target:   3,
			maxDepth: 4,
		},
		{
			name: "cycle",
			tree: map[uintptr][]uintptr{
				1: {2},
				2: {1, 3},
			},
			target:   3,
			maxDepth: 4,
			want:     3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			childCalls := make(map[uintptr]int)
			got := findElementAtDepth(
				1,
				test.maxDepth,
				32,
				func(element uintptr) []uintptr {
					childCalls[element]++
					return test.tree[element]
				},
				func(element uintptr) bool {
					return test.pruned[element]
				},
				func(element uintptr) bool {
					return element == test.target
				},
			)
			if got != test.want {
				t.Fatalf("element = %d, want %d", got, test.want)
			}
			for element := range test.pruned {
				if childCalls[element] != 0 {
					t.Fatalf("children called for pruned element %d", element)
				}
			}
		})
	}
}

func TestStandaloneRecoveryWindowKeyIgnoresAXHandle(t *testing.T) {
	left := standaloneRecoveryWindow{
		xcodeAXWindow: xcodeAXWindow{Element: 11, X: 229, Y: 320, Width: 1376, Height: 900},
		PID:           81051,
	}
	right := left
	right.Element = 22
	if standaloneRecoveryWindowKey(left) != standaloneRecoveryWindowKey(right) {
		t.Fatal("logical window key depends on transient AX element handle")
	}
}

func TestValidateRecoveryFinalizePrecondition(t *testing.T) {
	recovery := standaloneExportRecovery{
		Identity: xcodeProcessIdentity{PID: 81051, AppPath: "/Applications/Xcode.app"},
	}
	const key = "window"
	valid := recoveryFinalizeSnapshot{
		Identity:      recovery.Identity,
		WindowKey:     key,
		Performance:   true,
		StopCount:     1,
		StopEnabled:   true,
		StopElement:   7,
		ExportFound:   true,
		ExportEnabled: false,
	}
	tests := []struct {
		name string
		edit func(*recoveryFinalizeSnapshot)
		want string
	}{
		{name: "valid"},
		{name: "wrong pid", edit: func(s *recoveryFinalizeSnapshot) { s.Identity.PID++ }, want: "identity mismatch"},
		{name: "wrong app", edit: func(s *recoveryFinalizeSnapshot) { s.Identity.AppPath = "/Applications/Xcode-rc.app" }, want: "identity mismatch"},
		{name: "window changed", edit: func(s *recoveryFinalizeSnapshot) { s.WindowKey = "other" }, want: "window identity changed"},
		{name: "performance missing", edit: func(s *recoveryFinalizeSnapshot) { s.Performance = false }, want: "Performance group"},
		{name: "sheet open", edit: func(s *recoveryFinalizeSnapshot) { s.SheetOpen = true }, want: "open sheet"},
		{name: "stop absent", edit: func(s *recoveryFinalizeSnapshot) { s.StopCount = 0 }, want: "exactly one enabled"},
		{name: "stop duplicate", edit: func(s *recoveryFinalizeSnapshot) { s.StopCount = 2 }, want: "exactly one enabled"},
		{name: "stop disabled", edit: func(s *recoveryFinalizeSnapshot) { s.StopEnabled = false }, want: "exactly one enabled"},
		{name: "export missing", edit: func(s *recoveryFinalizeSnapshot) { s.ExportFound = false }, want: "could not find"},
		{name: "already ready", edit: func(s *recoveryFinalizeSnapshot) { s.ExportEnabled = true }, want: "already export-ready"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			if test.edit != nil {
				test.edit(&snapshot)
			}
			err := validateRecoveryFinalizePrecondition(snapshot, recovery, key)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRestoredRecoverySourceTarget(t *testing.T) {
	recovery := standaloneExportRecovery{
		SourcePath: "/Users/tmc/tmp/raw.gputrace",
		Identity:   xcodeProcessIdentity{PID: 81051, AppPath: "/Applications/Xcode.app"},
	}
	base := standaloneRecoveryWindow{
		xcodeAXWindow: xcodeAXWindow{
			Element:  21,
			Title:    "raw.gputrace",
			Document: "file:///Users/tmc/tmp/raw.gputrace",
			X:        229,
			Y:        320,
			Width:    1376,
			Height:   900,
		},
		PID:           81051,
		NewEditorView: true,
		Finished:      true,
	}
	key := standaloneRecoveryGeometryKey(base)
	tests := []struct {
		name    string
		edit    func(*standaloneRecoveryWindow)
		wantErr string
	}{
		{name: "exact restored source"},
		{name: "wrong pid", edit: func(w *standaloneRecoveryWindow) { w.PID++ }, wantErr: "found 0"},
		{name: "window drift", edit: func(w *standaloneRecoveryWindow) { w.X++ }, wantErr: "found 0"},
		{name: "wrong document", edit: func(w *standaloneRecoveryWindow) { w.Document = "/Users/tmc/tmp/other.gputrace" }, wantErr: "found 0"},
		{name: "empty document", edit: func(w *standaloneRecoveryWindow) { w.Document = "" }, wantErr: "found 0"},
		{name: "wrong title", edit: func(w *standaloneRecoveryWindow) { w.Title = "other.gputrace" }, wantErr: "found 0"},
		{name: "new editor missing", edit: func(w *standaloneRecoveryWindow) { w.NewEditorView = false }, wantErr: "found 0"},
		{name: "finished missing", edit: func(w *standaloneRecoveryWindow) { w.Finished = false }, wantErr: "found 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := base
			if test.edit != nil {
				test.edit(&window)
			}
			got, err := restoredRecoverySourceTarget([]standaloneRecoveryWindow{window}, recovery, key)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Element != base.Element {
				t.Fatalf("element = %d, want %d", got.Element, base.Element)
			}
		})
	}
}

func TestSummaryRecoveryTarget(t *testing.T) {
	recovery := standaloneExportRecovery{
		SourcePath: "/Users/tmc/tmp/raw.gputrace",
		Identity:   xcodeProcessIdentity{PID: 13556, AppPath: "/Applications/Xcode.app"},
	}
	base := standaloneRecoveryWindow{
		xcodeAXWindow: xcodeAXWindow{
			Element: 31,
			X:       0,
			Y:       100,
			Width:   1376,
			Height:  900,
		},
		PID:         13556,
		SummaryView: true,
		Debugging:   true,
		Progress95:  true,
		StopCount:   1,
		StopEnabled: true,
		ShowCount:   1,
		ShowEnabled: true,
	}
	key := standaloneRecoveryGeometryKey(base)
	tests := []struct {
		name    string
		edit    func(*standaloneRecoveryWindow)
		wantErr string
	}{
		{name: "exact summary"},
		{name: "wrong pid", edit: func(w *standaloneRecoveryWindow) { w.PID++ }, wantErr: "found 0"},
		{name: "wrong geometry", edit: func(w *standaloneRecoveryWindow) { w.X++ }, wantErr: "found 0"},
		{name: "titled", edit: func(w *standaloneRecoveryWindow) { w.Title = "other.gputrace" }, wantErr: "found 0"},
		{name: "document bound", edit: func(w *standaloneRecoveryWindow) { w.Document = recovery.SourcePath }, wantErr: "found 0"},
		{name: "summary missing", edit: func(w *standaloneRecoveryWindow) { w.SummaryView = false }, wantErr: "found 0"},
		{name: "debugging missing", edit: func(w *standaloneRecoveryWindow) { w.Debugging = false }, wantErr: "found 0"},
		{name: "wrong progress", edit: func(w *standaloneRecoveryWindow) { w.Progress95 = false }, wantErr: "found 0"},
		{name: "sheet", edit: func(w *standaloneRecoveryWindow) { w.SheetOpen = true }, wantErr: "found 0"},
		{name: "stop absent", edit: func(w *standaloneRecoveryWindow) { w.StopCount = 0 }, wantErr: "found 0"},
		{name: "stop disabled", edit: func(w *standaloneRecoveryWindow) { w.StopEnabled = false }, wantErr: "found 0"},
		{name: "AX show absent uses OCR", edit: func(w *standaloneRecoveryWindow) { w.ShowCount = 0; w.ShowEnabled = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := base
			if test.edit != nil {
				test.edit(&window)
			}
			got, err := summaryRecoveryTarget([]standaloneRecoveryWindow{window}, recovery, key)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Element != base.Element {
				t.Fatalf("element = %d, want %d", got.Element, base.Element)
			}
		})
	}

	duplicate := base
	duplicate.Element = 32
	if _, err := summaryRecoveryTarget([]standaloneRecoveryWindow{base, duplicate}, recovery, key); err != nil {
		t.Fatalf("duplicate AX representation: %v", err)
	}
	other := base
	other.Element = 33
	other.X++
	if _, err := summaryRecoveryTarget([]standaloneRecoveryWindow{base, other}, recovery, ""); err == nil {
		t.Fatal("distinct Summary windows were not rejected")
	}
}

func TestRunningRecoveryPerformanceTarget(t *testing.T) {
	recovery := standaloneExportRecovery{
		SourcePath: "/Users/tmc/tmp/raw.gputrace",
		Identity:   xcodeProcessIdentity{PID: 13556, AppPath: "/Applications/Xcode.app"},
	}
	base := standaloneRecoveryWindow{
		xcodeAXWindow:   xcodeAXWindow{Element: 41, X: 0, Y: 100, Width: 1376, Height: 900},
		PID:             13556,
		PerformanceView: true,
		StopCount:       1,
		StopEnabled:     true,
	}
	key := standaloneRecoveryGeometryKey(base)
	if _, err := runningRecoveryPerformanceTarget([]standaloneRecoveryWindow{base}, recovery, key); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*standaloneRecoveryWindow){
		func(w *standaloneRecoveryWindow) { w.PID++ },
		func(w *standaloneRecoveryWindow) { w.Width++ },
		func(w *standaloneRecoveryWindow) { w.PerformanceView = false },
		func(w *standaloneRecoveryWindow) { w.SheetOpen = true },
		func(w *standaloneRecoveryWindow) { w.StopCount = 0 },
		func(w *standaloneRecoveryWindow) { w.StopEnabled = false },
		func(w *standaloneRecoveryWindow) { w.Document = "/Users/tmc/tmp/other.gputrace" },
	} {
		window := base
		edit(&window)
		if _, err := runningRecoveryPerformanceTarget([]standaloneRecoveryWindow{window}, recovery, key); err == nil {
			t.Fatalf("invalid running Performance accepted: %+v", window)
		}
	}
}

func TestFinalizedRecoveryPerformanceTarget(t *testing.T) {
	recovery := standaloneExportRecovery{
		SourcePath: "/Users/tmc/tmp/raw.gputrace",
		Identity:   xcodeProcessIdentity{PID: 81051, AppPath: "/Applications/Xcode.app"},
	}
	base := standaloneRecoveryWindow{
		xcodeAXWindow: xcodeAXWindow{
			Element: 22,
			X:       229,
			Y:       320,
			Width:   1376,
			Height:  900,
		},
		PID:             81051,
		PerformanceView: true,
	}
	key := standaloneRecoveryGeometryKey(base)
	tests := []struct {
		name    string
		edit    func(*standaloneRecoveryWindow)
		wantErr string
	}{
		{name: "untitled transitioned performance"},
		{name: "source-bound transitioned performance", edit: func(w *standaloneRecoveryWindow) {
			w.Title = "raw.gputrace"
			w.Document = "file:///Users/tmc/tmp/raw.gputrace"
		}},
		{name: "wrong pid", edit: func(w *standaloneRecoveryWindow) { w.PID++ }, wantErr: "found 0"},
		{name: "window drift", edit: func(w *standaloneRecoveryWindow) { w.Width++ }, wantErr: "found 0"},
		{name: "performance missing", edit: func(w *standaloneRecoveryWindow) { w.PerformanceView = false }, wantErr: "found 0"},
		{name: "wrong document", edit: func(w *standaloneRecoveryWindow) { w.Document = "/Users/tmc/tmp/other.gputrace" }, wantErr: "found 0"},
		{name: "wrong title", edit: func(w *standaloneRecoveryWindow) { w.Title = "other.gputrace" }, wantErr: "found 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := base
			if test.edit != nil {
				test.edit(&window)
			}
			got, err := finalizedRecoveryPerformanceTarget([]standaloneRecoveryWindow{window}, recovery, key)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Element != base.Element {
				t.Fatalf("element = %d, want %d", got.Element, base.Element)
			}
		})
	}
}

func TestNormalizedTraceDocument(t *testing.T) {
	const want = "/Users/tmc/tmp/raw trace.gputrace"
	for _, document := range []string{
		want,
		"file:///Users/tmc/tmp/raw%20trace.gputrace",
	} {
		if got := normalizedTraceDocument(document); got != want {
			t.Fatalf("normalizedTraceDocument(%q) = %q, want %q", document, got, want)
		}
	}
}

func TestTraceDocumentMatchesFilesystemIdentity(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "raw trace.gputrace")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "real"), aliasRoot); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(aliasRoot, "raw trace.gputrace")
	fileURL := "file://" + strings.ReplaceAll(real, " ", "%20")
	for _, test := range []struct {
		name     string
		document string
		source   string
		want     bool
	}{
		{name: "alias to real", document: alias, source: real, want: true},
		{name: "real to alias", document: real, source: alias, want: true},
		{name: "escaped file URL", document: fileURL, source: alias, want: true},
		{name: "empty", document: "", source: real},
		{name: "non-file URL", document: "https://example.com/raw.gputrace", source: real},
		{name: "missing unequal", document: filepath.Join(root, "a", "raw.gputrace"), source: filepath.Join(root, "b", "raw.gputrace")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := traceDocumentMatches(test.document, test.source); got != test.want {
				t.Fatalf("traceDocumentMatches(%q, %q) = %t, want %t",
					test.document, test.source, got, test.want)
			}
		})
	}
}

func TestFinalizeStandaloneExportRejectsUUIDMismatchAndPreservesOutput(t *testing.T) {
	input := writeStandaloneExportFixture(t, "input", "wanted", true)
	output := writeStandaloneExportFixture(t, "output", "other", true)
	var status bytes.Buffer
	_, err := finalizeStandaloneExport(&status, input, output)
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("error = %v, want UUID mismatch", err)
	}
	if strings.Contains(status.String(), "Exported to:") {
		t.Fatalf("mismatched export printed success:\n%s", status.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("mismatched output was not preserved: %v", err)
	}
}
