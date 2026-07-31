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
  "procPath": "\/Applications\/Xcode-rc.app\/Contents\/MacOS\/Xcode",
  "procLaunch": "2026-07-30 04:43:00.0000 -0700",
  "captureTime": "2026-07-30 04:44:03.0000 -0700",
  "bundleInfo": {"CFBundleIdentifier":"com.apple.dt.Xcode"},
  "exception": {"type":"EXC_CRASH","signal":"SIGABRT"},
  "asi": {"libsystem_c.dylib":["Assertion failed: originalForMissingFileHistoryItem != NULL && missingFileError != NULL"]}
}`

func crashScopeForTest(appPath string, pids ...int) *xcodeCrashScope {
	scope := newXcodeCrashScope(appPath, time.Date(2026, 7, 30, 4, 42, 0, 0, time.FixedZone("PDT", -7*60*60)))
	for _, pid := range pids {
		scope.bindAtTime(
			xcodeProcessIdentity{PID: pid, AppPath: appPath},
			scope.startedAt.Add(10*time.Second),
		)
	}
	return scope
}

func TestParseXcodeCrashReportDecodesEscapedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Xcode.ips")
	if err := os.WriteFile(path, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := parseXcodeCrashReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.AppPath != "/Applications/Xcode-rc.app" {
		t.Fatalf("app path = %q", report.AppPath)
	}
	if report.PID != 57028 || report.BundleID != "com.apple.dt.Xcode" ||
		report.Exception != "EXC_CRASH" || report.Signal != "SIGABRT" {
		t.Fatalf("report = %+v", report)
	}
	if report.LaunchTime.IsZero() || report.CaptureTime.IsZero() {
		t.Fatalf("report times were not decoded: %+v", report)
	}
}

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

	report, err := detectNewXcodeCrash(dir, baseline,
		crashScopeForTest("/Applications/Xcode-rc.app", 57028))
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
	if report, err := detectNewXcodeCrash(dir, baseline,
		crashScopeForTest("/Applications/Xcode-rc.app", 57028)); err != nil || report != nil {
		t.Fatalf("baseline report = %+v, %v; want nil", report, err)
	}

	baseline = map[string]crashReportState{}
	if report, err := detectNewXcodeCrash(dir, baseline,
		crashScopeForTest("/Applications/Xcode.app", 57028)); err != nil || report != nil {
		t.Fatalf("other-app report = %+v, %v; want nil", report, err)
	}
}

func TestDetectNewXcodeCrashBeforePIDBind(t *testing.T) {
	dir := t.TempDir()
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	if err := os.WriteFile(path, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := crashScopeForTest("/Applications/Xcode-rc.app")
	scope.observe(xcodeProcessIdentity{PID: 57028, AppPath: "/Applications/Xcode-rc.app"})
	report, err := detectNewXcodeCrash(dir, baseline, scope)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.PID != 57028 {
		t.Fatalf("report = %+v, want pre-bind crash", report)
	}
}

func TestDetectNewXcodeCrashRejectsUnobservedPIDBeforeBind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	if err := os.WriteFile(path, []byte(xcodeCrashFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := detectNewXcodeCrash(dir, map[string]crashReportState{},
		crashScopeForTest("/Applications/Xcode-rc.app"))
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatalf("unobserved pre-bind report = %+v, want nil", report)
	}
}

func TestDetectNewXcodeCrashTracksPIDTransition(t *testing.T) {
	dir := t.TempDir()
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	crash := strings.ReplaceAll(xcodeCrashFixture, "57028", "96360")
	if err := os.WriteFile(path, []byte(crash), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := crashScopeForTest("/Applications/Xcode-rc.app", 22486)
	scope.observe(xcodeProcessIdentity{PID: 96360, AppPath: "/Applications/Xcode-rc.app"})
	report, err := detectNewXcodeCrash(dir, baseline, scope)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.PID != 96360 {
		t.Fatalf("report = %+v, want replacement PID", report)
	}
}

func TestDetectNewXcodeCrashRejectsUnobservedSameAppPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	crash := strings.ReplaceAll(xcodeCrashFixture, "57028", "77777")
	if err := os.WriteFile(path, []byte(crash), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := detectNewXcodeCrash(dir, map[string]crashReportState{},
		crashScopeForTest("/Applications/Xcode-rc.app", 22486))
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatalf("unobserved same-app report = %+v, want nil", report)
	}
}

func TestXcodeCrashMonitorDetectsDelayedReport(t *testing.T) {
	dir := t.TempDir()
	baseline, err := snapshotXcodeCrashReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := startXcodeCrashMonitor(context.Background(), dir, baseline,
		crashScopeForTest("/Applications/Xcode-rc.app", 57028))
	defer stop()

	path := filepath.Join(dir, "Xcode-2026-07-30-044403.ips")
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(path, []byte(xcodeCrashFixture), 0o644)
	}()

	if err := waitForXcodeCrashReport(ctx, 2*time.Second); err == nil {
		t.Fatal("delayed crash report was not returned")
	}
	var report xcodeCrashReport
	if !errors.As(context.Cause(ctx), &report) {
		t.Fatalf("cause = %T %v, want xcodeCrashReport", context.Cause(ctx), context.Cause(ctx))
	}
	if report.Path != path {
		t.Fatalf("report path = %q, want %q", report.Path, path)
	}
}

func TestWaitForXcodeCrashReportGraceExpires(t *testing.T) {
	start := time.Now()
	if err := waitForXcodeCrashReport(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("grace wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("grace returned too early: %v", elapsed)
	}
}

func TestXcodeCrashMonitorCancelsAfterNoReportGrace(t *testing.T) {
	dir := t.TempDir()
	scope := crashScopeForTest("/Applications/Xcode.app", 987654)
	scope.mu.Lock()
	scope.exitObserved = true
	scope.exitAt = time.Now().Add(-time.Second)
	scope.exitedPID = 987654
	scope.liveObserved = 0
	scope.mu.Unlock()

	ctx, stop := startXcodeCrashMonitorWithGrace(
		context.Background(), dir, map[string]crashReportState{}, scope, 20*time.Millisecond,
	)
	defer stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("monitor did not cancel after no-report grace")
	}
	var exitErr xcodeExitWithoutReportError
	if !errors.As(context.Cause(ctx), &exitErr) {
		t.Fatalf("cause = %T %v, want xcodeExitWithoutReportError",
			context.Cause(ctx), context.Cause(ctx))
	}
	if exitErr.PID != 987654 || exitErr.AppPath != "/Applications/Xcode.app" {
		t.Fatalf("exit error = %+v", exitErr)
	}
}

func TestXcodeExitGraceRequiresAllObservedProcessesAbsent(t *testing.T) {
	scope := crashScopeForTest("/Applications/Xcode.app", 111)
	scope.mu.Lock()
	scope.exitObserved = true
	scope.exitAt = time.Now().Add(-10 * time.Minute)
	scope.exitedPID = 111
	scope.liveObserved = 1
	scope.mu.Unlock()
	if _, expired := scope.exitGraceExpired(time.Now(), 5*time.Minute); expired {
		t.Fatal("exit grace expired while an adopted exact-app process remains live")
	}
}

func TestSelectSingleXcodeProcessPreservesExactApp(t *testing.T) {
	xcode := xcodeProcessIdentity{PID: 81051, AppPath: "/Applications/Xcode.app"}
	got, err := selectSingleXcodeProcess([]xcodeProcessIdentity{xcode}, xcode.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != xcode {
		t.Fatalf("identity = %+v, want %+v", got, xcode)
	}

	_, err = selectSingleXcodeProcess([]xcodeProcessIdentity{
		xcode,
		{PID: 81052, AppPath: "/Applications/Xcode.app"},
	}, xcode.AppPath)
	if err == nil || !strings.Contains(err.Error(), "cannot select one safely") {
		t.Fatalf("ambiguous selection error = %v", err)
	}
}
