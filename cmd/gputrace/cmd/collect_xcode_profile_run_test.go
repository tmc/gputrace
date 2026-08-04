//go:build darwin

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/tracebundle"
)

func writeProfiledTraceBundle(t *testing.T) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "trace-perfdata.gputrace")
	profilerDir := filepath.Join(bundle, "trace.gputrace.gpuprofiler_raw")
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "capture"), []byte("MTSP capture data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilerDir, "streamData"), []byte("profiler data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestRunCollectXcodeProfileReusesEmbeddedPerformanceData(t *testing.T) {
	oldJSON := collectProfileOpts.json
	oldOutput := collectProfileOpts.output
	oldHook := xcodeProfileAutomationStartHook
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
		collectProfileOpts.output = oldOutput
		xcodeProfileAutomationStartHook = oldHook
	})

	bundle := writeProfiledTraceBundle(t)
	automationStarted := false
	xcodeProfileAutomationStartHook = func() {
		automationStarted = true
	}
	collectProfileOpts.json = false
	collectProfileOpts.output = ""

	stdout, err := captureStdout(t, func() error {
		return runCollectXcodeProfileFull(&cobra.Command{}, []string{bundle})
	})
	if err != nil {
		t.Fatalf("runCollectXcodeProfileFull: %v", err)
	}
	if automationStarted {
		t.Fatal("Xcode automation started for a profiled trace")
	}
	if !strings.Contains(stdout, "Performance data already embedded; verified non-empty streamData.") {
		t.Fatalf("stdout lacks verification result:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Using existing trace: "+bundle) {
		t.Fatalf("stdout lacks reused path:\n%s", stdout)
	}
}

func TestRunCollectXcodeProfileReusedJSON(t *testing.T) {
	oldJSON := collectProfileOpts.json
	oldOutput := collectProfileOpts.output
	oldHook := xcodeProfileAutomationStartHook
	t.Cleanup(func() {
		collectProfileOpts.json = oldJSON
		collectProfileOpts.output = oldOutput
		xcodeProfileAutomationStartHook = oldHook
	})

	bundle := writeProfiledTraceBundle(t)
	xcodeProfileAutomationStartHook = func() {
		t.Fatal("Xcode automation started for a profiled trace")
	}
	collectProfileOpts.json = true
	collectProfileOpts.output = ""

	stdout, err := captureStdout(t, func() error {
		return runCollectXcodeProfileFull(&cobra.Command{}, []string{bundle})
	})
	if err != nil {
		t.Fatalf("runCollectXcodeProfileFull: %v", err)
	}
	var got xcodeProfileActionOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if !got.Success || !got.Reused || got.Action != "run" || got.Input != bundle || got.Output != bundle {
		t.Fatalf("JSON output = %+v", got)
	}
}

func TestWaitForExportedTraceRequiresProfilerData(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "trace-perfdata.gputrace")
	if err := os.Mkdir(bundle, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := waitForExportedTrace(context.Background(), []string{bundle}, 0)
	if err == nil {
		t.Fatal("waitForExportedTrace succeeded without profiler data")
	}
	if !strings.Contains(err.Error(), "without .gpuprofiler_raw") {
		t.Fatalf("error = %q, want missing profiler data", err)
	}

	profilerDir := filepath.Join(bundle, "trace.gputrace.gpuprofiler_raw")
	if err := os.Mkdir(profilerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilerDir, "streamData"), []byte("stream"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := waitForExportedTrace(context.Background(), []string{bundle}, time.Second)
	if err != nil {
		t.Fatalf("waitForExportedTrace failed: %v", err)
	}
	if got != bundle {
		t.Fatalf("path = %q, want %q", got, bundle)
	}
}

func TestWaitForExportedTraceRejectsEmptyStreamData(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "trace-perfdata.gputrace")
	profilerDir := filepath.Join(bundle, "trace.gputrace.gpuprofiler_raw")
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilerDir, "streamData"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := waitForExportedTrace(context.Background(), []string{bundle}, 0)
	if err == nil || !strings.Contains(err.Error(), "non-empty streamData") {
		t.Fatalf("error = %v, want incomplete streamData error", err)
	}
}

func TestWaitForExportedTraceStopsOnCancellation(t *testing.T) {
	want := errors.New("stop export wait")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(want)

	_, err := waitForExportedTrace(ctx, []string{t.TempDir()}, time.Hour)
	if !errors.Is(err, want) {
		t.Fatalf("waitForExportedTrace error = %v, want %v", err, want)
	}
}

func TestWaitForExportedTraceDeduplicatesSymlinkAliases(t *testing.T) {
	physicalRoot := t.TempDir()
	bundle := filepath.Join(physicalRoot, "trace-perfdata.gputrace")
	profilerDir := filepath.Join(bundle, "trace.gputrace.gpuprofiler_raw")
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilerDir, "streamData"), []byte("stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "capture"), []byte("capture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "MTLBuffer-1-0"), []byte("raw resource"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>(uuid)</key><string>same</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "metadata"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	aliasRoot := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(physicalRoot, aliasRoot); err != nil {
		t.Fatal(err)
	}
	requested := filepath.Join(aliasRoot, filepath.Base(bundle))

	scans := 0
	readSignature := func(path, profilerDir string) (exportTraceSignature, error) {
		scans++
		return readExportTraceSignature(path, profilerDir)
	}
	got, err := waitForExportedTraceWithReader(
		context.Background(),
		[]string{requested, bundle},
		time.Second,
		readSignature,
	)
	if err != nil {
		t.Fatalf("waitForExportedTraceWithReader: %v", err)
	}
	if got != requested {
		t.Fatalf("path = %q, want requested spelling %q", got, requested)
	}
	if scans != 3 {
		t.Fatalf("signature scans = %d, want 3 for one physical bundle", scans)
	}

	input := filepath.Join(t.TempDir(), "input.gputrace")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "metadata"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyExportTraceIdentity(input, got); err != nil {
		t.Fatalf("identity gate after stable alias: %v", err)
	}
	payload, err := tracebundle.InspectPayload(got)
	if err != nil {
		t.Fatalf("inspect payload after stable alias: %v", err)
	}
	if err := requireSelfContainedExport(got, payload); err != nil {
		t.Fatalf("payload gate after stable alias: %v", err)
	}
}

func TestTargetedShowPerformanceFoundSentinel(t *testing.T) {
	if targetedShowPerformanceFound == 0 {
		t.Fatal("targetedShowPerformanceFound must be non-zero")
	}
	if !isTargetedShowPerformanceFound(targetedShowPerformanceFound) {
		t.Fatal("targeted Show Performance sentinel not recognized")
	}
	if isTargetedShowPerformanceFound(0) {
		t.Fatal("zero should not be recognized as targeted Show Performance sentinel")
	}
}

func TestGPUTraceStateButtonsIncludeRunningState(t *testing.T) {
	for _, name := range gpuTraceStateButtonNames() {
		if name == "Stop GPU workload" {
			return
		}
	}
	t.Fatal("GPU trace state buttons do not include Stop GPU workload")
}

func TestDuplicateAXWindowsProduceOneExactTraceMatch(t *testing.T) {
	const tracePath = "/Users/test/trace.gputrace"
	windows := []xcodeAXWindow{
		{
			Element:  100,
			Title:    "Summary",
			Document: tracePath,
			X:        20,
			Y:        30,
			Width:    1200,
			Height:   800,
		},
		// Same AX element returned twice.
		{
			Element:  100,
			Title:    "Summary",
			Document: tracePath,
			X:        20,
			Y:        30,
			Width:    1200,
			Height:   800,
		},
		// A distinct AX reference for the same logical window.
		{
			Element:  101,
			Title:    "Summary",
			Document: tracePath,
			X:        20,
			Y:        30,
			Width:    1200,
			Height:   800,
		},
		{
			Element:  200,
			Title:    "Other",
			Document: "/Users/test/other.gputrace",
			X:        80,
			Y:        90,
			Width:    1000,
			Height:   700,
		},
	}

	logical := deduplicateXcodeWindows(windows)
	if got, want := len(logical), 2; got != want {
		t.Fatalf("logical windows = %d, want %d: %+v", got, want, logical)
	}
	matches := exactTraceWindows(logical, strings.ToLower(filepath.Clean(tracePath)))
	if got, want := len(matches), 1; got != want {
		t.Fatalf("exact matches = %d, want %d: %v", got, want, matches)
	}
	if matches[0] != 100 {
		t.Fatalf("selected AX element = %d, want first stable element 100", matches[0])
	}
}

func TestExportSheetDestinationVerification(t *testing.T) {
	const target = "/Users/tmc/tmp/gputrace-language-matrix-20260730/traces/go"
	tests := []struct {
		name       string
		remaining  string
		candidates []string
		wantDirect bool
	}{
		{
			name:       "basename is not exact destination",
			candidates: []string{"tmp"},
			wantDirect: true,
		},
		{
			name:       "parent directory is not nested destination",
			candidates: []string{"/Users/tmc/tmp"},
			wantDirect: true,
		},
		{
			name:       "private tmp is not user tmp",
			candidates: []string{"/private/tmp"},
			wantDirect: true,
		},
		{
			name:       "partial popup navigation requires direct location",
			remaining:  "gputrace-language-matrix-20260730/traces/go",
			candidates: []string{target},
			wantDirect: true,
		},
		{
			name:       "exact path verified",
			candidates: []string{target},
		},
		{
			name:       "file URL verified",
			candidates: []string{"file:///Users/tmc/tmp/gputrace-language-matrix-20260730/traces/go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := exportSheetState{DirectoryCandidates: tt.candidates}
			if got := needsDirectExportLocation(tt.remaining, state, target); got != tt.wantDirect {
				t.Fatalf("needsDirectExportLocation = %t, want %t", got, tt.wantDirect)
			}
		})
	}
}

func TestGoToFolderNavigationComplete(t *testing.T) {
	const target = "/Users/tmc/tmp/gputrace-language-matrix-20260730/traces/go"
	tests := []struct {
		name  string
		state exportSheetState
		want  bool
	}{
		{
			name: "closed at exact path",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
				SaveEnabled:         true,
			},
			want: true,
		},
		{
			name: "exact path but sheet remains open",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
				GoToFolderSheetOpen: true,
				SaveEnabled:         true,
			},
		},
		{
			name: "closed at exact path but save disabled",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
			},
		},
		{
			name: "closed at basename only",
			state: exportSheetState{
				DirectoryCandidates: []string{"tmp"},
			},
		},
		{
			name: "closed at private tmp",
			state: exportSheetState{
				DirectoryCandidates: []string{"/private/tmp"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goToFolderNavigationComplete(test.state, target); got != test.want {
				t.Fatalf("goToFolderNavigationComplete() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGoToFolderConfirmationReady(t *testing.T) {
	const target = "/Users/tmc/tmp/gputrace-language-matrix-20260730/traces/go"
	tests := []struct {
		name  string
		state exportSheetState
		want  bool
	}{
		{
			name: "open with exact Go to path",
			state: exportSheetState{
				GoToFolderSheetOpen: true,
				GoToFolderPath:      target,
			},
			want: true,
		},
		{
			name: "open with exact candidate but stale Go to field",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
				GoToFolderSheetOpen: true,
				GoToFolderPath:      "/private/tmp",
			},
		},
		{
			name: "closed with committed parent path",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
			},
			want: true,
		},
		{
			name: "closed with basename only",
			state: exportSheetState{
				DirectoryCandidates: []string{"tmp"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goToFolderConfirmationReady(test.state, target); got != test.want {
				t.Fatalf("goToFolderConfirmationReady() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGoToFolderNavigationCompleteAfterExactEntry(t *testing.T) {
	const target = "/Users/tmc/tmp/gputrace-language-matrix-20260730/traces/go"
	tests := []struct {
		name  string
		state exportSheetState
		want  bool
	}{
		{
			name: "exact absolute candidate",
			state: exportSheetState{
				DirectoryCandidates: []string{target},
				SaveEnabled:         true,
			},
			want: true,
		},
		{
			name: "committed basename",
			state: exportSheetState{
				DirectoryCandidates: []string{"go"},
				SaveEnabled:         true,
			},
			want: true,
		},
		{
			name: "wrong basename",
			state: exportSheetState{
				DirectoryCandidates: []string{"tmp"},
				SaveEnabled:         true,
			},
		},
		{
			name: "sheet still open",
			state: exportSheetState{
				DirectoryCandidates: []string{"go"},
				SaveEnabled:         true,
				GoToFolderSheetOpen: true,
			},
		},
		{
			name: "save disabled",
			state: exportSheetState{
				DirectoryCandidates: []string{"go"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goToFolderNavigationCompleteAfterExactEntry(test.state, target); got != test.want {
				t.Fatalf("goToFolderNavigationCompleteAfterExactEntry() = %t, want %t", got, test.want)
			}
		})
	}
}

// TestGoToFolderNativeEntryReleasesCommandBeforePath pins the entry order:
// select all, delete, wait for System Events to release Command, then type the
// the path as ordinary key events. An earlier version typed the path body and
// then moved the cursor back to insert the leading slash; under host load that
// final insert was dropped and the field committed a relative path such as "tmp".
func TestGoToFolderNativeEntryReleasesCommandBeforePath(t *testing.T) {
	activateIndex := strings.Index(typeGoToFolderPathScript, `tell application id "com.apple.dt.Xcode" to activate`)
	selectIndex := strings.Index(typeGoToFolderPathScript, `keystroke "a" using command down`)
	clearIndex := strings.Index(typeGoToFolderPathScript, "key code 51")
	delayIndex := strings.Index(typeGoToFolderPathScript, "delay 0.4")
	typeIndex := strings.Index(typeGoToFolderPathScript, "repeat with pathCharacter in characters of (item 1 of argv)")
	if activateIndex < 0 || selectIndex <= activateIndex || clearIndex <= selectIndex || delayIndex <= clearIndex || typeIndex <= delayIndex {
		t.Fatalf("native entry script does not clear and release before typing the path:\n%s",
			typeGoToFolderPathScript)
	}
	if strings.Contains(typeGoToFolderPathScript, "key code 123 using command down") {
		t.Error("script still moves the cursor to insert a separate leading slash")
	}
	if strings.Contains(typeGoToFolderPathScript, `keystroke "/"`) {
		t.Error("script still types the leading slash separately")
	}
	if strings.Contains(typeGoToFolderPathScript, "keystroke (item 1 of argv)") {
		t.Error("script still types the full path as one truncation-prone event")
	}
}

// TestTypeGoToFolderPathSendsAbsolutePath guards that the whole absolute path,
// leading separator included, is what gets typed.
func TestTypeGoToFolderPathSendsAbsolutePath(t *testing.T) {
	if _, err := goToFolderPathBody("tmp"); err == nil {
		t.Error("goToFolderPathBody accepted a relative path")
	}
	if _, err := goToFolderPathBody("/tmp"); err != nil {
		t.Errorf("goToFolderPathBody rejected an absolute path: %v", err)
	}
}

func TestGoToFolderPathBody(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"absolute", "/Users/tmc/tmp", "Users/tmc/tmp", false},
		{"root", "/", "", false},
		{"relative", "Users/tmc/tmp", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := goToFolderPathBody(test.path)
			if (err != nil) != test.wantErr {
				t.Fatalf("goToFolderPathBody(%q) error = %v, wantErr %v", test.path, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("goToFolderPathBody(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestStableExportSheetWaitResult(t *testing.T) {
	tests := []struct {
		name            string
		stable          int
		deadlineReached bool
		wantDone        bool
		wantOK          bool
	}{
		{"first slow match earns confirmation", 1, true, false, false},
		{"second consecutive match succeeds", 2, true, true, true},
		{"first slow miss fails", 0, true, true, false},
		{"before deadline continues", 0, false, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotDone, gotOK := stableExportSheetWaitResult(test.stable, test.deadlineReached)
			if gotDone != test.wantDone || gotOK != test.wantOK {
				t.Fatalf("stableExportSheetWaitResult(%d, %t) = (%t, %t), want (%t, %t)",
					test.stable, test.deadlineReached,
					gotDone, gotOK, test.wantDone, test.wantOK)
			}
		})
	}
}

func TestFormatExportSheetStateIncludesBlockingEvidence(t *testing.T) {
	state := exportSheetState{
		Filename:            "raw-basename.gputrace",
		DirectoryCandidates: []string{"tmp"},
		SaveEnabled:         true,
		GoToFolderSheetOpen: false,
	}
	got := formatExportSheetState(state)
	for _, want := range []string{
		`filename="raw-basename.gputrace"`,
		`directory_candidates=["tmp"]`,
		"save_enabled=true",
		"go_to_folder_open=false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("sheet state lacks %q: %s", want, got)
		}
	}
}

func TestVerifyExportTraceIdentity(t *testing.T) {
	writeBundle := func(name, uuid string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name+".gputrace")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		metadata := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>(uuid)</key><string>` + uuid + `</string></dict></plist>`
		if err := os.WriteFile(filepath.Join(path, "metadata"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	input := writeBundle("input", "same")
	if err := verifyExportTraceIdentity(input, writeBundle("matching", "same")); err != nil {
		t.Fatalf("matching identity: %v", err)
	}
	if err := verifyExportTraceIdentity(input, writeBundle("wrong", "different")); err == nil {
		t.Fatal("mismatched identity succeeded")
	}
}

func TestStopWorkloadInWindow(t *testing.T) {
	if err := stopWorkloadInWindow(0); err != nil {
		t.Fatalf("stopWorkloadInWindow(0) failed: %v", err)
	}
}
