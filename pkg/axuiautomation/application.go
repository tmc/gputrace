package axuiautomation

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/appledocs/generated/applicationservices"
	"github.com/tmc/appledocs/generated/corefoundation"
)

// Application represents a running application for accessibility automation.
type Application struct {
	bundleID string
	pid      int32
	root     *Element
	strCache *stringCache
}

// NewApplication creates a new Application by bundle ID.
// It looks up the running process by bundle ID and creates an accessibility element for it.
func NewApplication(bundleID string) (*Application, error) {
	// Find PID by bundle ID using pgrep
	pid, err := findPIDByBundleID(bundleID)
	if err != nil {
		return nil, ErrNotRunning
	}

	return NewApplicationFromPID(pid), nil
}

// NewApplicationFromPID creates a new Application from a process ID.
func NewApplicationFromPID(pid int32) *Application {
	ref := applicationservices.AXUIElementCreateApplication(pid)
	if ref == 0 {
		return nil
	}

	app := &Application{
		pid:      pid,
		strCache: newStringCache(),
	}
	app.root = newElement(ref, app)

	return app
}

// findPIDByBundleID finds the PID of a running application by its bundle ID.
func findPIDByBundleID(bundleID string) (int32, error) {
	// Try using pgrep with bundle ID pattern
	// First, try to find by process name derived from bundle ID
	// e.g., "com.apple.dt.Xcode" -> "Xcode"
	parts := strings.Split(bundleID, ".")
	if len(parts) == 0 {
		return 0, ErrNotRunning
	}
	processName := parts[len(parts)-1]

	// Try case-insensitive pgrep first (more reliable)
	cmd := exec.Command("pgrep", "-xi", processName)
	output, err := cmd.Output()
	if err != nil {
		// Try exact match with capitalized first letter
		capitalizedName := strings.ToUpper(processName[:1]) + processName[1:]
		cmd = exec.Command("pgrep", "-x", capitalizedName)
		output, err = cmd.Output()
	}
	if err != nil {
		// Try with full bundle ID using lsappinfo
		cmd = exec.Command("lsappinfo", "info", "-only", "pid", bundleID)
		output, err = cmd.Output()
		if err != nil {
			return 0, ErrNotRunning
		}
		// Parse lsappinfo output like "pid=12345"
		outStr := strings.TrimSpace(string(output))
		if strings.HasPrefix(outStr, "pid=") {
			pidStr := strings.TrimPrefix(outStr, "pid=")
			pid, err := strconv.ParseInt(pidStr, 10, 32)
			if err != nil {
				return 0, ErrNotRunning
			}
			return int32(pid), nil
		}
		return 0, ErrNotRunning
	}

	// Parse pgrep output
	pidStr := strings.TrimSpace(string(output))
	lines := strings.Split(pidStr, "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, ErrNotRunning
	}

	pid, err := strconv.ParseInt(lines[0], 10, 32)
	if err != nil {
		return 0, ErrNotRunning
	}

	return int32(pid), nil
}

// PID returns the application's process ID.
func (a *Application) PID() int32 {
	return a.pid
}

// BundleID returns the application's bundle ID.
func (a *Application) BundleID() string {
	return a.bundleID
}

// Root returns the root accessibility element for the application.
func (a *Application) Root() *Element {
	return a.root
}

// Close releases all resources associated with the application.
func (a *Application) Close() {
	if a.root != nil {
		a.root.Release()
		a.root = nil
	}
	if a.strCache != nil {
		a.strCache.release()
	}
}

// IsRunning returns true if the application is still running.
func (a *Application) IsRunning() bool {
	if a.root == nil {
		return false
	}
	// Try to get a basic attribute to verify the app is accessible
	return a.root.Exists()
}

// Activate brings the application to the foreground.
func (a *Application) Activate() error {
	// Use AppleScript to activate the application
	script := `tell application "System Events" to set frontmost of (first process whose unix id is ` +
		strconv.Itoa(int(a.pid)) + `) to true`

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

// Windows returns a query for all windows of the application.
func (a *Application) Windows() *ElementQuery {
	if a.root == nil {
		return newElementQuery(nil, a)
	}
	return newElementQuery(a.root, a).ByRole("AXWindow")
}

// MainWindow returns the application's main window (first window).
func (a *Application) MainWindow() *Element {
	return a.Windows().First()
}

// WindowByTitle returns the first window with the given title.
func (a *Application) WindowByTitle(title string) *Element {
	return a.Windows().ByTitle(title).First()
}

// WindowByTitleContains returns the first window whose title contains the given substring.
func (a *Application) WindowByTitleContains(substr string) *Element {
	return a.Windows().ByTitleContains(substr).First()
}

// FocusedElement returns the currently focused element.
func (a *Application) FocusedElement() *Element {
	if a.root == nil || a.root.ref == 0 {
		return nil
	}

	var value corefoundation.CFTypeRef
	err := applicationservices.AXUIElementCopyAttributeValue(a.root.ref, axAttr("AXFocusedUIElement"), &value)
	if int(err) != axErrorSuccess || value == 0 {
		return nil
	}

	return newElement(applicationservices.AXUIElementRef(value), a)
}

// MenuBar returns the application's menu bar element.
func (a *Application) MenuBar() *Element {
	if a.root == nil || a.root.ref == 0 {
		return nil
	}

	var value corefoundation.CFTypeRef
	err := applicationservices.AXUIElementCopyAttributeValue(a.root.ref, axAttr("AXMenuBar"), &value)
	if int(err) != axErrorSuccess || value == 0 {
		return nil
	}

	return newElement(applicationservices.AXUIElementRef(value), a)
}

// Dialogs returns a query for all dialog windows.
func (a *Application) Dialogs() *ElementQuery {
	if a.root == nil {
		return newElementQuery(nil, a)
	}
	return newElementQuery(a.root, a).ByRole("AXDialog")
}

// Sheets returns a query for all sheet windows.
func (a *Application) Sheets() *ElementQuery {
	if a.root == nil {
		return newElementQuery(nil, a)
	}
	return newElementQuery(a.root, a).ByRole("AXSheet")
}

// NewObserver creates a new observer for this application.
func (a *Application) NewObserver() (*Observer, error) {
	return NewObserver(a)
}

// Descendants returns a query for all descendants of the application root.
func (a *Application) Descendants() *ElementQuery {
	if a.root == nil {
		return newElementQuery(nil, a)
	}
	return newElementQuery(a.root, a)
}

// Buttons returns a query for all buttons in the application.
func (a *Application) Buttons() *ElementQuery {
	return a.Descendants().ByRole("AXButton")
}

// TextFields returns a query for all text fields in the application.
func (a *Application) TextFields() *ElementQuery {
	return a.Descendants().ByRole("AXTextField")
}

// Checkboxes returns a query for all checkboxes in the application.
func (a *Application) Checkboxes() *ElementQuery {
	return a.Descendants().ByRole("AXCheckBox")
}

// IsProcessTrusted checks if the current process has accessibility permissions.
func IsProcessTrusted() bool {
	return applicationservices.AXIsProcessTrusted()
}

// PromptForAccessibility triggers the system accessibility permission prompt.
// Returns true if already trusted, false otherwise.
func PromptForAccessibility() bool {
	// Just use the simple version - the prompt happens automatically on macOS
	return applicationservices.AXIsProcessTrustedWithOptions(nil)
}

// CheckAccessibilityAccess performs a diagnostic check to see if accessibility API is working.
// Returns the AX error code (0 = success, -25211 = API disabled/no permission).
func CheckAccessibilityAccess(pid int32) (int, string) {
	ref := applicationservices.AXUIElementCreateApplication(pid)
	if ref == 0 {
		return -1, "failed to create AXUIElement"
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(ref))

	// Try to get the AXChildren attribute - this will fail if we don't have permission
	var value corefoundation.CFTypeRef
	err := applicationservices.AXUIElementCopyAttributeValue(ref, axAttr("AXChildren"), &value)
	// Convert to signed int32 to get proper error codes
	code := int(int32(err))
	if code == 0 && value != 0 {
		corefoundation.CFRelease(value)
		return 0, "OK"
	}

	switch code {
	case -25211:
		return code, "API disabled (no accessibility permission)"
	case -25202:
		return code, "invalid UI element"
	case -25204:
		return code, "cannot complete"
	case -25205:
		return code, "attribute unsupported"
	case -25212:
		return code, "no value"
	default:
		return code, fmt.Sprintf("error code %d", code)
	}
}

// ClickMenuItem clicks a menu item by its path (e.g., ["File", "Export..."]).
func (a *Application) ClickMenuItem(path []string) error {
	if len(path) == 0 {
		return ErrElementNotFound
	}

	menuBar := a.MenuBar()
	if menuBar == nil {
		return &Error{Message: "menu bar not found"}
	}

	// Navigate through the menu hierarchy
	current := menuBar
	for i, itemName := range path {
		// Find the menu item
		var menuItem *Element
		children := current.Children()
		for _, child := range children {
			role := child.Role()
			title := child.Title()
			if (role == "AXMenuBarItem" || role == "AXMenuItem" || role == "AXMenu") && title == itemName {
				menuItem = child
				break
			}
		}

		if menuItem == nil {
			return fmt.Errorf("menu item %q not found", itemName)
		}

		// Click the menu item to open it
		if err := menuItem.Click(); err != nil {
			return fmt.Errorf("failed to click menu item %q: %w", itemName, err)
		}

		// For non-leaf items, wait for menu to expand and find the submenu
		if i < len(path)-1 {
			time.Sleep(200 * time.Millisecond)

			// Find the submenu that opened
			children = menuItem.Children()
			for _, child := range children {
				if child.Role() == "AXMenu" {
					current = child
					break
				}
			}
		}
	}

	return nil
}
