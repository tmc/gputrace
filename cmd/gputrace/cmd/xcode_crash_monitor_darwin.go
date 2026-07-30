//go:build darwin

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	Path        string
	PID         int
	AppPath     string
	BundleID    string
	LaunchTime  time.Time
	CaptureTime time.Time
	Exception   string
	Signal      string
	Assertion   string
}

// DiagnosticReports can publish an .ips several minutes after the process
// exits. Keep this bounded, but long enough to cover the delay observed from
// Xcode's GPU profiler crash reporter.
const xcodeCrashReportGrace = 5 * time.Minute

type xcodeCrashScope struct {
	mu           sync.RWMutex
	appPath      string
	startedAt    time.Time
	boundAt      time.Time
	pids         map[int]struct{}
	exitObserved bool
	allowRebind  bool
}

func newXcodeCrashScope(appPath string, startedAt time.Time) *xcodeCrashScope {
	return &xcodeCrashScope{
		appPath:     filepath.Clean(appPath),
		startedAt:   startedAt,
		pids:        make(map[int]struct{}),
		allowRebind: true,
	}
}

func (scope *xcodeCrashScope) observe(identity xcodeProcessIdentity) {
	if scope == nil || identity.PID == 0 {
		return
	}
	if scope.appPath != "." && identity.AppPath != "" &&
		filepath.Clean(identity.AppPath) != scope.appPath {
		return
	}
	scope.mu.Lock()
	scope.pids[identity.PID] = struct{}{}
	scope.mu.Unlock()
}

func (scope *xcodeCrashScope) bind(identity xcodeProcessIdentity) {
	scope.bindAtTime(identity, time.Now())
}

func (scope *xcodeCrashScope) bindAtTime(identity xcodeProcessIdentity, when time.Time) {
	scope.observe(identity)
	scope.mu.Lock()
	if scope.boundAt.IsZero() {
		scope.boundAt = when
	}
	scope.mu.Unlock()
}

func (scope *xcodeCrashScope) matches(report xcodeCrashReport) bool {
	if scope == nil || report.AppPath == "" ||
		filepath.Clean(report.AppPath) != scope.appPath {
		return false
	}
	scope.mu.RLock()
	_, observed := scope.pids[report.PID]
	scope.mu.RUnlock()
	return observed
}

func (scope *xcodeCrashScope) refreshProcesses() {
	if scope == nil {
		return
	}
	current := xcodeProcessesForApp(scope.appPath)
	currentPIDs := make(map[int]xcodeProcessIdentity, len(current))
	for _, identity := range current {
		currentPIDs[identity.PID] = identity
	}

	scope.mu.Lock()
	defer scope.mu.Unlock()
	for pid := range scope.pids {
		if _, ok := currentPIDs[pid]; !ok {
			scope.exitObserved = true
		}
	}
	if !scope.boundAt.IsZero() {
		// If every observed process exited and LaunchServices supplied one
		// exact-app replacement, it is the only safe automatic rebind.
		allExited := len(scope.pids) > 0
		for pid := range scope.pids {
			if _, ok := currentPIDs[pid]; ok {
				allExited = false
				break
			}
		}
		if scope.allowRebind && allExited && len(current) == 1 {
			scope.pids[current[0].PID] = struct{}{}
		}
		return
	}
	// Before the first AX bind, "open" targets the sole exact-app process.
	// Do not guess when multiple same-path instances exist.
	if len(current) == 1 {
		scope.pids[current[0].PID] = struct{}{}
	}
}

func (scope *xcodeCrashScope) crashSuspected() bool {
	if scope == nil {
		return false
	}
	scope.mu.RLock()
	defer scope.mu.RUnlock()
	return scope.exitObserved
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

func xcodeProcessesForApp(requestedApp string) []xcodeProcessIdentity {
	out, _ := exec.Command("pgrep", "-x", "Xcode").Output()
	var identities []xcodeProcessIdentity
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		appPath := xcodeProcessPath(pid)
		if requestedApp != "" && filepath.Clean(appPath) != filepath.Clean(requestedApp) {
			continue
		}
		identities = append(identities, xcodeProcessIdentity{
			PID:      pid,
			AppPath:  appPath,
			BundleID: "com.apple.dt.Xcode",
		})
	}
	return identities
}

func findSelectedXcodeApp(ctx context.Context, requestedApp string) (uintptr, xcodeProcessIdentity, error) {
	for {
		for _, identity := range xcodeProcessesForApp(requestedApp) {
			appAX := axCreateApplication(int32(identity.PID))
			if appAX == 0 {
				continue
			}
			return appAX, identity, nil
		}
		if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
			return 0, xcodeProcessIdentity{}, err
		}
	}
}

func selectSingleXcodeProcess(identities []xcodeProcessIdentity, requestedApp string) (xcodeProcessIdentity, error) {
	switch len(identities) {
	case 0:
		return xcodeProcessIdentity{}, fmt.Errorf("no Xcode process is running from %s", requestedApp)
	case 1:
		return identities[0], nil
	default:
		var pids []string
		for _, identity := range identities {
			pids = append(pids, strconv.Itoa(identity.PID))
		}
		return xcodeProcessIdentity{}, fmt.Errorf("multiple Xcode processes are running from %s (PIDs %s); cannot select one safely",
			requestedApp, strings.Join(pids, ", "))
	}
}

func findSingleXcodeApp(ctx context.Context, requestedApp string, timeout time.Duration) (uintptr, xcodeProcessIdentity, error) {
	deadline := time.Now().Add(timeout)
	for {
		identities := xcodeProcessesForApp(requestedApp)
		if len(identities) > 0 {
			identity, err := selectSingleXcodeProcess(identities, requestedApp)
			if err != nil {
				return 0, xcodeProcessIdentity{}, err
			}
			appAX := axCreateApplication(int32(identity.PID))
			if appAX == 0 {
				return 0, xcodeProcessIdentity{}, fmt.Errorf("create AX application for Xcode PID %d", identity.PID)
			}
			return appAX, identity, nil
		}
		if time.Now().After(deadline) {
			return 0, xcodeProcessIdentity{}, fmt.Errorf("no Xcode process is running from %s", requestedApp)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, xcodeProcessIdentity{}, err
		}
	}
}

func xcodeIdentityForAX(appAX uintptr) (xcodeProcessIdentity, error) {
	var pid int32
	if appAX == 0 || axUIElementGetPid(appAX, &pid) != kAXErrorSuccess || pid == 0 {
		return xcodeProcessIdentity{}, fmt.Errorf("cannot read bound Xcode PID")
	}
	appPath := xcodeProcessPath(int(pid))
	if appPath == "" {
		return xcodeProcessIdentity{}, fmt.Errorf("cannot read app path for Xcode PID %d", pid)
	}
	return xcodeProcessIdentity{
		PID:      int(pid),
		AppPath:  appPath,
		BundleID: "com.apple.dt.Xcode",
	}, nil
}

func reacquireXcodeApp(identity xcodeProcessIdentity) (uintptr, error) {
	if identity.PID == 0 || filepath.Clean(xcodeProcessPath(identity.PID)) != filepath.Clean(identity.AppPath) {
		return 0, fmt.Errorf("bound Xcode PID %d is no longer running from %s", identity.PID, identity.AppPath)
	}
	appAX := axCreateApplication(int32(identity.PID))
	if appAX == 0 {
		return 0, fmt.Errorf("cannot reacquire Xcode PID %d from %s", identity.PID, identity.AppPath)
	}
	return appAX, nil
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

func detectNewXcodeCrash(dir string, baseline map[string]crashReportState, scope *xcodeCrashScope) (*xcodeCrashReport, error) {
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
		if !scope.matches(report) {
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
	crashAssertionPattern = regexp.MustCompile(`(?i)assertion failed:?\s*([^"\n]+)`)
	crashKnownAssertion   = regexp.MustCompile(`(?i)([^"\n]*originalForMissingFileHistoryItem[^"\n]*)`)
)

func parseXcodeCrashReport(path string) (xcodeCrashReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return xcodeCrashReport{}, err
	}
	var header struct {
		Timestamp string `json:"timestamp"`
	}
	var body struct {
		PID         int    `json:"pid"`
		ProcPath    string `json:"procPath"`
		Path        string `json:"path"`
		ProcLaunch  string `json:"procLaunch"`
		CaptureTime string `json:"captureTime"`
		BundleInfo  struct {
			Identifier string `json:"CFBundleIdentifier"`
		} `json:"bundleInfo"`
		Exception struct {
			Type   string `json:"type"`
			Signal string `json:"signal"`
		} `json:"exception"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&header); err != nil {
		return xcodeCrashReport{}, fmt.Errorf("decode crash report header: %w", err)
	}
	if err := decoder.Decode(&body); err != nil {
		return xcodeCrashReport{}, fmt.Errorf("decode crash report body: %w", err)
	}
	report := xcodeCrashReport{
		Path:        path,
		PID:         body.PID,
		AppPath:     xcodeAppPath(body.ProcPath),
		BundleID:    body.BundleInfo.Identifier,
		LaunchTime:  parseIPSTime(body.ProcLaunch),
		CaptureTime: parseIPSTime(body.CaptureTime),
		Exception:   body.Exception.Type,
		Signal:      body.Exception.Signal,
	}
	if report.AppPath == "" {
		report.AppPath = xcodeAppPath(body.Path)
	}
	if match := crashAssertionPattern.FindSubmatch(data); len(match) == 2 {
		report.Assertion = strings.TrimSpace(string(match[1]))
	} else if match := crashKnownAssertion.FindSubmatch(data); len(match) == 2 {
		report.Assertion = strings.TrimSpace(string(match[1]))
	}
	return report, nil
}

func xcodeAppPath(processPath string) string {
	processPath = filepath.Clean(processPath)
	if index := strings.Index(processPath, ".app/"); index >= 0 {
		return processPath[:index+len(".app")]
	}
	if strings.HasSuffix(processPath, ".app") {
		return processPath
	}
	return ""
}

func parseIPSTime(value string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

type xcodeCrashScopeContextKey struct{}

func startXcodeCrashMonitor(parent context.Context, dir string, baseline map[string]crashReportState, scope *xcodeCrashScope) (context.Context, func()) {
	cancelContext, cancel := context.WithCancelCause(parent)
	ctx := context.WithValue(cancelContext, xcodeCrashScopeContextKey{}, scope)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				scope.refreshProcesses()
				report, err := detectNewXcodeCrash(dir, baseline, scope)
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

func xcodeCrashScopeFromContext(ctx context.Context) *xcodeCrashScope {
	scope, _ := ctx.Value(xcodeCrashScopeContextKey{}).(*xcodeCrashScope)
	return scope
}

func waitForXcodeCrashReport(ctx context.Context, grace time.Duration) error {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
