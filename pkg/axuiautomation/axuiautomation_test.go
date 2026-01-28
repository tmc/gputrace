package axuiautomation_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tmc/gputrace/pkg/axuiautomation"
	"github.com/tmc/macgo"
)

// TestMain locks the main test goroutine to the OS thread before running tests.
// This is required for ApplicationServices operations which must run on the main thread.
// It also sets up TCC identity for accessibility permissions.
func TestMain(m *testing.M) {
	runtime.LockOSThread()

	// Set up TCC identity for accessibility permissions
	// This allows tests to access the accessibility API
	if err := macgo.Start(&macgo.Config{
		Permissions: []macgo.Permission{
			macgo.Accessibility,
		},
	},
	); err != nil {
		// Log but don't fail - tests will skip if permissions aren't available
		os.Stderr.WriteString("macgo.Setup failed: " + err.Error() + "\n")
	}

	os.Exit(m.Run())
}

func TestIsProcessTrusted(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The actual result depends on system permissions
	trusted := axuiautomation.IsProcessTrusted()
	t.Logf("IsProcessTrusted: %v", trusted)
}

// skipIfNotTrusted skips the test if accessibility permissions are not granted
func skipIfNotTrusted(t *testing.T) {
	t.Helper()
	if !axuiautomation.IsProcessTrusted() {
		t.Skip("Skipping: accessibility permissions not granted")
	}
}

func TestNewApplicationFromPID(t *testing.T) {
	// Test with PID 1 (launchd) - should create an app reference
	app := axuiautomation.NewApplicationFromPID(1)
	if app == nil {
		t.Skip("Could not create application from PID 1 (may require accessibility permissions)")
	}
	defer app.Close()

	if app.PID() != 1 {
		t.Errorf("expected PID 1, got %d", app.PID())
	}
}

func TestNewApplication_Finder(t *testing.T) {
	skipIfNotTrusted(t)

	// Finder should always be running on macOS
	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	if !app.IsRunning() {
		t.Error("expected Finder to be running")
	}

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element to not be nil")
	}

	role := root.Role()
	if role != "AXApplication" {
		t.Errorf("expected role AXApplication, got %q", role)
	}
}

func TestApplication_Windows(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	windows := app.Windows()
	if windows == nil {
		t.Fatal("expected Windows() to return a query")
	}

	// Count windows (Finder usually has at least one)
	count := windows.Count()
	t.Logf("Finder has %d windows", count)
}

func TestElement_Attributes(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element")
	}

	// Test various attribute getters
	tests := []struct {
		name   string
		getter func() string
		want   string
	}{
		{"Role", root.Role, "AXApplication"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.getter()
			if got != tt.want {
				t.Errorf("%s() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	// Test Title (should be "Finder")
	title := root.Title()
	if title != "Finder" {
		t.Errorf("Title() = %q, want %q", title, "Finder")
	}
}

func TestElement_Children(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element")
	}

	children := root.Children()
	t.Logf("Root has %d children", len(children))

	// Finder root should have children (menu bar, windows, etc.)
	if len(children) == 0 {
		t.Error("expected root to have children")
	}

	// Check that children have valid roles
	for i, child := range children {
		if child == nil {
			t.Errorf("child %d is nil", i)
			continue
		}
		role := child.Role()
		if role == "" {
			t.Errorf("child %d has empty role", i)
		}
		t.Logf("  child %d: role=%s title=%q", i, role, child.Title())
	}
}

func TestElementQuery_ByRole(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	// Find the menu bar
	menuBar := app.MenuBar()
	if menuBar == nil {
		t.Skip("Could not find menu bar")
	}

	role := menuBar.Role()
	if role != "AXMenuBar" {
		t.Errorf("MenuBar role = %q, want AXMenuBar", role)
	}
}

func TestElementQuery_ByTitle(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	// Search for buttons with "File" in the menu bar
	menuBar := app.MenuBar()
	if menuBar == nil {
		t.Skip("Could not find menu bar")
	}

	// Find File menu item
	fileMenu := menuBar.Descendants().ByRole("AXMenuBarItem").ByTitle("File").First()
	if fileMenu == nil {
		t.Skip("Could not find File menu item")
	}

	if fileMenu.Title() != "File" {
		t.Errorf("expected title 'File', got %q", fileMenu.Title())
	}
}

func TestElementQuery_WithLimit(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element")
	}

	// Search with a small limit
	query := root.Descendants().WithLimit(10)
	elements := query.AllElements()

	t.Logf("Found %d elements with limit 10", len(elements))

	// Should find some elements but respect the visit limit
	if len(elements) == 0 {
		t.Error("expected to find some elements")
	}
}

func TestElementQuery_Matching(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element")
	}

	// Find elements with a custom predicate
	query := root.Descendants().Matching(func(e *axuiautomation.Element) bool {
		return strings.HasPrefix(e.Role(), "AXMenu")
	}).WithLimit(100)

	elements := query.AllElements()
	t.Logf("Found %d menu-related elements", len(elements))

	for _, el := range elements {
		if !strings.HasPrefix(el.Role(), "AXMenu") {
			t.Errorf("element role %q does not start with AXMenu", el.Role())
		}
	}
}

func TestElement_Exists(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		t.Fatal("expected root element")
	}

	if !root.Exists() {
		t.Error("expected root to exist")
	}

	// Test with nil element
	var nilElement *axuiautomation.Element
	if nilElement.Exists() {
		t.Error("expected nil element to not exist")
	}
}

func TestElement_Frame(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	// Get first window
	window := app.Windows().First()
	if window == nil {
		t.Skip("No Finder windows open")
	}

	frame := window.Frame()
	t.Logf("Window frame: origin=(%v,%v) size=(%v,%v)",
		frame.Origin.X, frame.Origin.Y, frame.Size.Width, frame.Size.Height)

	// Window should have positive size
	if frame.Size.Width <= 0 || frame.Size.Height <= 0 {
		t.Errorf("expected positive window size, got %vx%v", frame.Size.Width, frame.Size.Height)
	}
}

func TestElement_IsEnabled(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	menuBar := app.MenuBar()
	if menuBar == nil {
		t.Skip("Could not find menu bar")
	}

	// Menu bar items should be enabled
	fileMenu := menuBar.Descendants().ByRole("AXMenuBarItem").ByTitle("File").First()
	if fileMenu == nil {
		t.Skip("Could not find File menu")
	}

	if !fileMenu.IsEnabled() {
		t.Error("expected File menu to be enabled")
	}
}

func TestStringCache(t *testing.T) {
	// This test doesn't require accessibility - just exercises the cache
	// by creating and closing applications

	for i := 0; i < 3; i++ {
		app, err := axuiautomation.NewApplication("com.apple.finder")
		if err != nil {
			t.Skipf("Could not connect to Finder: %v", err)
		}

		root := app.Root()
		if root == nil {
			app.Close()
			continue
		}

		// Access various attributes to exercise the string cache
		// These may return empty strings without AX permissions, but that's OK
		_ = root.Role()
		_ = root.Title()
		_ = root.Description()
		_ = root.Identifier()

		app.Close()
	}
}

func TestApplication_Activate(t *testing.T) {
	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	// This uses AppleScript, not AX, so it doesn't need AX permissions
	err = app.Activate()
	if err != nil {
		t.Logf("Activate returned error (may be expected in CI): %v", err)
	}
}

func TestKeyboardEvents(t *testing.T) {
	// Just verify the keyboard functions don't panic
	// We don't actually send keys in tests to avoid side effects

	t.Run("SendEscape_initialized", func(t *testing.T) {
		// This will initialize keyboard events but we won't actually send
		// Just verify it doesn't panic during init
		err := axuiautomation.SendEscape()
		if err != nil {
			t.Logf("SendEscape error (may be expected): %v", err)
		}
	})
}

// Benchmark tests
func BenchmarkElementQuery_BFS(b *testing.B) {
	if !axuiautomation.IsProcessTrusted() {
		b.Skip("Skipping: accessibility permissions not granted")
	}

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		b.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		b.Fatal("expected root element")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = root.Descendants().WithLimit(100).AllElements()
	}
}

func BenchmarkElement_Role(b *testing.B) {
	if !axuiautomation.IsProcessTrusted() {
		b.Skip("Skipping: accessibility permissions not granted")
	}

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		b.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	root := app.Root()
	if root == nil {
		b.Fatal("expected root element")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = root.Role()
	}
}

// Example tests
func ExampleNewApplication() {
	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		return
	}
	defer app.Close()

	// Get the first window
	window := app.Windows().First()
	if window != nil {
		_ = window.Title()
	}
}

func ExampleElementQuery() {
	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		return
	}
	defer app.Close()

	// Find all buttons with "OK" in the title
	buttons := app.Buttons().ByTitleContains("OK").AllElements()
	for _, btn := range buttons {
		_ = btn.Title()
	}
}

// Test for observer (basic functionality)
func TestObserver_Create(t *testing.T) {
	skipIfNotTrusted(t)

	app, err := axuiautomation.NewApplication("com.apple.finder")
	if err != nil {
		t.Skipf("Could not connect to Finder: %v", err)
	}
	defer app.Close()

	observer, err := app.NewObserver()
	if err != nil {
		t.Skipf("Could not create observer: %v", err)
	}
	defer observer.Close()

	// Just verify we can create and close without panic
	observer.Start()
	time.Sleep(100 * time.Millisecond)
	observer.Stop()
}
