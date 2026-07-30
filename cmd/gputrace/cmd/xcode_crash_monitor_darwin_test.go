//go:build darwin

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const xcodeCrashFixture = `{"app_name":"Xcode","timestamp":"2026-07-30 04:44:03.00 -0700"}
{
  "pid": 57028,
  "procPath": "/Applications/Xcode-rc.app/Contents/MacOS/Xcode",
  "bundleInfo": {"CFBundleIdentifier":"com.apple.dt.Xcode"},
  "exception": {"type":"EXC_CRASH","signal":"SIGABRT"},
  "asi": {"libsystem_c.dylib":["Assertion failed: originalForMissingFileHistoryItem != NULL && missingFileError != NULL"]}
}`

func TestDetectNewXcodeCrashMatchesRunPIDAndApp(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Xcode-2026-07-30-010000.ips")
	if err := os.WriteFile(oldPath, []byte(strings.ReplaceAll(xcodeCrashFixture, "57028", "11111")), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}

	unrelated := strings.ReplaceAll(xcodeCrashFixture, "57028", "64688")
	unrelated = strings.ReplaceAll(unrelated, "Xcode-rc.app", "Xcode.app")
	if err := os.WriteFile(filepath.Join(dir, "Xcode-2026-07-30-044402.ips"), []byte(unrelated), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	if err := os.WriteFile(targetPath, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := detectNewXcodeCrash(dir, baseline, xcodeProcessIdentity{
		PID:      57028,
		AppPath:  "/Applications/Xcode-rc.app",
		BundleID: "com.apple.dt.Xcode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("target crash report not detected")
	}
	if report.Path != targetPath || report.PID != 57028 || report.AppPath != "/Applications/Xcode-rc.app" {
		t.Fatalf("report identity = %+v", report)
	}
	if report.Exception != "EXC_CRASH" || report.Signal != "SIGABRT" {
		t.Fatalf("report exception = %+v", report)
	}
	if !strings.Contains(report.Assertion, "originalForMissingFileHistoryItem != NULL") {
		t.Fatalf("assertion = %q", report.Assertion)
	}
	for _, want := range []string{targetPath, "PID", "SIGABRT", "originalForMissingFileHistoryItem"} {
		if !strings.Contains(report.Error(), want) {
			t.Fatalf("error summary lacks %q: %s", want, report.Error())
		}
	}
}

func TestDetectNewXcodeCrashIgnoresBaselineAndOtherApp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	if err := os.WriteFile(path, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := detectNewXcodeCrash(dir, baseline, xcodeProcessIdentity{PID: 57028, AppPath: "/Applications/Xcode-rc.app"}); err != nil || report != nil {
		t.Fatalf("baseline report = %+v, %v; want nil", report, err)
	}

	baseline = map[string]crashReportState{}
	if report, err := detectNewXcodeCrash(dir, baseline, xcodeProcessIdentity{PID: 57028, AppPath: "/Applications/Xcode.app"}); err != nil || report != nil {
		t.Fatalf("other-app report = %+v, %v; want nil", report, err)
	}
}

func TestXcodeCrashMonitorCancelsRunContext(t *testing.T) {
	dir := t.TempDir()
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := startXcodeCrashMonitor(context.Background(), dir, baseline, xcodeProcessIdentity{
		PID:     57028,
		AppPath: "/Applications/Xcode-rc.app",
	})
	defer stop()

	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	if err := os.WriteFile(path, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("crash monitor did not cancel run context")
	}
	var report xcodeCrashReport
	if !errors.As(context.Cause(ctx), &report) {
		t.Fatalf("cause = %T %v, want xcodeCrashReport", context.Cause(ctx), context.Cause(ctx))
	}
	if report.Path != path {
		t.Fatalf("report path = %q, want %q", report.Path, path)
	}
}
