//go:build darwin

// axperms is a utility to check and manage Accessibility permissions on macOS.
// It uses macgo to run with the same bundle identity as gputrace.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/macgo"
)

//

var (
	axIsProcessTrusted            func() bool
	axIsProcessTrustedWithOptions func(uintptr) bool
	axCreateApplication           func(int32) uintptr
	axCopyAttributeValue          func(uintptr, uintptr, *uintptr) int32
	axPerformAction               func(uintptr, uintptr) int32

	cfRelease                 func(uintptr)
	cfRetain                  func(uintptr) uintptr
	cfStringCreateWithCString func(uintptr, unsafe.Pointer, uint32) uintptr
	cfStringGetLength         func(uintptr) int
	cfStringGetCString        func(uintptr, unsafe.Pointer, int, uint32) bool
	cfArrayGetCount           func(uintptr) int
	cfArrayGetValueAtIndex    func(uintptr, int) uintptr
	cfBooleanGetValue         func(uintptr) bool
)

const (
	kCFStringEncodingUTF8 = 0x08000100
	kAXErrorSuccess       = 0
)

var debugMode bool

// hasArg checks if an argument is present (before flag.Parse)
func hasArg(arg string) bool {
	for _, a := range os.Args[1:] {
		if a == arg {
			return true
		}
	}
	return false
}

func initAX() {
	axIsProcessTrusted = axuiautomation.AXIsProcessTrusted
	axIsProcessTrustedWithOptions = func(options uintptr) bool {
		return axuiautomation.AXIsProcessTrustedWithOptions(options)
	}
	axCreateApplication = func(pid int32) uintptr {
		return uintptr(axuiautomation.AXUIElementCreateApplication(pid))
	}
	axCopyAttributeValue = func(element uintptr, attribute uintptr, value *uintptr) int32 {
		return int32(axuiautomation.AXUIElementCopyAttributeValue(
			axuiautomation.AXUIElementRef(element),
			attribute,
			value,
		))
	}
	axPerformAction = func(element uintptr, action uintptr) int32 {
		return int32(axuiautomation.AXUIElementPerformAction(
			axuiautomation.AXUIElementRef(element),
			action,
		))
	}
	cfRelease = func(value uintptr) {
		corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	}
	cfRetain = func(value uintptr) uintptr {
		return uintptr(corefoundation.CFRetain(corefoundation.CFTypeRef(value)))
	}
	cfStringCreateWithCString = func(allocator uintptr, cstr unsafe.Pointer, encoding uint32) uintptr {
		// Unused — mkString calls corefoundation directly
		return 0
	}
	cfStringGetLength = func(value uintptr) int {
		return corefoundation.CFStringGetLength(corefoundation.CFStringRef(value))
	}
	cfStringGetCString = func(value uintptr, buffer unsafe.Pointer, bufferSize int, encoding uint32) bool {
		return corefoundation.CFStringGetCString(
			corefoundation.CFStringRef(value),
			(*byte)(buffer),
			bufferSize,
			encoding,
		)
	}
	cfArrayGetCount = func(array uintptr) int {
		return corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(array))
	}
	cfArrayGetValueAtIndex = func(array uintptr, idx int) uintptr {
		return uintptr(corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(array), idx))
	}
	cfBooleanGetValue = func(value uintptr) bool {
		return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(value))
	}
}

// Privacy pane types
const (
	PaneAccessibility   = "accessibility"
	PaneScreenRecording = "screen-recording"
	PaneFullDiskAccess  = "full-disk-access"
)

// Privacy pane URLs
var paneURLs = map[string]string{
	PaneAccessibility:   "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_Accessibility",
	PaneScreenRecording: "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_ScreenCapture",
	PaneFullDiskAccess:  "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles",
}

func main() {
	// Early debug output to see if we get past macgo re-exec
	if os.Getenv("MACGO_DEBUG") == "1" || hasArg("-debug") {
		fmt.Fprintf(os.Stderr, "[axperms] main() started, PID=%d\n", os.Getpid())
	}

	prompt := flag.Bool("prompt", false, "Trigger permission prompt if not trusted")
	reset := flag.String("reset", "", "Reset Accessibility permission for bundle ID (default: com.tmc.gputrace)")
	resetScreenRecording := flag.String("reset-screen-recording", "", "Reset Screen Recording permission for bundle ID")
	openSettings := flag.Bool("open", false, "Open System Settings Accessibility pane")
	openScreenRecording := flag.Bool("open-screen-recording", false, "Open System Settings Screen Recording pane")
	watch := flag.Bool("watch", false, "Poll for permission changes until granted")
	devMode := flag.Bool("dev-mode", true, "Use macgo DevMode for stable TCC")
	debug := flag.Bool("debug", false, "Enable debug output")
	pane := flag.String("pane", PaneAccessibility, "Privacy pane to use: accessibility, screen-recording, full-disk-access")
	readUI := flag.String("read-ui", "", "Read permission state from System Settings UI for app name")
	listUI := flag.Bool("list-ui", false, "List all apps in current privacy pane")
	toggle := flag.String("toggle", "", "Toggle permission for app name in System Settings UI")
	enable := flag.String("enable", "", "Enable permission for app name in System Settings UI")
	disable := flag.String("disable", "", "Disable permission for app name in System Settings UI")
	remove := flag.String("remove", "", "Remove app from permission list (select and press Delete)")
	// Screen Recording specific shortcuts
	enableSR := flag.String("enable-screen-recording", "", "Enable Screen Recording for app name")
	disableSR := flag.String("disable-screen-recording", "", "Disable Screen Recording for app name")
	listSR := flag.Bool("list-screen-recording", false, "List apps in Screen Recording pane")
	// Full Disk Access specific shortcuts
	openFDA := flag.Bool("open-fda", false, "Open System Settings Full Disk Access pane")
	resetFDA := flag.String("reset-fda", "", "Reset Full Disk Access permission for bundle ID")
	enableFDA := flag.String("enable-fda", "", "Enable Full Disk Access for app name")
	disableFDA := flag.String("disable-fda", "", "Disable Full Disk Access for app name")
	listFDA := flag.Bool("list-fda", false, "List apps in Full Disk Access pane")
	// Popup dismissal
	dismissPopup := flag.String("dismiss-popup", "", "Find and dismiss TCC popup for app (e.g., 'gputrace.app')")
	dismissAction := flag.String("dismiss-action", "Deny", "Button to click when dismissing popup: Deny, Open System Settings")
	flag.Parse()

	debugMode = *debug

	// Handle operations that don't need macgo
	if *reset != "" {
		resetTCC(*reset, "Accessibility")
		return
	}

	if *resetScreenRecording != "" {
		resetTCC(*resetScreenRecording, "ScreenCapture")
		return
	}

	if *openSettings {
		exec.Command("open", paneURLs[PaneAccessibility]).Run()
		return
	}

	if *openScreenRecording {
		exec.Command("open", paneURLs[PaneScreenRecording]).Run()
		return
	}

	if *openFDA {
		exec.Command("open", paneURLs[PaneFullDiskAccess]).Run()
		return
	}

	if *resetFDA != "" {
		resetTCC(*resetFDA, "SystemPolicyAllFiles")
		return
	}

	// Screen Recording shortcuts
	if *enableSR != "" {
		*pane = PaneScreenRecording
		*enable = *enableSR
	}
	if *disableSR != "" {
		*pane = PaneScreenRecording
		*disable = *disableSR
	}
	if *listSR {
		*pane = PaneScreenRecording
		*listUI = true
	}

	// Full Disk Access shortcuts
	if *enableFDA != "" {
		*pane = PaneFullDiskAccess
		*enable = *enableFDA
	}
	if *disableFDA != "" {
		*pane = PaneFullDiskAccess
		*disable = *disableFDA
	}
	if *listFDA {
		*pane = PaneFullDiskAccess
		*listUI = true
	}

	// Popup dismissal - needs Accessibility but different flow
	if *dismissPopup != "" {
		setupMacgo(*debug, *devMode)
		initAX()
		if !axIsProcessTrusted() {
			fmt.Fprintf(os.Stderr, "axperms needs Accessibility permission to dismiss popups.\n")
			triggerPrompt()
			time.Sleep(500 * time.Millisecond)
			if !axIsProcessTrusted() {
				fmt.Fprintf(os.Stderr, "Please grant Accessibility permission first.\n")
				os.Exit(1)
			}
		}
		dismissTCCPopup(*dismissPopup, *dismissAction)
		return
	}

	// UI operations need macgo for Accessibility permission
	if *readUI != "" || *listUI || *toggle != "" || *enable != "" || *disable != "" || *remove != "" {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[axperms] Starting UI operation\n")
		}
		setupMacgo(*debug, *devMode)
		if debugMode {
			fmt.Fprintf(os.Stderr, "[axperms] macgo setup complete, initializing AX\n")
		}
		initAX()
		if debugMode {
			fmt.Fprintf(os.Stderr, "[axperms] AX initialized\n")
		}

		// Check if axperms has Accessibility permission (required for UI operations)
		if !axIsProcessTrusted() {
			fmt.Fprintf(os.Stderr, "axperms needs Accessibility permission to manipulate System Settings.\n")
			fmt.Fprintf(os.Stderr, "Triggering permission prompt...\n")
			triggerPrompt()

			// Wait briefly and re-check
			time.Sleep(500 * time.Millisecond)
			if !axIsProcessTrusted() {
				fmt.Fprintf(os.Stderr, "\nPlease grant Accessibility permission to axperms in System Settings,\n")
				fmt.Fprintf(os.Stderr, "then run this command again.\n")
				exec.Command("open", paneURLs[PaneAccessibility]).Run()
				os.Exit(1)
			}
		}

		paneURL := paneURLs[*pane]
		if paneURL == "" {
			paneURL = paneURLs[PaneAccessibility]
		}

		if *listUI {
			listPrivacyApps(*pane, paneURL)
			return
		}
		if *readUI != "" {
			readAppPermission(*readUI, paneURL)
			return
		}
		if *toggle != "" {
			toggleAppPermission(*toggle, paneURL)
			return
		}
		if *enable != "" {
			setAppPermission(*enable, true, paneURL)
			return
		}
		if *disable != "" {
			setAppPermission(*disable, false, paneURL)
			return
		}
		if *remove != "" {
			removeAppFromList(*remove, paneURL)
			return
		}
		return
	}

	// Setup macgo
	setupMacgo(*debug, *devMode)

	// Initialize AX bindings
	initAX()

	// Check current status
	trusted := axIsProcessTrusted()
	fmt.Printf("AXIsProcessTrusted: %v\n", trusted)

	if *prompt && !trusted {
		fmt.Println("Triggering permission prompt...")
		triggerPrompt()
	}

	if *watch {
		watchPermission()
		return
	}

	// Show process info
	showProcessInfo()
}

func setupMacgo(debug, devMode bool) {
	if debug {
		os.Setenv("MACGO_DEBUG", "1")
	}
	if devMode {
		os.Setenv("MACGO_DEV_MODE", "1")
	}

	cfg := &macgo.Config{
		AppName: "axperms",
		Permissions: []macgo.Permission{
			macgo.Accessibility,
		},
		AdHocSign: true,
		DevMode:   true, // TODO: make this configurable via flag
		UIMode:    macgo.UIModeAccessory,
		Info: map[string]interface{}{
			"NSAccessibilityUsageDescription": "axperms needs Accessibility access to test and manage permissions.",
		},
	}

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "macgo setup failed: %v\n", err)
		os.Exit(1)
	}
}

// mkString creates a CFString from a Go string
func mkString(s string) uintptr {
	return uintptr(corefoundation.CFStringCreateWithCString(0, s, kCFStringEncodingUTF8))
}

// axString gets a string attribute from an AX element
func axString(el uintptr, attr string) string {
	var val uintptr
	key := mkString(attr)
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) != kAXErrorSuccess {
		return ""
	}
	if val == 0 {
		return ""
	}
	defer cfRelease(val)

	length := cfStringGetLength(val)
	if length == 0 {
		return ""
	}
	buf := make([]byte, length*4+1)
	if cfStringGetCString(val, unsafe.Pointer(&buf[0]), len(buf), kCFStringEncodingUTF8) {
		return string(buf[:len(buf)-1])
	}
	return ""
}

// axBool gets a boolean attribute from an AX element
func axBool(el uintptr, attr string) bool {
	var val uintptr
	key := mkString(attr)
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) != kAXErrorSuccess {
		return false
	}
	if val == 0 {
		return false
	}
	defer cfRelease(val)
	return cfBooleanGetValue(val)
}

// axChildren gets children of an AX element
func axChildren(el uintptr) []uintptr {
	var val uintptr
	key := mkString("AXChildren")
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) != kAXErrorSuccess {
		return nil
	}
	if val == 0 {
		return nil
	}
	defer cfRelease(val)

	count := cfArrayGetCount(val)
	result := make([]uintptr, count)
	for i := 0; i < count; i++ {
		child := cfArrayGetValueAtIndex(val, i)
		result[i] = cfRetain(child)
	}
	return result
}

// findSystemSettingsPID finds the PID of System Settings
func findSystemSettingsPID() int32 {
	out, err := exec.Command("pgrep", "-x", "System Settings").Output()
	if err != nil {
		return 0
	}
	var pid int32
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

// findAppInPrivacyList searches for an app in the privacy list
func findAppInPrivacyList(appName string, paneURL string) (found bool, enabled bool, element uintptr) {
	// Always open the correct pane URL to ensure we're on the right page
	if debugMode {
		fmt.Printf("[DEBUG] Opening pane: %s\n", paneURL)
	}
	exec.Command("open", paneURL).Run()
	time.Sleep(1 * time.Second)

	pid := findSystemSettingsPID()
	if pid == 0 {
		time.Sleep(2 * time.Second)
		pid = findSystemSettingsPID()
		if pid == 0 {
			fmt.Fprintf(os.Stderr, "Failed to open System Settings\n")
			return false, false, 0
		}
	}

	app := axCreateApplication(pid)
	if app == 0 {
		fmt.Fprintf(os.Stderr, "Failed to get AX element for System Settings\n")
		return false, false, 0
	}

	// Check if we have AX access
	if debugMode {
		// Test if we can read basic attributes
		title := axString(app, "AXTitle")
		role := axString(app, "AXRole")
		fmt.Printf("[DEBUG] AX app element: %v, title=%q, role=%q\n", app, title, role)
		children := axChildren(app)
		fmt.Printf("[DEBUG] Direct children count: %d\n", len(children))
		if len(children) == 0 {
			fmt.Printf("[DEBUG] WARNING: No children! Do we have Accessibility permission? AXIsProcessTrusted=%v\n", axIsProcessTrusted())
		}
	}

	// BFS to find checkboxes/switches in the privacy list
	appNameLower := strings.ToLower(appName)
	queue := []uintptr{app}
	visited := make(map[uintptr]bool)
	maxVisit := 2000 // Reduced for faster searching
	togglesFound := 0

	if debugMode {
		fmt.Printf("[DEBUG] Searching for app: %q (max %d nodes)\n", appName, maxVisit)
	}

	startTime := time.Now()
	for len(queue) > 0 && len(visited) < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		// Progress logging every 100 nodes
		if debugMode && len(visited)%100 == 0 {
			fmt.Printf("[DEBUG] Progress: visited %d nodes, queue size %d, elapsed %v\n", len(visited), len(queue), time.Since(startTime))
		}

		role := strings.Split(axString(el, "AXRole"), "\x00")[0]
		title := strings.Split(axString(el, "AXTitle"), "\x00")[0]
		desc := strings.Split(axString(el, "AXDescription"), "\x00")[0]
		identifier := strings.Split(axString(el, "AXIdentifier"), "\x00")[0]

		// Extract app name from identifier (e.g., "gputrace.app_Toggle" -> "gputrace.app")
		name := title
		if name == "" {
			name = desc
		}
		if name == "" && strings.Contains(identifier, "_Toggle") {
			name = strings.Split(identifier, "_Toggle")[0]
		}

		// Look for checkbox/switch/toggle with matching app name
		if role == "AXCheckBox" || role == "AXSwitch" || role == "AXToggle" {
			togglesFound++
			if debugMode {
				fmt.Printf("[DEBUG] Toggle %d: title=%q desc=%q id=%q name=%q\n", togglesFound, title, desc, identifier, name)
			}
			nameLower := strings.ToLower(name)
			titleLower := strings.ToLower(title)
			descLower := strings.ToLower(desc)

			if strings.Contains(nameLower, appNameLower) || strings.Contains(titleLower, appNameLower) || strings.Contains(descLower, appNameLower) {
				value := axBool(el, "AXValue")
				return true, value, cfRetain(el)
			}
		}

		// Also check for rows that might contain the app name
		if role == "AXRow" || role == "AXCell" || role == "AXGroup" {
			titleLower := strings.ToLower(title)
			descLower := strings.ToLower(desc)

			if strings.Contains(titleLower, appNameLower) || strings.Contains(descLower, appNameLower) {
				// Found a row/cell with the app name, look for checkbox in children
				children := axChildren(el)
				for _, child := range children {
					childRole := strings.Split(axString(child, "AXRole"), "\x00")[0]
					if childRole == "AXCheckBox" || childRole == "AXSwitch" || childRole == "AXToggle" {
						value := axBool(child, "AXValue")
						return true, value, cfRetain(child)
					}
				}
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	if debugMode {
		fmt.Printf("[DEBUG] Search complete: visited %d nodes, found %d toggles\n", len(visited), togglesFound)
	}
	return false, false, 0
}

// listPrivacyApps lists all apps in the specified privacy pane
func listPrivacyApps(paneName string, paneURL string) {
	pid := findSystemSettingsPID()
	if pid == 0 {
		fmt.Println("System Settings not running. Opening...")
		exec.Command("open", paneURL).Run()
		time.Sleep(2 * time.Second)
		pid = findSystemSettingsPID()
		if pid == 0 {
			fmt.Fprintf(os.Stderr, "Failed to open System Settings\n")
			return
		}
	}

	app := axCreateApplication(pid)
	if app == 0 {
		fmt.Fprintf(os.Stderr, "Failed to get AX element for System Settings\n")
		return
	}

	if debugMode {
		fmt.Printf("[DEBUG] System Settings PID: %d, AX element: %v\n", pid, app)
		// Test if we can get basic attributes
		title := axString(app, "AXTitle")
		role := axString(app, "AXRole")
		fmt.Printf("[DEBUG] App AXTitle: %q, AXRole: %q\n", title, role)
		children := axChildren(app)
		fmt.Printf("[DEBUG] Direct children count: %d\n", len(children))
	}

	paneTitle := "Accessibility"
	switch paneName {
	case PaneScreenRecording:
		paneTitle = "Screen Recording"
	case PaneFullDiskAccess:
		paneTitle = "Full Disk Access"
	}
	fmt.Printf("%s Apps:\n", paneTitle)
	fmt.Println(strings.Repeat("=", len(paneTitle)+6))

	// BFS to find all checkboxes/switches
	queue := []uintptr{app}
	visited := make(map[uintptr]bool)
	maxVisit := 5000
	found := 0

	// Track unique roles for debugging
	roles := make(map[string]int)

	for len(queue) > 0 && len(visited) < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := strings.Split(axString(el, "AXRole"), "\x00")[0]
		title := strings.Split(axString(el, "AXTitle"), "\x00")[0]
		desc := strings.Split(axString(el, "AXDescription"), "\x00")[0]
		identifier := axString(el, "AXIdentifier")

		roles[role]++

		// Debug: show toggle/switch/checkbox elements
		if debugMode && (role == "AXCheckBox" || role == "AXSwitch" || role == "AXToggle" ||
			strings.Contains(role, "Toggle") || strings.Contains(role, "Check") || strings.Contains(role, "Switch")) {
			fmt.Printf("[DEBUG] %s: title=%q desc=%q id=%q\n", role, title, desc, identifier)
		}

		// Look for checkbox/switch which represent app permissions
		if role == "AXCheckBox" || role == "AXSwitch" || role == "AXToggle" {
			name := title
			if name == "" {
				name = desc
			}
			// Extract app name from identifier (e.g., "gputrace.app_Toggle" -> "gputrace.app")
			// Clean identifier first - it may have embedded null bytes
			cleanID := strings.Split(identifier, "\x00")[0]
			if debugMode {
				fmt.Printf("[DEBUG] cleanID=%q contains_Toggle=%v\n", cleanID, strings.Contains(cleanID, "_Toggle"))
			}
			if name == "" && strings.Contains(cleanID, "_Toggle") {
				name = strings.Split(cleanID, "_Toggle")[0]
			}
			if name != "" {
				value := axBool(el, "AXValue")
				status := "[ ]"
				if value {
					status = "[✓]"
				}
				fmt.Printf("  %s %s\n", status, name)
				found++
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	if debugMode {
		fmt.Printf("\n[DEBUG] Roles found:\n")
		for r, c := range roles {
			fmt.Printf("  %s: %d\n", r, c)
		}
	}

	if found == 0 {
		fmt.Println("  (no apps found - make sure Accessibility pane is visible)")
	}
	fmt.Printf("\nTotal: %d apps\n", found)
}

// readAppPermission reads the permission state for a specific app
func readAppPermission(appName string, paneURL string) {
	found, enabled, el := findAppInPrivacyList(appName, paneURL)
	if el != 0 {
		cfRelease(el)
	}

	if !found {
		fmt.Printf("App '%s' not found in permission list\n", appName)
		fmt.Println("Make sure the app has requested this permission at least once.")
		return
	}

	fmt.Printf("App: %s\n", appName)
	fmt.Printf("Enabled: %v\n", enabled)
}

// toggleAppPermission toggles the permission for a specific app
func toggleAppPermission(appName string, paneURL string) {
	found, enabled, el := findAppInPrivacyList(appName, paneURL)
	if !found {
		fmt.Printf("App '%s' not found in permission list\n", appName)
		return
	}
	defer cfRelease(el)

	fmt.Printf("App: %s (currently %v)\n", appName, enabled)
	fmt.Println("Toggling...")

	// Perform AXPress action on the checkbox
	actionKey := mkString("AXPress")
	defer cfRelease(actionKey)

	if ret := axPerformAction(el, actionKey); ret != kAXErrorSuccess {
		fmt.Fprintf(os.Stderr, "Failed to toggle (AX error %d)\n", ret)
		return
	}

	// Check new state
	time.Sleep(500 * time.Millisecond)
	newValue := axBool(el, "AXValue")
	fmt.Printf("New state: %v\n", newValue)
}

// setAppPermission sets the permission to a specific value
func setAppPermission(appName string, enable bool, paneURL string) {
	found, currentValue, el := findAppInPrivacyList(appName, paneURL)
	if !found {
		fmt.Printf("App '%s' not found in permission list\n", appName)
		return
	}
	defer cfRelease(el)

	action := "Enabling"
	if !enable {
		action = "Disabling"
	}

	fmt.Printf("App: %s (currently %v)\n", appName, currentValue)

	if currentValue == enable {
		fmt.Printf("Already %s, no change needed\n", map[bool]string{true: "enabled", false: "disabled"}[enable])
		return
	}

	fmt.Printf("%s...\n", action)

	// Perform AXPress action on the checkbox
	actionKey := mkString("AXPress")
	defer cfRelease(actionKey)

	if ret := axPerformAction(el, actionKey); ret != kAXErrorSuccess {
		fmt.Fprintf(os.Stderr, "Failed to %s (AX error %d)\n", strings.ToLower(action), ret)
		return
	}

	// Check new state
	time.Sleep(500 * time.Millisecond)
	newValue := axBool(el, "AXValue")
	fmt.Printf("New state: %v\n", newValue)

	if newValue != enable {
		fmt.Fprintf(os.Stderr, "Warning: State change may not have taken effect\n")
	}
}

// findRowForApp finds the row element containing the app (for selection/removal)
func findRowForApp(appName string, paneURL string) (found bool, row uintptr) {
	pid := findSystemSettingsPID()
	if pid == 0 {
		fmt.Println("System Settings not running. Opening...")
		exec.Command("open", paneURL).Run()
		time.Sleep(2 * time.Second)
		pid = findSystemSettingsPID()
		if pid == 0 {
			return false, 0
		}
	}

	app := axCreateApplication(pid)
	if app == 0 {
		return false, 0
	}

	appNameLower := strings.ToLower(appName)
	queue := []uintptr{app}
	visited := make(map[uintptr]bool)
	maxVisit := 5000

	for len(queue) > 0 && len(visited) < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := axString(el, "AXRole")
		title := axString(el, "AXTitle")
		desc := axString(el, "AXDescription")

		// Look for rows containing the app name
		if role == "AXRow" || role == "AXCell" || role == "AXGroup" {
			titleLower := strings.ToLower(title)
			descLower := strings.ToLower(desc)

			if strings.Contains(titleLower, appNameLower) || strings.Contains(descLower, appNameLower) {
				return true, cfRetain(el)
			}

			// Also check if any child text contains the app name
			children := axChildren(el)
			for _, child := range children {
				childTitle := strings.ToLower(axString(child, "AXTitle"))
				childDesc := strings.ToLower(axString(child, "AXDescription"))
				childValue := strings.ToLower(axString(child, "AXValue"))
				if strings.Contains(childTitle, appNameLower) ||
					strings.Contains(childDesc, appNameLower) ||
					strings.Contains(childValue, appNameLower) {
					return true, cfRetain(el)
				}
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	return false, 0
}

// removeAppFromList removes an app from the permission list
func removeAppFromList(appName string, paneURL string) {
	found, row := findRowForApp(appName, paneURL)
	if !found {
		fmt.Printf("App '%s' not found in Accessibility list\n", appName)
		return
	}
	defer cfRelease(row)

	fmt.Printf("Found app: %s\n", appName)
	fmt.Println("Selecting row...")

	// Select the row first
	actionKey := mkString("AXPress")
	if ret := axPerformAction(row, actionKey); ret != kAXErrorSuccess {
		// Try AXSelect instead
		cfRelease(actionKey)
		actionKey = mkString("AXSelect")
		axPerformAction(row, actionKey)
	}
	cfRelease(actionKey)

	time.Sleep(300 * time.Millisecond)

	// Now find and click the minus button to remove
	// The minus button is usually in a toolbar or near the list
	fmt.Println("Looking for remove button...")

	pid := findSystemSettingsPID()
	app := axCreateApplication(pid)
	if app == 0 {
		fmt.Fprintf(os.Stderr, "Failed to get System Settings\n")
		return
	}

	// Search for minus button
	queue := []uintptr{app}
	visited := make(map[uintptr]bool)
	var minusButton uintptr

	for len(queue) > 0 && len(visited) < 5000 {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := axString(el, "AXRole")
		title := axString(el, "AXTitle")
		desc := axString(el, "AXDescription")
		identifier := axString(el, "AXIdentifier")

		if debugMode && role == "AXButton" {
			fmt.Printf("[DEBUG] Button: title=%q desc=%q id=%q\n", title, desc, identifier)
		}

		// Look for minus/remove button
		if role == "AXButton" {
			if title == "-" || title == "−" || title == "Remove" ||
				strings.Contains(strings.ToLower(desc), "remove") ||
				strings.Contains(strings.ToLower(identifier), "remove") ||
				strings.Contains(strings.ToLower(identifier), "minus") {
				minusButton = cfRetain(el)
				break
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	if minusButton == 0 {
		fmt.Fprintf(os.Stderr, "Could not find remove button. Try selecting the app manually and pressing Delete.\n")
		return
	}
	defer cfRelease(minusButton)

	fmt.Println("Clicking remove button...")
	actionKey = mkString("AXPress")
	defer cfRelease(actionKey)

	if ret := axPerformAction(minusButton, actionKey); ret != kAXErrorSuccess {
		fmt.Fprintf(os.Stderr, "Failed to click remove button (AX error %d)\n", ret)
		return
	}

	fmt.Println("Done. App should be removed from the list.")
}

func triggerPrompt() {
	key := objc.Send[uintptr](objc.ID(objc.GetClass("NSString")), objc.Sel("stringWithUTF8String:"), "AXTrustedCheckOptionPrompt\x00")
	val := objc.Send[uintptr](objc.ID(objc.GetClass("NSNumber")), objc.Sel("numberWithBool:"), true)
	opts := objc.Send[uintptr](objc.ID(objc.GetClass("NSDictionary")), objc.Sel("dictionaryWithObject:forKey:"), val, key)
	result := axIsProcessTrustedWithOptions(opts)
	fmt.Printf("AXIsProcessTrustedWithOptions (with prompt): %v\n", result)
}

func watchPermission() {
	fmt.Println("Watching for permission changes (Ctrl+C to stop)...")
	fmt.Println("Please grant Accessibility permission in System Settings.")

	// Open settings
	exec.Command("open", "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_Accessibility").Run()

	// Trigger initial prompt
	triggerPrompt()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if axIsProcessTrusted() {
			fmt.Println("\n✓ Permission granted!")
			return
		}
		fmt.Print(".")
	}
}

func showProcessInfo() {
	fmt.Printf("\nProcess Info:\n")
	fmt.Printf("  PID: %d\n", os.Getpid())
	fmt.Printf("  Executable: %s\n", os.Args[0])

	// Try to get code signature info
	if exe, err := os.Executable(); err == nil {
		out, _ := exec.Command("codesign", "-dvvv", exe).CombinedOutput()
		if len(out) > 0 {
			fmt.Printf("\nCode Signature:\n")
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "Identifier=") ||
					strings.HasPrefix(line, "TeamIdentifier=") ||
					strings.HasPrefix(line, "Signature=") ||
					strings.HasPrefix(line, "Authority=") {
					fmt.Printf("  %s\n", line)
				}
			}
		}
	}
}

func resetTCC(bundleID string, service string) {
	if bundleID == "" {
		bundleID = "com.tmc.gputrace"
	}
	if service == "" {
		service = "Accessibility"
	}
	fmt.Printf("Resetting %s permission for %s...\n", service, bundleID)
	cmd := exec.Command("tccutil", "reset", service, bundleID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n%s\n", err, out)
		os.Exit(1)
	}
	fmt.Printf("%s", out)
	fmt.Println("Done. You may need to re-grant permission in System Settings.")
}

// dismissTCCPopup finds and dismisses a TCC permission popup for the given app.
// The popup is typically shown by UserNotificationCenter or CoreServicesUIAgent.
func dismissTCCPopup(appName string, buttonName string) {
	// Processes that might show TCC popups
	popupProcesses := []string{
		"UserNotificationCenter",
		"CoreServicesUIAgent",
		"SystemUIServer",
		"System Preferences",
		"System Settings",
	}

	appNameLower := strings.ToLower(appName)
	buttonNameLower := strings.ToLower(buttonName)

	fmt.Printf("Looking for TCC popup for '%s'...\n", appName)

	for _, procName := range popupProcesses {
		pid := findProcessPID(procName)
		if pid == 0 {
			continue
		}

		if debugMode {
			fmt.Printf("[DEBUG] Checking process: %s (PID %d)\n", procName, pid)
		}

		app := axCreateApplication(pid)
		if app == 0 {
			continue
		}

		// Search for windows/dialogs containing the app name
		found, button := findPopupButton(app, appNameLower, buttonNameLower)
		if found && button != 0 {
			fmt.Printf("Found popup in %s, clicking '%s'...\n", procName, buttonName)

			actionKey := mkString("AXPress")
			ret := axPerformAction(button, actionKey)
			cfRelease(actionKey)
			cfRelease(button)

			if ret == kAXErrorSuccess {
				fmt.Println("Popup dismissed successfully")
				return
			}
			fmt.Fprintf(os.Stderr, "Failed to click button (AX error %d)\n", ret)
			return
		}
	}

	fmt.Println("No TCC popup found for this app")
}

// findProcessPID finds the PID of a process by name
func findProcessPID(name string) int32 {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0
	}
	var pid int32
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

// findPopupButton searches for a TCC popup dialog and returns the button to click
func findPopupButton(app uintptr, appNameLower string, buttonNameLower string) (found bool, button uintptr) {
	queue := []uintptr{app}
	visited := make(map[uintptr]bool)
	maxVisit := 1000

	// First pass: find if there's a window/dialog containing the app name
	var targetWindow uintptr
	for len(queue) > 0 && len(visited) < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := strings.Split(axString(el, "AXRole"), "\x00")[0]
		title := strings.ToLower(strings.Split(axString(el, "AXTitle"), "\x00")[0])
		desc := strings.ToLower(strings.Split(axString(el, "AXDescription"), "\x00")[0])
		value := strings.ToLower(strings.Split(axString(el, "AXValue"), "\x00")[0])

		if debugMode && (role == "AXWindow" || role == "AXSheet" || role == "AXDialog") {
			fmt.Printf("[DEBUG] %s: title=%q\n", role, title)
		}

		// Check if this element or its text contains the app name
		if strings.Contains(title, appNameLower) || strings.Contains(desc, appNameLower) || strings.Contains(value, appNameLower) {
			// Found a match - this window/dialog is about our app
			// Find the parent window if we're on a text element
			if role == "AXWindow" || role == "AXSheet" || role == "AXDialog" {
				targetWindow = cfRetain(el)
			} else {
				// We found text with app name, the popup exists
				// Continue searching from app root for buttons
				targetWindow = app
			}
			break
		}

		// Also check for "Screen Recording" or "would like to record" text
		if strings.Contains(title, "screen recording") || strings.Contains(desc, "screen recording") ||
			strings.Contains(title, "would like to record") || strings.Contains(desc, "would like to record") {
			targetWindow = app
			break
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	if targetWindow == 0 {
		return false, 0
	}

	// Second pass: find the button in this window
	queue = []uintptr{targetWindow}
	visited = make(map[uintptr]bool)

	for len(queue) > 0 && len(visited) < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := strings.Split(axString(el, "AXRole"), "\x00")[0]
		title := strings.ToLower(strings.Split(axString(el, "AXTitle"), "\x00")[0])
		desc := strings.ToLower(strings.Split(axString(el, "AXDescription"), "\x00")[0])

		if debugMode && role == "AXButton" {
			fmt.Printf("[DEBUG] Button: title=%q desc=%q\n", title, desc)
		}

		if role == "AXButton" {
			// Match button by name
			if strings.Contains(title, buttonNameLower) || strings.Contains(desc, buttonNameLower) {
				if targetWindow != app {
					cfRelease(targetWindow)
				}
				return true, cfRetain(el)
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	if targetWindow != app {
		cfRelease(targetWindow)
	}
	return false, 0
}
