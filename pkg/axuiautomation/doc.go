// Package axuiautomation provides XCUIAutomation-style interfaces for macOS
// accessibility automation.
//
// This package wraps the macOS Accessibility APIs (AXUIElement, AXObserver, etc.)
// with a clean, Go-idiomatic interface inspired by Apple's XCUIAutomation framework.
//
// # Core Types
//
//   - Application: represents a running application
//   - Element: represents a UI element (button, window, etc.)
//   - ElementQuery: fluent API for finding elements
//   - Observer: event-based waiting for UI state changes
//
// # Example Usage
//
//	app, err := axuiautomation.NewApplication("com.apple.dt.Xcode")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer app.Close()
//
//	// Find and click a button
//	replayBtn := app.Windows().Buttons().ByTitle("Replay").Element(0)
//	if replayBtn.Exists() && replayBtn.IsEnabled() {
//	    replayBtn.Click()
//	}
//
//	// Wait for element with observer
//	observer, _ := app.NewObserver()
//	defer observer.Close()
//	err = observer.WaitForEnabled(replayBtn, 5*time.Minute)
//
// # Accessibility Permissions
//
// This package requires accessibility permissions. Use IsProcessTrusted() to
// check if your app has the required permissions.
package axuiautomation
