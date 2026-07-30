//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type xcodeProcessIdentity struct {
	PID      int
	AppPath  string
	BundleID string
}

type crashReportState struct {
	Size    int64
	ModTime time.Time
}

type xcodeCrashReport struct {
	Path      string
	PID       int
	AppPath   string
	Exception string
	Signal    string
	Assertion string
}

func (report xcodeCrashReport) Error() string {
	var details []string
	if report.Exception != "" {
		details = append(details, "exception="+report.Exception)
	}
	if report.Signal != "" {
		details = append(details, "signal="+report.Signal)
	}
	if report.Assertion != "" {
		details = append(details, "assertion="+report.Assertion)
	}
	summary := "no exception summary found"
	if len(details) > 0 {
		summary = strings.Join(details, ", ")
	}
	return fmt.Sprintf("target Xcode PID %d crashed: %s (%s)", report.PID, report.Path, summary)
}

func diagnosticReportDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "DiagnosticReports")
}

func requestedXcodeAppPath() string {
	app := os.Getenv("GPUTRACE_XCODE_APP")
	if app == "" {
		return "/Applications/Xcode.app"
	}
	if filepath.IsAbs(app) {
		return filepath.Clean(app)
	}
	name := app
	if !strings.HasSuffix(strings.ToLower(name), ".app") {
		name += ".app"
	}
	candidate := filepath.Join("/Applications", name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func xcodeProcessPath(pid int) string {
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	command := strings.TrimSpace(string(out))
	if index := strings.Index(command, ".app/"); index >= 0 {
		return command[:index+len(".app")]
	}
	return command
}

func findSelectedXcodeApp(ctx context.Context, requestedApp string) (uintptr, xcodeProcessIdentity, error) {
	for {
		out, _ := exec.Command("pgrep", "-x", "Xcode").Output()
		for _, field := range strings.Fields(string(out)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			appPath := xcodeProcessPath(pid)
			if requestedApp != "" && filepath.Clean(appPath) != filepath.Clean(requestedApp) {
				continue
			}
			appAX := axCreateApplication(int32(pid))
			if appAX == 0 {
				continue
			}
			return appAX, xcodeProcessIdentity{
				PID:      pid,
				AppPath:  appPath,
				BundleID: "com.apple.dt.Xcode",
			}, nil
		}
		if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
			return 0, xcodeProcessIdentity{}, err
		}
	}
}

func snapshotXcodeCrashReports(dir string) (map[string]crashReportState, error) {
	snapshot := make(map[string]crashReportState)
	if dir == "" {
		return snapshot, nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "Xcode*.ips"))
	if err != nil {
		return nil, fmt.Errorf("list Xcode crash reports: %w", err)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		snapshot[path] = crashReportState{Size: info.Size(), ModTime: info.ModTime()}
	}
	return snapshot, nil
}

func detectNewXcodeCrash(dir string, baseline map[string]crashReportState, identity xcodeProcessIdentity) (*xcodeCrashReport, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "Xcode*.ips"))
	if err != nil {
		return nil, fmt.Errorf("list Xcode crash reports: %w", err)
	}
	sort.Slice(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr != nil || rightErr != nil {
			return paths[i] > paths[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if old, ok := baseline[path]; ok && old.Size == info.Size() && old.ModTime.Equal(info.ModTime()) {
			continue
		}
		report, err := parseXcodeCrashReport(path)
		if err != nil {
			continue
		}
		if report.PID != identity.PID {
			continue
		}
		if identity.AppPath != "" && report.AppPath != "" &&
			filepath.Clean(report.AppPath) != filepath.Clean(identity.AppPath) {
			continue
		}
		if report.Exception == "" && report.Signal == "" && report.Assertion == "" {
			continue
		}
		return &report, nil
	}
	return nil, nil
}

var (
	crashPIDPattern       = regexp.MustCompile(`"pid"\s*:\s*(\d+)`)
	crashPathPattern      = regexp.MustCompile(`"(?:procPath|path)"\s*:\s*"([^"]*Xcode[^"]*?\.app)(?:/Contents/MacOS/Xcode)?"`)
	crashExceptionPattern = regexp.MustCompile(`"type"\s*:\s*"(EXC_[^"]+)"`)
	crashSignalPattern    = regexp.MustCompile(`"signal"\s*:\s*"([^"]+)"`)
	crashAssertionPattern = regexp.MustCompile(`(?i)assertion failed:?\s*([^"\n]+)`)
	crashKnownAssertion   = regexp.MustCompile(`(?i)([^"\n]*originalForMissingFileHistoryItem[^"\n]*)`)
)

func parseXcodeCrashReport(path string) (xcodeCrashReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return xcodeCrashReport{}, err
	}
	report := xcodeCrashReport{Path: path}
	if match := crashPIDPattern.FindSubmatch(data); len(match) == 2 {
		report.PID, _ = strconv.Atoi(string(match[1]))
	}
	if match := crashPathPattern.FindSubmatch(data); len(match) == 2 {
		report.AppPath = string(match[1])
	}
	if match := crashExceptionPattern.FindSubmatch(data); len(match) == 2 {
		report.Exception = string(match[1])
	}
	if match := crashSignalPattern.FindSubmatch(data); len(match) == 2 {
		report.Signal = string(match[1])
	}
	if match := crashAssertionPattern.FindSubmatch(data); len(match) == 2 {
		report.Assertion = strings.TrimSpace(string(match[1]))
	} else if match := crashKnownAssertion.FindSubmatch(data); len(match) == 2 {
		report.Assertion = strings.TrimSpace(string(match[1]))
	}
	return report, nil
}

func startXcodeCrashMonitor(parent context.Context, dir string, baseline map[string]crashReportState, identity xcodeProcessIdentity) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				report, err := detectNewXcodeCrash(dir, baseline, identity)
				if err != nil {
					verboseLog("Xcode crash monitor: %v", err)
					continue
				}
				if report != nil {
					cancel(*report)
					return
				}
			case <-done:
				return
			case <-parent.Done():
				return
			}
		}
	}()
	return ctx, func() {
		close(done)
		cancel(nil)
	}
}
