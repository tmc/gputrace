//go:build darwin

package cmd

import (
	"errors"
	"fmt"
	"time"
)

type fileExportProbeOps struct {
	open  func() error
	state func() (bool, bool, error)
	close func() error
}

func runFileExportProbe(ops fileExportProbeOps) (found, enabled bool, err error) {
	defer func() {
		if closeErr := ops.close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			found = false
			enabled = false
		}
	}()
	if err := ops.open(); err != nil {
		return false, false, err
	}
	return ops.state()
}

func fileExportMenuState(app, window uintptr) (found, enabled bool, err error) {
	var appPID, windowPID int32
	if axUIElementGetPid(app, &appPID) != kAXErrorSuccess ||
		axUIElementGetPid(window, &windowPID) != kAXErrorSuccess ||
		appPID == 0 || appPID != windowPID {
		return false, false, fmt.Errorf("File menu probe is not bound to the selected Xcode window")
	}
	if err := requireFocusedWindow(app, window); err != nil {
		return false, false, err
	}
	menuBar := findElementAtDepth(
		app,
		2,
		64,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXWindow"
		},
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXMenuBar"
		},
	)
	if menuBar == 0 {
		return false, false, fmt.Errorf("menubar not found")
	}
	fileMenu := findElementAtDepth(
		menuBar,
		2,
		64,
		axChildren,
		func(uintptr) bool { return false },
		func(element uintptr) bool {
			return axString(element, "AXTitle") == "File"
		},
	)
	if fileMenu == 0 {
		return false, false, fmt.Errorf("File menu not found")
	}
	return runFileExportProbe(fileExportProbeOps{
		open: func() error {
			expanded, err := axBoolAttribute(fileMenu, "AXExpanded")
			if err != nil {
				return fmt.Errorf("verify File menu before open: %w", err)
			}
			if expanded {
				if err := closeAXMenuForWindow(app, window, fileMenu); err != nil {
					return fmt.Errorf("close pre-existing File menu: %w", err)
				}
			}
			if err := axAction(fileMenu, "AXPress"); err != nil {
				return fmt.Errorf("open File menu: %w", err)
			}
			return waitForMenuOpen(fileMenu)
		},
		state: func() (bool, bool, error) {
			var matches []uintptr
			for _, item := range findAllMenuItems(fileMenu) {
				title := axString(item, "AXTitle")
				if title == "Export..." || title == "Export…" {
					matches = append(matches, item)
				}
			}
			switch len(matches) {
			case 0:
				return false, false, nil
			case 1:
				return true, IsElementEnabled(matches[0]), nil
			default:
				return false, false, fmt.Errorf("multiple File > Export menu items found")
			}
		},
		close: func() error {
			return closeAXMenuForWindow(app, window, fileMenu)
		},
	})
}

// waitForMenuOpen reports whether the menu actually began tracking.
//
// AXPress on a menu bar item returns kAXErrorSuccess whether or not a menu
// tracking session starts: the press is accepted, `AXExpanded` stays false, and
// no menu opens. Treating that success as "the menu is open" makes the caller
// enumerate the children of a menu that never opened and read a stale
// `AXEnabled` off them, so the export probe concludes Export is disabled and
// spends its whole attempt budget on an answer it can never revise. That
// presents as a timeout and never as a failure, which is why it went unnoticed.
//
// Establishing the press succeeded is therefore not the check; observing
// AXExpanded is.
func waitForMenuOpen(menu uintptr) error {
	var lastErr error
	for range 20 {
		expanded, err := axBoolAttribute(menu, "AXExpanded")
		if err == nil && expanded {
			return nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("verify File menu opened after AXPress: %w", lastErr)
	}
	return fmt.Errorf("File menu did not open after AXPress reported success (menu_open=false)")
}

// windowGeometryIdentity returns a pid-and-geometry fingerprint for a window,
// and reports whether it is usable. A zero-sized window yields no identity: a
// key built from zeros compares equal to every other such key, which would turn
// the scoping check below into one that cannot fail.
func windowGeometryIdentity(window uintptr) (string, bool) {
	var pid int32
	if axUIElementGetPid(window, &pid) != kAXErrorSuccess || pid == 0 {
		return "", false
	}
	if width, height := axSize(window); width <= 0 || height <= 0 {
		return "", false
	}
	return recoveryGeometryKeyForElement(window, int(pid)), true
}

// requireFocusedWindow refuses a File menu action that is not scoped to the
// selected Xcode window.
//
// Identity is normally the window-server id. `_AXUIElementGetWindow` is private
// and fails on a trace window while Xcode is building the Performance view: the
// element is live and correct, but its id is briefly unresolvable. Failing the
// export there is wrong, so fall back to a pid-and-geometry fingerprint.
//
// The fallback is deliberately the second choice. Two windows stacked at
// identical geometry would compare equal, so it is only reached when the strong
// check is unavailable, and it refuses to run at all on a window with no usable
// geometry rather than silently admitting everything.
func requireFocusedWindow(app, window uintptr) error {
	wantID, idErr := getWindowID(window)
	var wantKey string
	if idErr != nil {
		key, ok := windowGeometryIdentity(window)
		if !ok {
			return fmt.Errorf("read selected Xcode window identity: %w", idErr)
		}
		wantKey = key
	}
	for _, attr := range []string{"AXFocusedWindow", "AXMainWindow"} {
		var candidate uintptr
		key := mkString(attr)
		ret := axCopyAttributeValue(app, key, &candidate)
		cfRelease(key)
		if ret != kAXErrorSuccess || candidate == 0 {
			continue
		}
		var matched bool
		if idErr == nil {
			gotID, candidateErr := getWindowID(candidate)
			matched = candidateErr == nil && gotID == wantID
		} else if gotKey, ok := windowGeometryIdentity(candidate); ok {
			matched = gotKey == wantKey
		}
		cfRelease(candidate)
		if matched {
			return nil
		}
	}
	if idErr != nil {
		return fmt.Errorf("File menu operation is not scoped to the selected Xcode window "+
			"(window id unavailable: %v; geometry %s)", idErr, wantKey)
	}
	return fmt.Errorf("File menu operation is not scoped to the selected Xcode window %d", wantID)
}

type menuCloseOps struct {
	expanded func() (bool, error)
	cancel   func() error
	escape   func() error
}

func closeMenuWithOps(ops menuCloseOps) error {
	expanded, err := ops.expanded()
	if err == nil && !expanded {
		return nil
	}
	cancelErr := ops.cancel()
	for range 10 {
		expanded, err = ops.expanded()
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if !expanded {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		cancelErr = errors.Join(cancelErr,
			fmt.Errorf("verify menu after AXCancel: %w (menu_open=unknown)", err))
	}
	if ops.escape == nil {
		return errors.Join(cancelErr, fmt.Errorf("menu remained open after AXCancel (menu_open=true)"))
	}
	if err := ops.escape(); err != nil {
		return errors.Join(cancelErr, fmt.Errorf("close menu with scoped Escape: %w (menu_open=true)", err))
	}
	for range 10 {
		expanded, err = ops.expanded()
		if err != nil {
			return fmt.Errorf("verify menu after scoped Escape: %w (menu_open=unknown)", err)
		}
		if !expanded {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.Join(cancelErr, fmt.Errorf("menu remained open after scoped Escape (menu_open=true)"))
}

func closeAXMenu(menu uintptr) error {
	return closeMenuWithOps(menuCloseOps{
		expanded: func() (bool, error) { return axBoolAttribute(menu, "AXExpanded") },
		cancel:   func() error { return axAction(menu, "AXCancel") },
	})
}

func closeAXMenuForWindow(app, window, menu uintptr) error {
	return closeMenuWithOps(menuCloseOps{
		expanded: func() (bool, error) { return axBoolAttribute(menu, "AXExpanded") },
		cancel:   func() error { return axAction(menu, "AXCancel") },
		escape: func() error {
			if err := requireFocusedWindow(app, window); err != nil {
				return err
			}
			var appPID, windowPID int32
			if axUIElementGetPid(app, &appPID) != kAXErrorSuccess ||
				axUIElementGetPid(window, &windowPID) != kAXErrorSuccess ||
				appPID == 0 || appPID != windowPID {
				return fmt.Errorf("scoped Escape is not bound to the selected Xcode window")
			}
			if err := axAction(window, "AXRaise"); err != nil {
				return fmt.Errorf("raise selected Xcode window: %w", err)
			}
			if err := requireFocusedWindow(app, window); err != nil {
				return err
			}
			return sendKeyToPid(appPID, kVK_Escape, 0)
		},
	})
}
