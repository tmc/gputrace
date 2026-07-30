//go:build darwin

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
