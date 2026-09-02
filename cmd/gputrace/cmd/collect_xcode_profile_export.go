//go:build darwin

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	gputraceTrace "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

type standaloneExportRecovery struct {
	Enabled    bool
	CheckOnly  bool
	Finalize   bool
	SourcePath string
	SourceUUID string
	Identity   xcodeProcessIdentity
}

type standaloneRecoveryWindow struct {
	xcodeAXWindow
	PID             int
	PerformanceView bool
	SummaryView     bool
	NewEditorView   bool
	Finished        bool
	Debugging       bool
	Progress95      bool
	SheetOpen       bool
	StopCount       int
	StopEnabled     bool
	ShowCount       int
	ShowEnabled     bool
}

type depthElement struct {
	Element uintptr
	Depth   int
}

type recoveryFinalizeSnapshot struct {
	Identity      xcodeProcessIdentity
	WindowKey     string
	Performance   bool
	SheetOpen     bool
	StopCount     int
	StopEnabled   bool
	StopElement   uintptr
	ExportFound   bool
	ExportEnabled bool
}

func runExport(cmd *cobra.Command, args []string) (retErr error) {
	status := xcodeProfileStatusWriter()
	var outputPath string
	var err error
	if len(args) > 0 {
		outputPath, err = resolveXcodeProfileTraceOutputPath(args[0])
		if err != nil {
			return err
		}
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	var crashReportDir string
	var crashBaseline map[string]crashReportState
	recoveryRequested, _ := cmd.Flags().GetBool("recover-untitled")
	if recoveryRequested {
		crashReportDir = diagnosticReportDirectory()
		crashBaseline, err = snapshotXcodeCrashReports(crashReportDir)
		if err != nil {
			return fmt.Errorf("snapshot Xcode crash reports: %w", err)
		}
	}
	recovery, err := standaloneExportRecoveryFromFlags(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	var crashScope *xcodeCrashScope
	if recovery.Enabled {
		crashScope = newXcodeCrashScope(recovery.Identity.AppPath, time.Now())
		crashScope.allowRebind = false
		crashScope.bind(recovery.Identity)
		var stopCrashMonitor func()
		ctx, stopCrashMonitor = startXcodeCrashMonitor(ctx, crashReportDir, crashBaseline, crashScope)
		defer stopCrashMonitor()
		defer func() {
			if retErr != nil {
				retErr = normalizeStandaloneRecoveryFailure(ctx, crashScope, recovery, retErr)
			}
		}()
		if err := validateStandaloneRecoveryIdentity(recovery.Identity, xcodeProcessPath(recovery.Identity.PID)); err != nil {
			return err
		}
	}

	var appAX uintptr
	var identity xcodeProcessIdentity
	if recovery.Enabled {
		identity = recovery.Identity
		appAX, err = reacquireXcodeApp(identity)
		if err != nil {
			return fmt.Errorf("cannot establish recovery Xcode selection: %w", err)
		}
		fmt.Fprintf(status, "Recovering source: %s\n", recovery.SourcePath)
		fmt.Fprintf(status, "Source trace UUID: %s\n", recovery.SourceUUID)
		fmt.Fprintf(status, "Bound Xcode: PID %d app %s\n", identity.PID, identity.AppPath)
		fmt.Fprintln(status, "Recovery accepts exact Summary, Performance, or source-bound Finished states; replay is never restarted")
	} else {
		requestedApp := requestedXcodeAppPath()
		appAX, identity, err = findSingleXcodeApp(cmd.Context(), requestedApp, 10*time.Second)
		if err != nil {
			return fmt.Errorf("cannot establish exact Xcode selection: %w", err)
		}
	}
	defer cfRelease(appAX)

	var windowAX uintptr
	var doc string
	if recovery.Enabled {
		if recovery.Finalize || recovery.CheckOnly {
			windowAX, err = waitForStandaloneFinalizeWindow(ctx, appAX, recovery, 10*time.Second)
		} else {
			windowAX, err = waitForStandaloneRecoveryWindow(ctx, appAX, recovery, 10*time.Second)
		}
		if err != nil {
			return err
		}
		doc = recovery.SourcePath
	} else {
		windowAX, doc, err = waitForStandaloneExportWindow(cmd.Context(), appAX, identity, 10*time.Second)
		if err != nil {
			return err
		}
	}

	if err := requireStandaloneExportTarget(doc); err != nil {
		return err
	}
	if recovery.CheckOnly {
		fmt.Fprintln(status, "Recovery target verified; no UI action performed")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action:           "check_recovery",
			Target:           recovery.SourcePath,
			Source:           recovery.SourcePath,
			SourceUUID:       recovery.SourceUUID,
			XcodePID:         recovery.Identity.PID,
			XcodeApp:         recovery.Identity.AppPath,
			Phase:            "recovery state verified",
			Evidence:         "exact PID/app and supported Summary, Performance, or source-bound Finished state stable across two samples",
			TargetBound:      boolPointer(true),
			SelectedTitle:    "",
			SelectedDocument: "",
		})
	}
	if recovery.Finalize {
		windowAX, err = finalizeRecoveredWorkload(ctx, appAX, windowAX, recovery, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("finalize recovered workload: %w", err)
		}
		fmt.Fprintln(status, "Recovered workload finalized; source restored, Performance reopened, and Export is enabled")
	}
	// If no output path specified, try to infer from window document
	if outputPath == "" {
		// e.g. /path/to/trace.gputrace -> /path/to/trace-perfdata.gputrace
		ext := filepath.Ext(doc) // .gputrace
		if ext == "" {
			ext = ".gputrace"
		}
		base := strings.TrimSuffix(doc, ext)
		outputPath = base + "-perfdata" + ext
		fmt.Fprintf(status, "Inferred output path: %s\n", outputPath)
	}

	fmt.Fprintf(status, "Exporting trace to: %s\n", outputPath)
	if doc != "" {
		verboseLog("runExport: window AXDocument=%q", doc)
	}

	if err := exportTrace(ctx, appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	candidates := exportCandidatePaths(doc, outputPath)
	finalPath, err := waitForExportedTrace(ctx, []string{outputPath}, exportWaitTimeout())
	if err != nil {
		if alternates := existingExportCandidates(candidates, outputPath); len(alternates) > 0 {
			return fmt.Errorf("export did not appear at requested location %s; Xcode wrote candidate output at %s; preserving it for recovery: %w",
				outputPath, strings.Join(alternates, ", "), err)
		}
		return err
	}
	payload, err := finalizeStandaloneExport(status, doc, finalPath)
	if err != nil {
		return err
	}
	if err := requireExportedTrace(outputPath); err != nil {
		return err
	}
	fmt.Fprintf(status, Colorize("Exported to: %s\n", ColorGreen), outputPath)
	actionOutput := xcodeProfileActionOutput{
		Action: "export",
		Target: doc,
		Output: outputPath,
	}
	if recovery.Enabled {
		actionOutput.Source = recovery.SourcePath
		actionOutput.SourceUUID = recovery.SourceUUID
		actionOutput.XcodePID = recovery.Identity.PID
		actionOutput.XcodeApp = recovery.Identity.AppPath
		actionOutput.Evidence = "explicit untitled-window recovery; exported UUID verified against source"
		actionOutput.TargetBound = boolPointer(true)
	}
	applyXcodePayload(&actionOutput, payload)
	return writeXcodeProfileActionOutput(actionOutput)
}

func standaloneExportRecoveryFromFlags(cmd *cobra.Command) (standaloneExportRecovery, error) {
	enabled, _ := cmd.Flags().GetBool("recover-untitled")
	checkOnly, _ := cmd.Flags().GetBool("check-recovery")
	finalize, _ := cmd.Flags().GetBool("finalize-workload")
	source, _ := cmd.Flags().GetString("source")
	pid, _ := cmd.Flags().GetInt("xcode-pid")
	app, _ := cmd.Flags().GetString("xcode-app")

	any := enabled || checkOnly || finalize || source != "" || pid != 0 || app != ""
	if !any {
		return standaloneExportRecovery{}, nil
	}
	// --source on its own declares which trace the selected window holds, for
	// the case where Xcode has cleared the window's AXDocument after replay.
	// Identity is still verified against that trace after the export is written.
	if source != "" && !enabled && !checkOnly && !finalize && pid == 0 && app == "" {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return standaloneExportRecovery{}, fmt.Errorf("resolve declared source: %w", err)
		}
		declaredExportSource = absolute
		return standaloneExportRecovery{}, nil
	}
	if !enabled || source == "" || pid <= 0 || app == "" {
		return standaloneExportRecovery{}, fmt.Errorf(
			"untitled recovery requires --recover-untitled, --source, --xcode-pid, and --xcode-app",
		)
	}
	if checkOnly && finalize {
		return standaloneExportRecovery{}, fmt.Errorf("--check-recovery and --finalize-workload are mutually exclusive")
	}
	if !filepath.IsAbs(app) {
		return standaloneExportRecovery{}, fmt.Errorf("--xcode-app must be an absolute .app path")
	}
	app = filepath.Clean(app)
	if !strings.HasSuffix(strings.ToLower(app), ".app") {
		return standaloneExportRecovery{}, fmt.Errorf("--xcode-app must name an .app bundle")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("resolve recovery source: %w", err)
	}
	source = filepath.Clean(source)
	if !strings.HasSuffix(strings.ToLower(source), ".gputrace") {
		return standaloneExportRecovery{}, fmt.Errorf("--source must name a .gputrace bundle")
	}
	payload, err := tracebundle.InspectPayload(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("inspect recovery source: %w", err)
	}
	if payload.Class != tracebundle.PayloadFull {
		return standaloneExportRecovery{}, fmt.Errorf("recovery source is not self-contained: %s", source)
	}
	metadata, err := gputraceTrace.ReadMetadata(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("read recovery source metadata: %w", err)
	}
	if metadata.UUID == "" {
		return standaloneExportRecovery{}, fmt.Errorf("recovery source has no trace UUID: %s", source)
	}
	identity := xcodeProcessIdentity{PID: pid, AppPath: app, BundleID: "com.apple.dt.Xcode"}
	return standaloneExportRecovery{
		Enabled:    true,
		CheckOnly:  checkOnly,
		Finalize:   finalize,
		SourcePath: source,
		SourceUUID: metadata.UUID,
		Identity:   identity,
	}, nil
}

func validateStandaloneRecoveryIdentity(identity xcodeProcessIdentity, actualApp string) error {
	if identity.PID <= 0 {
		return fmt.Errorf("invalid Xcode PID %d", identity.PID)
	}
	actualApp = strings.TrimSpace(actualApp)
	if actualApp == "" {
		return fmt.Errorf("Xcode PID %d is not running", identity.PID)
	}
	actualApp = filepath.Clean(actualApp)
	if actualApp != filepath.Clean(identity.AppPath) {
		return fmt.Errorf("Xcode PID %d runs from %s, not requested app %s",
			identity.PID, actualApp, identity.AppPath)
	}
	return nil
}

func normalizeStandaloneRecoveryFailure(ctx context.Context, scope *xcodeCrashScope, recovery standaloneExportRecovery, original error) error {
	return normalizeStandaloneRecoveryFailureWithGrace(ctx, scope, recovery, original, xcodeCrashReportGrace)
}

func normalizeStandaloneRecoveryFailureWithGrace(ctx context.Context, scope *xcodeCrashScope, recovery standaloneExportRecovery, original error, grace time.Duration) error {
	if scope == nil {
		return original
	}
	if xcodeProcessPath(recovery.Identity.PID) != "" {
		return original
	}
	scope.refreshProcesses()
	if !scope.crashSuspected() {
		return original
	}
	var report xcodeCrashReport
	if cause := context.Cause(ctx); errors.As(cause, &report) {
		return cause
	}
	if err := waitForXcodeCrashReport(ctx, grace); err != nil {
		if errors.As(err, &report) {
			return err
		}
		return fmt.Errorf("bound Xcode PID %d exited while waiting for a crash report (File menu state unavailable after process exit): %w",
			recovery.Identity.PID, err)
	}
	return fmt.Errorf("bound Xcode PID %d exited; File menu state unavailable after process exit; no matching DiagnosticReport appeared within %s: %w",
		recovery.Identity.PID, grace, original)
}

// declaredExportSource is the trace path given by a bare --source. It supplies
// the identity that Xcode dropped from the window's AXDocument during replay,
// so verifyExportTraceIdentity still runs against a known trace.
var declaredExportSource string

func standaloneExportTarget(windows []xcodeAXWindow) (uintptr, string, error) {
	var matches []xcodeAXWindow
	for _, window := range windows {
		doc := filepath.Clean(strings.TrimSpace(window.Document))
		if doc == "." || !filepath.IsAbs(doc) || !strings.HasSuffix(strings.ToLower(doc), ".gputrace") {
			continue
		}
		matches = append(matches, window)
	}
	switch len(matches) {
	case 0:
		// Xcode clears a trace window's AXDocument while it replays and does not
		// always restore it. Fall back to the window carrying GPU trace UI when
		// exactly one does; uniqueness within the bound process is then the only
		// available evidence of identity. The caller still verifies the exported
		// bundle against the requested source.
		var ui []xcodeAXWindow
		for _, window := range windows {
			if hasGPUTraceUI(window.Element) {
				ui = append(ui, window)
			}
		}
		if len(ui) == 1 {
			return ui[0].Element, declaredExportSource, nil
		}
		return 0, "", fmt.Errorf("cannot establish standalone export target: no AXDocument-bound .gputrace window")
	case 1:
		return matches[0].Element, matches[0].Document, nil
	default:
		var docs []string
		for _, match := range matches {
			docs = append(docs, match.Document)
		}
		return 0, "", fmt.Errorf("cannot establish standalone export target: multiple .gputrace windows are open: %s",
			strings.Join(docs, ", "))
	}
}

func waitForStandaloneExportWindow(ctx context.Context, appAX uintptr, identity xcodeProcessIdentity, timeout time.Duration) (uintptr, string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != identity.PID || filepath.Clean(bound.AppPath) != filepath.Clean(identity.AppPath) {
			return 0, "", fmt.Errorf("standalone export lost exact Xcode PID/app binding: want PID %d app %s",
				identity.PID, identity.AppPath)
		}
		window, doc, err := standaloneExportTarget(deduplicateAXWindows(GetAllWindows(appAX)))
		if err == nil {
			var pid int32
			if axUIElementGetPid(window, &pid) != kAXErrorSuccess || int(pid) != identity.PID {
				return 0, "", fmt.Errorf("standalone export target window is not owned by bound Xcode PID %d", identity.PID)
			}
			return window, doc, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return 0, "", fmt.Errorf("standalone export target not established for PID %d app %s: %w",
				identity.PID, identity.AppPath, lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, "", err
		}
	}
}

func standaloneRecoveryTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	seen := make(map[uintptr]bool)
	for _, window := range windows {
		if window.PID != recovery.Identity.PID || window.Document != "" ||
			strings.TrimSpace(window.Title) != "" || !window.PerformanceView {
			continue
		}
		if seen[window.Element] {
			continue
		}
		seen[window.Element] = true
		matches = append(matches, window)
	}
	switch len(matches) {
	case 0:
		return standaloneRecoveryWindow{}, fmt.Errorf("no untitled Performance window is bound to Xcode PID %d app %s",
			recovery.Identity.PID, recovery.Identity.AppPath)
	case 1:
		return matches[0], nil
	default:
		var elements []string
		for _, match := range matches {
			elements = append(elements, fmt.Sprintf("%d", match.Element))
		}
		return standaloneRecoveryWindow{}, fmt.Errorf("multiple untitled Performance windows are ambiguous for Xcode PID %d app %s: AX elements %s",
			recovery.Identity.PID, recovery.Identity.AppPath, strings.Join(elements, ", "))
	}
}

func standaloneRecoveryWindowKey(window standaloneRecoveryWindow) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%d,%d,%d,%d",
		window.PID,
		strings.TrimSpace(window.Title),
		filepath.Clean(window.Document),
		window.X,
		window.Y,
		window.Width,
		window.Height,
	)
}

func standaloneRecoveryGeometryKey(window standaloneRecoveryWindow) string {
	return fmt.Sprintf("%d\x00%d,%d,%d,%d",
		window.PID, window.X, window.Y, window.Width, window.Height)
}

func recoveryGeometryKeyForElement(element uintptr, pid int) string {
	x, y := axPosition(element)
	width, height := axSize(element)
	return standaloneRecoveryGeometryKey(standaloneRecoveryWindow{
		xcodeAXWindow: xcodeAXWindow{
			Element: element,
			X:       x,
			Y:       y,
			Width:   width,
			Height:  height,
		},
		PID: pid,
	})
}

func recoveryWindows(appAX uintptr) []standaloneRecoveryWindow {
	windows := deduplicateAXWindows(GetAllWindows(appAX))
	out := make([]standaloneRecoveryWindow, 0, len(windows))
	for _, window := range windows {
		var pid int32
		if axUIElementGetPid(window.Element, &pid) != kAXErrorSuccess {
			continue
		}
		stops := shallowStopButtons(window.Element)
		shows := shallowShowPerformanceButtons(window.Element)
		out = append(out, standaloneRecoveryWindow{
			xcodeAXWindow:   window,
			PID:             int(pid),
			PerformanceView: hasPerformanceView(window.Element),
			SummaryView:     hasShallowNamedGroup(window.Element, "Summary"),
			NewEditorView:   hasShallowNamedGroup(window.Element, "New Editor"),
			Finished:        hasShallowFinishedActivity(window.Element),
			Debugging:       hasShallowActivityText(window.Element, "macOS App - Debugging GPU Workload", false),
			Progress95:      hasShallowActivityText(window.Element, "95% completed", true),
			SheetOpen:       shallowSheetOpen(window.Element),
			StopCount:       len(stops),
			StopEnabled:     len(stops) == 1 && IsElementEnabled(stops[0]),
			ShowCount:       len(shows),
			ShowEnabled:     len(shows) == 1 && IsElementEnabled(shows[0]),
		})
	}
	return out
}

func hasPerformanceView(root uintptr) bool {
	return hasShallowNamedGroup(root, "Performance") || hasPerformanceData(root)
}

func hasShallowNamedGroup(root uintptr, name string) bool {
	return findElementAtDepth(
		root,
		4,
		128,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			role := axString(element, "AXRole")
			description := strings.TrimSpace(axString(element, "AXDescription"))
			return (role == "AXGroup" || role == "AXSplitGroup") && description == name
		},
	) != 0
}

func hasShallowFinishedActivity(root uintptr) bool {
	return hasShallowActivityText(root, "Finished running macOS App", false)
}

func hasShallowActivityText(root uintptr, text string, contains bool) bool {
	return findElementAtDepth(
		root,
		5,
		256,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			for _, attribute := range []string{"AXValue", "AXTitle", "AXDescription"} {
				value := strings.TrimSpace(axString(element, attribute))
				if value == text || contains && strings.Contains(value, text) {
					return true
				}
			}
			return false
		},
	) != 0
}

func findElementAtDepth(
	root uintptr,
	maxDepth, maxVisit int,
	children func(uintptr) []uintptr,
	prune, match func(uintptr) bool,
) uintptr {
	if root == 0 || maxDepth < 0 || maxVisit <= 0 {
		return 0
	}
	queue := []depthElement{{Element: root}}
	seen := make(map[uintptr]bool)
	visited := 0
	for len(queue) > 0 && visited < maxVisit {
		item := queue[0]
		queue = queue[1:]
		if item.Element == 0 || seen[item.Element] {
			continue
		}
		seen[item.Element] = true
		visited++
		if match(item.Element) {
			return item.Element
		}
		if item.Depth >= maxDepth || prune(item.Element) {
			continue
		}
		for _, child := range children(item.Element) {
			queue = append(queue, depthElement{Element: child, Depth: item.Depth + 1})
		}
	}
	return 0
}

func findElementsAtDepth(
	root uintptr,
	maxDepth, maxVisit, maxMatches int,
	children func(uintptr) []uintptr,
	prune, match func(uintptr) bool,
) []uintptr {
	if root == 0 || maxDepth < 0 || maxVisit <= 0 || maxMatches <= 0 {
		return nil
	}
	queue := []depthElement{{Element: root}}
	seen := make(map[uintptr]bool)
	var matches []uintptr
	visited := 0
	for len(queue) > 0 && visited < maxVisit && len(matches) < maxMatches {
		item := queue[0]
		queue = queue[1:]
		if item.Element == 0 || seen[item.Element] {
			continue
		}
		seen[item.Element] = true
		visited++
		if match(item.Element) {
			matches = append(matches, item.Element)
		}
		if item.Depth >= maxDepth || prune(item.Element) {
			continue
		}
		for _, child := range children(item.Element) {
			queue = append(queue, depthElement{Element: child, Depth: item.Depth + 1})
		}
	}
	return matches
}

func shallowStopButtons(root uintptr) []uintptr {
	return findElementsAtDepth(
		root,
		4,
		128,
		2,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			if axString(element, "AXRole") != "AXButton" {
				return false
			}
			title := axString(element, "AXTitle")
			description := axString(element, "AXDescription")
			return title == "Stop GPU workload" || description == "Stop GPU workload"
		},
	)
}

func shallowShowPerformanceButtons(root uintptr) []uintptr {
	return findElementsAtDepth(
		root,
		6,
		512,
		2,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			if axString(element, "AXRole") != "AXButton" {
				return false
			}
			title := axString(element, "AXTitle")
			description := axString(element, "AXDescription")
			return title == "Show Performance" || description == "Show Performance" ||
				title == "Open Performance" || description == "Open Performance"
		},
	)
}

func readRecoveryFinalizeSnapshot(appAX uintptr, recovery standaloneExportRecovery) (recoveryFinalizeSnapshot, error) {
	identity, err := xcodeIdentityForAX(appAX)
	if err != nil {
		return recoveryFinalizeSnapshot{}, err
	}
	if identity.PID != recovery.Identity.PID ||
		filepath.Clean(identity.AppPath) != filepath.Clean(recovery.Identity.AppPath) {
		return recoveryFinalizeSnapshot{}, fmt.Errorf("recovery Xcode identity changed: got PID %d app %s",
			identity.PID, identity.AppPath)
	}
	window, err := standaloneRecoveryTarget(recoveryWindows(appAX), recovery)
	if err != nil {
		return recoveryFinalizeSnapshot{}, err
	}
	stops := shallowStopButtons(window.Element)
	snapshot := recoveryFinalizeSnapshot{
		Identity:    identity,
		WindowKey:   standaloneRecoveryWindowKey(window),
		Performance: window.PerformanceView,
		StopCount:   len(stops),
	}
	if len(stops) == 1 {
		snapshot.StopElement = stops[0]
		snapshot.StopEnabled = IsElementEnabled(stops[0])
	}
	snapshot.SheetOpen = findElementAtDepth(
		window.Element,
		3,
		64,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXSheet"
		},
	) != 0
	snapshot.ExportFound, snapshot.ExportEnabled, err = fileExportMenuState(appAX, window.Element)
	if err != nil {
		return recoveryFinalizeSnapshot{}, err
	}
	afterIdentity, err := xcodeIdentityForAX(appAX)
	if err != nil || afterIdentity.PID != identity.PID ||
		filepath.Clean(afterIdentity.AppPath) != filepath.Clean(identity.AppPath) {
		return recoveryFinalizeSnapshot{}, fmt.Errorf("recovery Xcode identity changed while probing File > Export")
	}
	afterWindow, err := standaloneRecoveryTarget(recoveryWindows(appAX), recovery)
	if err != nil {
		return recoveryFinalizeSnapshot{}, err
	}
	if standaloneRecoveryWindowKey(afterWindow) != snapshot.WindowKey {
		return recoveryFinalizeSnapshot{}, fmt.Errorf("recovery window identity changed while probing File > Export")
	}
	return snapshot, nil
}

func validateRecoveryFinalizePrecondition(snapshot recoveryFinalizeSnapshot, recovery standaloneExportRecovery, windowKey string) error {
	if snapshot.Identity.PID != recovery.Identity.PID ||
		filepath.Clean(snapshot.Identity.AppPath) != filepath.Clean(recovery.Identity.AppPath) {
		return fmt.Errorf("recovery finalize identity mismatch")
	}
	if snapshot.WindowKey != windowKey {
		return fmt.Errorf("recovery finalize window identity changed")
	}
	if !snapshot.Performance {
		return fmt.Errorf("recovery finalize requires a populated Performance group")
	}
	if snapshot.SheetOpen {
		return fmt.Errorf("recovery finalize refuses a window with an open sheet")
	}
	if snapshot.StopCount != 1 || !snapshot.StopEnabled {
		return fmt.Errorf("recovery finalize requires exactly one enabled Stop GPU workload control")
	}
	if !snapshot.ExportFound {
		return fmt.Errorf("recovery finalize could not find File > Export")
	}
	if snapshot.ExportEnabled {
		return fmt.Errorf("recovery window is already export-ready; omit --finalize-workload")
	}
	return nil
}

func finalizeRecoveredWorkload(ctx context.Context, appAX, windowAX uintptr, recovery standaloneExportRecovery, timeout time.Duration) (uintptr, error) {
	axAction(windowAX, "AXRaise")
	geometryKey := recoveryGeometryKeyForElement(windowAX, recovery.Identity.PID)

	if _, summaryErr := summaryRecoveryTarget(recoveryWindows(appAX), recovery, geometryKey); summaryErr == nil {
		transitioned, err := transitionSummaryToPerformance(ctx, appAX, recovery, geometryKey, time.Now().Add(timeout))
		if err != nil {
			return 0, err
		}
		windowAX = transitioned
	}

	if _, err := restoredRecoverySourceTarget(recoveryWindows(appAX), recovery, geometryKey); err != nil {
		before, err := readRecoveryFinalizeSnapshot(appAX, recovery)
		if err != nil {
			return 0, err
		}
		windowKey := before.WindowKey
		if err := validateRecoveryFinalizePrecondition(before, recovery, windowKey); err != nil {
			return 0, err
		}

		// Re-read after probing File > Export so the exact window and Stop
		// control are current at the only mutating action in this phase.
		before, err = readRecoveryFinalizeSnapshot(appAX, recovery)
		if err != nil {
			return 0, err
		}
		if err := validateRecoveryFinalizePrecondition(before, recovery, windowKey); err != nil {
			return 0, err
		}
		var stopPID int32
		if axUIElementGetPid(before.StopElement, &stopPID) != kAXErrorSuccess ||
			int(stopPID) != recovery.Identity.PID {
			return 0, fmt.Errorf("Stop GPU workload is not owned by bound Xcode PID %d", recovery.Identity.PID)
		}
		if err := axPressWithFallbackWindow(before.StopElement, windowAX); err != nil {
			return 0, fmt.Errorf("press Stop GPU workload: %w", err)
		}
	}

	deadline := time.Now().Add(timeout)
	sourceWindow, err := waitForRestoredRecoverySource(ctx, appAX, recovery, geometryKey, deadline)
	if err != nil {
		return 0, err
	}
	shows := shallowShowPerformanceButtons(sourceWindow.Element)
	switch len(shows) {
	case 0:
		if err := clickFinishedPerformanceOCR(ctx, appAX, sourceWindow, recovery, geometryKey); err != nil {
			return 0, err
		}
	case 1:
		if !IsElementEnabled(shows[0]) {
			return 0, fmt.Errorf("Finished Show Performance control is disabled")
		}
		var showPID int32
		if axUIElementGetPid(shows[0], &showPID) != kAXErrorSuccess ||
			int(showPID) != recovery.Identity.PID {
			return 0, fmt.Errorf("Show Performance is not owned by bound Xcode PID %d", recovery.Identity.PID)
		}
		if err := axPressWithFallbackWindow(shows[0], sourceWindow.Element); err != nil {
			return 0, fmt.Errorf("press Show Performance: %w", err)
		}
	default:
		return 0, fmt.Errorf("multiple AX Show Performance controls are ambiguous")
	}

	return waitForFinalizedRecoveryPerformance(ctx, appAX, recovery, geometryKey, deadline)
}

func transitionSummaryToPerformance(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, geometryKey string, deadline time.Time) (uintptr, error) {
	stable := 0
	var lastKey string
	var summary standaloneRecoveryWindow
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return 0, err
		}
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return 0, err
		}
		window, err := summaryRecoveryTarget(recoveryWindows(appAX), recovery, geometryKey)
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
			}
			summary = window
		} else {
			lastKey = ""
			stable = 0
		}
		if stable >= 2 {
			break
		}
		if time.Now().After(deadline) {
			return 0, recoveryTimeoutError("timed out waiting for stable 95% Summary recovery state", err)
		}
		if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
			return 0, err
		}
	}

	// Re-read the complete Summary precondition at the only mutating action.
	summary, err := summaryRecoveryTarget(recoveryWindows(appAX), recovery, geometryKey)
	if err != nil {
		return 0, err
	}
	shows := shallowShowPerformanceButtons(summary.Element)
	switch len(shows) {
	case 0:
		if err := clickSummaryPerformanceOCR(ctx, appAX, summary, recovery, geometryKey); err != nil {
			return 0, err
		}
	case 1:
		if !IsElementEnabled(shows[0]) {
			return 0, fmt.Errorf("Summary Show Performance control is disabled")
		}
		var showPID int32
		if axUIElementGetPid(shows[0], &showPID) != kAXErrorSuccess ||
			int(showPID) != recovery.Identity.PID {
			return 0, fmt.Errorf("Show Performance is not owned by bound Xcode PID %d", recovery.Identity.PID)
		}
		if err := axPressWithFallbackWindow(shows[0], summary.Element); err != nil {
			return 0, fmt.Errorf("press Show Performance from Summary: %w", err)
		}
	default:
		return 0, fmt.Errorf("multiple AX Show Performance controls are ambiguous")
	}

	stable = 0
	lastKey = ""
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return 0, err
		}
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return 0, err
		}
		window, err := runningRecoveryPerformanceTarget(recoveryWindows(appAX), recovery, geometryKey)
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
			}
		} else {
			lastKey = ""
			stable = 0
		}
		if stable >= 2 {
			return window.Element, nil
		}
		if time.Now().After(deadline) {
			return 0, recoveryTimeoutError("timed out waiting for Performance after Summary Show Performance", err)
		}
		if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func waitForRestoredRecoverySource(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, geometryKey string, deadline time.Time) (standaloneRecoveryWindow, error) {
	stable := 0
	var lastKey string
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return standaloneRecoveryWindow{}, err
		}
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return standaloneRecoveryWindow{}, err
		}
		window, err := restoredRecoverySourceTarget(recoveryWindows(appAX), recovery, geometryKey)
		if err == nil {
			if shallowSheetOpen(window.Element) {
				err = fmt.Errorf("restored source window has an open sheet")
			}
		}
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
			}
		} else {
			lastKey = ""
			stable = 0
		}
		if stable >= 2 {
			return window, nil
		}
		if time.Now().After(deadline) {
			return standaloneRecoveryWindow{}, recoveryTimeoutError("timed out waiting for exact source-bound Finished state after Stop", err)
		}
		if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
			return standaloneRecoveryWindow{}, err
		}
	}
}

func waitForFinalizedRecoveryPerformance(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, geometryKey string, deadline time.Time) (uintptr, error) {
	stable := 0
	probes := 0
	var lastKey string
	var lastErr error
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return 0, err
		}
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return 0, err
		}
		window, err := finalizedRecoveryPerformanceTarget(recoveryWindows(appAX), recovery, geometryKey)
		if err == nil {
			if shallowSheetOpen(window.Element) {
				err = fmt.Errorf("unexpected sheet appeared after Show Performance")
			} else if stops := shallowStopButtons(window.Element); len(stops) != 0 {
				err = fmt.Errorf("Stop GPU workload reappeared after Show Performance")
			}
		}
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
			}
		} else {
			lastKey = ""
			stable = 0
			lastErr = err
		}
		// The File > Export probe opens the menu bar, so it runs only once the
		// cheap non-mutating signals above have gone stable. Probing it on every
		// poll opened and closed the File menu twice a second for the whole
		// wait, which takes key focus away from whatever else is running.
		if stable >= 2 {
			if probes >= maxFileExportProbes {
				return 0, recoveryTimeoutError(
					fmt.Sprintf("File > Export never became export-ready in %d probes", probes), lastErr)
			}
			probes++
			found, enabled, menuErr := fileExportMenuState(appAX, window.Element)
			switch {
			case menuErr != nil:
				err = menuErr
			case !found:
				err = fmt.Errorf("File > Export disappeared after Show Performance")
			case !enabled:
				err = fmt.Errorf("File > Export remains disabled after Show Performance")
			}
			if err == nil {
				return window.Element, nil
			}
			lastErr = err
			lastKey = ""
			stable = 0
		}
		if time.Now().After(deadline) {
			return 0, recoveryTimeoutError("timed out waiting for export-ready Performance after Show Performance", lastErr)
		}
		if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func recoveryTimeoutError(message string, lastErr error) error {
	if lastErr == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, lastErr)
}

func requireRecoveryIdentity(appAX uintptr, recovery standaloneExportRecovery) error {
	identity, err := xcodeIdentityForAX(appAX)
	if err != nil {
		return err
	}
	if identity.PID != recovery.Identity.PID ||
		filepath.Clean(identity.AppPath) != filepath.Clean(recovery.Identity.AppPath) {
		return fmt.Errorf("recovery Xcode identity changed: got PID %d app %s", identity.PID, identity.AppPath)
	}
	return nil
}

func summaryRecoveryTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	seen := make(map[string]bool)
	for _, window := range windows {
		key := standaloneRecoveryGeometryKey(window)
		if window.PID != recovery.Identity.PID ||
			geometryKey != "" && key != geometryKey ||
			strings.TrimSpace(window.Title) != "" ||
			normalizedTraceDocument(window.Document) != "" ||
			!window.SummaryView || !window.Debugging || !window.Progress95 ||
			window.SheetOpen || window.StopCount != 1 || !window.StopEnabled ||
			seen[key] {
			continue
		}
		seen[key] = true
		matches = append(matches, window)
	}
	if len(matches) != 1 {
		return standaloneRecoveryWindow{}, fmt.Errorf("want one exact untitled 95%% Summary window, found %d", len(matches))
	}
	return matches[0], nil
}

func runningRecoveryPerformanceTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) (standaloneRecoveryWindow, error) {
	window, err := transitionedRecoveryPerformanceTarget(windows, recovery, geometryKey)
	if err != nil {
		return standaloneRecoveryWindow{}, err
	}
	if window.StopCount != 1 || !window.StopEnabled {
		return standaloneRecoveryWindow{}, fmt.Errorf("transitioned Performance window is not running")
	}
	return window, nil
}

func transitionedRecoveryPerformanceTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	seen := make(map[string]bool)
	for _, window := range windows {
		key := standaloneRecoveryGeometryKey(window)
		if window.PID != recovery.Identity.PID ||
			key != geometryKey ||
			!window.PerformanceView || window.SheetOpen ||
			seen[key] {
			continue
		}
		doc := normalizedTraceDocument(window.Document)
		title := strings.TrimSpace(window.Title)
		if doc != "" && !traceDocumentMatches(window.Document, recovery.SourcePath) {
			continue
		}
		if title != "" && title != filepath.Base(recovery.SourcePath) {
			continue
		}
		seen[key] = true
		matches = append(matches, window)
	}
	if len(matches) != 1 {
		return standaloneRecoveryWindow{}, fmt.Errorf("want one Performance window with exact transition provenance, found %d", len(matches))
	}
	return matches[0], nil
}

func restoredRecoverySourceTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	for _, window := range windows {
		if window.PID != recovery.Identity.PID ||
			standaloneRecoveryGeometryKey(window) != geometryKey ||
			!isRestoredRecoverySource(window, recovery) {
			continue
		}
		matches = append(matches, window)
	}
	if len(matches) != 1 {
		return standaloneRecoveryWindow{}, fmt.Errorf("want one exact source-bound Finished New Editor window, found %d", len(matches))
	}
	return matches[0], nil
}

func restoredRecoverySourceAnyGeometry(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	for _, window := range windows {
		if window.PID == recovery.Identity.PID && isRestoredRecoverySource(window, recovery) {
			matches = append(matches, window)
		}
	}
	if len(matches) != 1 {
		return standaloneRecoveryWindow{}, fmt.Errorf("want one exact source-bound Finished New Editor window, found %d", len(matches))
	}
	return matches[0], nil
}

func isRestoredRecoverySource(window standaloneRecoveryWindow, recovery standaloneExportRecovery) bool {
	return traceDocumentMatches(window.Document, recovery.SourcePath) &&
		strings.TrimSpace(window.Title) == filepath.Base(recovery.SourcePath) &&
		window.NewEditorView && window.Finished
}

func finalizedRecoveryPerformanceTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	for _, window := range windows {
		if window.PID != recovery.Identity.PID ||
			standaloneRecoveryGeometryKey(window) != geometryKey ||
			!window.PerformanceView {
			continue
		}
		doc := normalizedTraceDocument(window.Document)
		title := strings.TrimSpace(window.Title)
		if doc != "" && !traceDocumentMatches(window.Document, recovery.SourcePath) {
			continue
		}
		if title != "" && title != filepath.Base(recovery.SourcePath) {
			continue
		}
		matches = append(matches, window)
	}
	if len(matches) != 1 {
		return standaloneRecoveryWindow{}, fmt.Errorf("want one transitioned Performance window with exact source provenance, found %d", len(matches))
	}
	return matches[0], nil
}

func normalizedTraceDocument(document string) string {
	document = strings.TrimSpace(document)
	if document == "" {
		return ""
	}
	if parsed, err := url.Parse(document); err == nil && parsed.Scheme == "file" {
		if path, err := url.PathUnescape(parsed.Path); err == nil {
			document = path
		}
	}
	return filepath.Clean(document)
}

func traceDocumentMatches(document, source string) bool {
	for _, value := range []string{document, source} {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Scheme != "" && parsed.Scheme != "file" {
			return false
		}
	}
	document = normalizedTraceDocument(document)
	source = normalizedTraceDocument(source)
	if document == "" || source == "" {
		return false
	}
	document, err := filepath.Abs(document)
	if err != nil {
		return false
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return false
	}
	if document == source {
		return true
	}
	resolvedDocument, documentErr := filepath.EvalSymlinks(document)
	resolvedSource, sourceErr := filepath.EvalSymlinks(source)
	if documentErr == nil && sourceErr == nil && resolvedDocument == resolvedSource {
		return true
	}
	documentInfo, documentErr := os.Stat(document)
	sourceInfo, sourceErr := os.Stat(source)
	return documentErr == nil && sourceErr == nil && os.SameFile(documentInfo, sourceInfo)
}

func shallowSheetOpen(window uintptr) bool {
	return findElementAtDepth(
		window,
		3,
		64,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXSheet"
		},
	) != 0
}

func waitForStandaloneRecoveryWindow(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	var lastKey string
	stable := 0
	var lastErr error
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != recovery.Identity.PID ||
			filepath.Clean(bound.AppPath) != filepath.Clean(recovery.Identity.AppPath) {
			return 0, fmt.Errorf("untitled recovery lost exact Xcode PID/app binding: want PID %d app %s",
				recovery.Identity.PID, recovery.Identity.AppPath)
		}
		window, err := standaloneRecoveryTarget(recoveryWindows(appAX), recovery)
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
				lastErr = fmt.Errorf("untitled Performance window identity is not yet stable")
			}
			if stable >= 2 {
				return window.Element, nil
			}
		} else {
			lastKey = ""
			stable = 0
			lastErr = err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("untitled recovery target not established: %w", lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func waitForStandaloneFinalizeWindow(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	var lastKey string
	stable := 0
	var lastErr error
	for {
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return 0, err
		}
		windows := recoveryWindows(appAX)
		window, err := standaloneRecoveryTarget(windows, recovery)
		if err != nil {
			window, err = restoredRecoverySourceAnyGeometry(windows, recovery)
		}
		if err != nil {
			window, err = summaryRecoveryTarget(windows, recovery, "")
		}
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
			}
			if stable >= 2 {
				return window.Element, nil
			}
		} else {
			lastKey = ""
			stable = 0
			lastErr = err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("finalize recovery target not established: %w", lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func finalizeStandaloneExport(w io.Writer, targetPath, outputPath string) (tracebundle.Payload, error) {
	if err := requireStandaloneExportTarget(targetPath); err != nil {
		return tracebundle.Payload{}, err
	}
	if err := verifyExportTraceIdentity(targetPath, outputPath); err != nil {
		return tracebundle.Payload{}, err
	}
	payload, err := tracebundle.InspectPayload(outputPath)
	if err != nil {
		return tracebundle.Payload{}, fmt.Errorf("inspect exported trace payload: %w", err)
	}
	writeXcodePayloadStatus(w, payload)
	if err := requireSelfContainedExport(outputPath, payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func requireStandaloneExportTarget(targetPath string) error {
	if targetPath != "" {
		return nil
	}
	return fmt.Errorf(
		"cannot verify standalone export identity: selected Xcode window has no AXDocument binding; use a combined xp run or explicitly bind the source trace before export",
	)
}

func existingExportCandidates(candidates []string, requested string) []string {
	var found []string
	requested = filepath.Clean(requested)
	for _, candidate := range uniquePaths(candidates) {
		if filepath.Clean(candidate) == requested {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
		}
	}
	return found
}

func requireExportedTrace(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("export completed but output not found at expected location %s: %w", path, err)
	}
	return nil
}

// isExportDialogOpen checks if an export/save dialog is already open on the window.
func isExportDialogOpen(window uintptr) bool {
	saveBtn := findButtonBFS(window, "Save", 500) // Export sheet is shallow
	return saveBtn != 0
}

func runOpenExport(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	var outputPath string
	if len(args) > 0 {
		var err error
		outputPath, err = resolveXcodeProfileTraceOutputPath(args[0])
		if err != nil {
			return err
		}
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")
	var outputFilename string
	var warning string
	if outputPath != "" {
		outputFilename = filepath.Base(outputPath)
	}
	writeOutput := func() error {
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action:          "open-export",
			Target:          target,
			Output:          outputFilename,
			RequestedOutput: outputPath,
			Warning:         warning,
		})
	}

	// Check if export dialog is already open
	if isExportDialogOpen(windowAX) {
		fmt.Fprintln(status, "Export dialog already open")
	} else {
		fmt.Fprintln(status, "Opening export dialog...")

		// Try clicking Export button in Summary panel first
		exportBtn := FindExportButton(windowAX)
		if exportBtn != 0 {
			fmt.Fprintln(status, "  Found Export button in Summary panel")
			if err := axAction(exportBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Export button: %w", err)
			}
		} else {
			// Fall back to menu
			fmt.Fprintln(status, "  Using File > Export menu...")
			if err := clickMenuItemForWindow(appAX, windowAX, []string{"File", "Export..."}); err != nil {
				return fmt.Errorf("failed to click Export menu: %w", err)
			}
		}

		// Wait for dialog to appear
		fmt.Fprintln(status, "  Waiting for export sheet...")
		sheetFound := false
		for i := 0; i < 30; i++ {
			saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
			if saveBtn != 0 {
				sheetFound = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		if !sheetFound {
			return fmt.Errorf("export dialog did not appear")
		}

		fmt.Fprintln(status, "  Export dialog opened")

		// Check "Embed performance data" checkbox if not already checked
		embedCheckbox := findCheckboxByName(windowAX, "Embed performance data")
		if embedCheckbox != 0 {
			if !IsCheckboxChecked(embedCheckbox) {
				fmt.Fprintln(status, "  Enabling 'Embed performance data'")
				axAction(embedCheckbox, "AXPress")
				time.Sleep(300 * time.Millisecond)
			}
		}
	}

	// Re-fetch window to get fresh reference after sheet appeared
	// Use findTargetWindow to get the trace window, not just any window
	freshWindow, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil || freshWindow == 0 {
		warning = "could not get window reference"
		fmt.Fprintln(status, "  Warning: could not get window reference")
		fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
		return writeOutput()
	}
	if doc := axString(freshWindow, "AXDocument"); doc != "" {
		target = doc
	}

	saveNameField := FindSaveAsTextField(freshWindow)
	if saveNameField == 0 {
		warning = "save as field not found"
		fmt.Fprintln(status, "  Warning: Save As field not found")
		fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
		return writeOutput()
	}

	if outputPath != "" {
		// User specified path - use just the basename
		// (folder navigation via Cmd+Shift+G isn't reliable, so we only set the filename)
		dir := filepath.Dir(outputPath)
		if dir != "." && dir != "/" {
			fmt.Fprintf(status, "  Note: Navigate to %s manually (folder navigation not yet supported)\n", dir)
		}
	} else {
		// Generate -perfdata suffix from current filename
		currentName := axString(saveNameField, "AXValue")
		if currentName != "" && strings.HasSuffix(currentName, ".gputrace") {
			// Don't add -perfdata if it's already there
			if strings.Contains(currentName, "-perfdata") {
				outputFilename = currentName // Keep as-is
			} else {
				ext := filepath.Ext(currentName)
				base := strings.TrimSuffix(currentName, ext)
				outputFilename = base + "-perfdata" + ext
			}
		}
	}

	// Set the filename (need to re-find the field after navigation)
	if outputFilename != "" {
		// Re-find the save field after navigation
		saveNameField = FindSaveAsTextField(freshWindow)
		if saveNameField != 0 {
			fmt.Fprintf(status, "  Setting filename: %s\n", outputFilename)
			if err := axSetValue(saveNameField, outputFilename); err != nil {
				warning = fmt.Sprintf("could not set filename: %v", err)
				fmt.Fprintf(status, "  Warning: could not set filename: %v\n", err)
			}
			// Focus out of the field to commit the value (Tab key)
			time.Sleep(200 * time.Millisecond)
			if err := axAction(saveNameField, "AXConfirm"); err != nil {
				// AXConfirm may not be supported, that's OK
			}
		}
	}

	fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
	return writeOutput()
}

func runClickSave(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
	if saveBtn == 0 {
		return fmt.Errorf("Save button not found")
	}

	// Get the filename being saved
	filename := ""
	saveField := FindSaveAsTextField(windowAX)
	if saveField != 0 {
		filename = axString(saveField, "AXValue")
		fmt.Fprintf(status, "Saving: %s\n", filename)
	}

	fmt.Fprintln(status, "Clicking Save...")
	if err := axAction(saveBtn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click Save: %w", err)
	}

	// Wait briefly for save to complete
	time.Sleep(2 * time.Second)
	fmt.Fprintln(status, "Export initiated")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "click-save",
		Target: target,
		Output: filename,
	})
}

func runSendKey(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	key := args[0]

	// Activate Xcode first
	fmt.Fprintln(status, "Activating Xcode...")
	if err := ActivateXcode(); err != nil {
		return fmt.Errorf("failed to activate Xcode: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	switch key {
	case "cmd-shift-g":
		fmt.Fprintln(status, "Sending Cmd+Shift+G...")
		if err := sendCmdShiftG(); err != nil {
			return fmt.Errorf("failed to send Cmd+Shift+G: %w", err)
		}
	case "escape":
		fmt.Fprintln(status, "Sending Escape...")
		if err := sendEscape(); err != nil {
			return fmt.Errorf("failed to send Escape: %w", err)
		}
	case "return":
		fmt.Fprintln(status, "Sending Return...")
		if err := sendReturn(); err != nil {
			return fmt.Errorf("failed to send Return: %w", err)
		}
	default:
		return fmt.Errorf("unknown key: %s (supported: cmd-shift-g, escape, return)", key)
	}

	fmt.Fprintln(status, "Key sent")
	return nil
}

func runCheckGoToFolder(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	// Look for "Go" button which indicates Go to Folder dialog
	goBtn := findButtonBFS(windowAX, "Go", 1000)
	if goBtn != 0 {
		fmt.Fprintln(status, "Go to Folder dialog: OPEN")
		// Try to find the path text field
		pathField := FindPathTextField(windowAX)
		if pathField != 0 {
			val := axString(pathField, "AXValue")
			fmt.Fprintf(status, "  Path field value: %q\n", val)
		}
	} else {
		fmt.Fprintln(status, "Go to Folder dialog: NOT OPEN")
	}

	// Also check for Save button to see overall dialog state
	saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
	if saveBtn != 0 {
		fmt.Fprintln(status, "Export dialog: OPEN")
		// Check if Save is enabled
		enabled := IsElementEnabled(saveBtn)
		fmt.Fprintf(status, "  Save button enabled: %v\n", enabled)
		// Show the save-as field value
		saveField := FindSaveAsTextField(windowAX)
		if saveField != 0 {
			val := axString(saveField, "AXValue")
			fmt.Fprintf(status, "  Filename: %q\n", val)
		} else {
			fmt.Fprintln(status, "  Filename field: NOT FOUND")
		}
		// Look for disclosure triangle or path control
		disclosure := findButtonBFS(windowAX, "disclosure", 500)
		if disclosure != 0 {
			fmt.Fprintln(status, "  Has disclosure button")
		}
		// Look for popup buttons (e.g., "Where" location selector)
		popup := findElement(windowAX, func(el uintptr) bool {
			role := axString(el, "AXRole")
			return role == "AXPopUpButton"
		})
		if popup != 0 {
			val := axString(popup, "AXValue")
			desc := axString(popup, "AXDescription")
			fmt.Fprintf(status, "  Popup button: value=%q desc=%q\n", val, desc)
		}
	} else {
		fmt.Fprintln(status, "Export dialog: NOT OPEN")
	}

	return nil
}

func runDebugFileBrowser(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open - use 'gputrace xp open-export' first")
	}

	fmt.Fprintln(status, "Scanning file browser elements...")
	fmt.Fprintln(status)

	// Look for browser/table/outline elements that might contain the file list
	count := 0
	findElement(windowAX, func(el uintptr) bool {
		role := axString(el, "AXRole")

		// Look for elements that might be file list items
		if role == "AXCell" || role == "AXRow" || role == "AXOutlineRow" ||
			role == "AXStaticText" || role == "AXTextField" || role == "AXGroup" ||
			role == "AXBrowser" || role == "AXTable" || role == "AXOutline" {

			title := axString(el, "AXTitle")
			value := axString(el, "AXValue")
			desc := axString(el, "AXDescription")
			identifier := axString(el, "AXIdentifier")

			// Only print if there's some content
			if title != "" || value != "" || desc != "" || identifier != "" {
				count++
				fmt.Fprintf(status, "[%d] Role=%s\n", count, role)
				if title != "" {
					fmt.Fprintf(status, "    Title: %q\n", title)
				}
				if value != "" {
					fmt.Fprintf(status, "    Value: %q\n", value)
				}
				if desc != "" {
					fmt.Fprintf(status, "    Desc: %q\n", desc)
				}
				if identifier != "" {
					fmt.Fprintf(status, "    ID: %q\n", identifier)
				}
				fmt.Fprintln(status)
			}
		}
		return false // Continue searching
	})

	fmt.Fprintf(status, "Found %d elements with content\n", count)
	return nil
}

func runSetExportPath(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	absPath := args[0]
	if !filepath.IsAbs(absPath) {
		return fmt.Errorf("path must be absolute: %s", absPath)
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	// Check export dialog is open
	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	// Strategy: Set the full path in the filename field
	// macOS will interpret paths with / as directory navigation
	saveField := FindSaveAsTextField(windowAX)
	if saveField == 0 {
		return fmt.Errorf("save-as field not found")
	}

	fmt.Fprintf(status, "Setting export path: %s\n", absPath)
	if err := axSetValue(saveField, absPath); err != nil {
		return fmt.Errorf("failed to set path: %w", err)
	}

	// Note: When setting a full path in macOS save dialogs,
	// the system converts "/" to ":" in the filename (HFS path separator)
	// The correct approach is to:
	// 1. Set just the filename
	// 2. Navigate to the directory separately

	// For now, we set the full path and note that manual intervention
	// may be needed for directory navigation since Cmd+Shift+G doesn't
	// work in Xcode's export dialog.

	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)

	fmt.Fprintf(status, "  Directory: %s\n", dir)
	fmt.Fprintf(status, "  Filename: %s\n", base)
	fmt.Fprintln(status, "  Note: Xcode export dialog doesn't support Cmd+Shift+G")
	fmt.Fprintln(status, "  If directory navigation is needed, set filename only and navigate manually")

	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:          "set-export-path",
		Target:          target,
		Output:          absPath,
		RequestedOutput: absPath,
	})
}

func runSetExportFilename(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	filename := args[0]

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	saveField := FindSaveAsTextField(windowAX)
	if saveField == 0 {
		return fmt.Errorf("save-as field not found")
	}

	fmt.Fprintf(status, "Setting filename: %s\n", filename)
	if err := axSetValue(saveField, filename); err != nil {
		return fmt.Errorf("failed to set filename: %w", err)
	}

	fmt.Fprintln(status, "Filename set")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "set-export-filename",
		Target: target,
		Output: filename,
	})
}

func runSendEnter(cmd *cobra.Command, args []string) error {
	// No setupMacgo needed - we just use AppleScript
	status := xcodeProfileStatusWriter()

	fmt.Fprintln(status, "Sending Enter to Xcode...")
	if err := sendReturn(); err != nil {
		return fmt.Errorf("failed to send Enter: %w", err)
	}

	fmt.Fprintln(status, "Enter sent")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "send-enter",
		Target: "xcode",
	})
}
