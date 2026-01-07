package cmd

import (
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// === Minimal AX/CoreFoundation Bindings ===

var (
	axCreateApplication              func(int32) uintptr
	axCopyAttributeValue             func(uintptr, uintptr, *uintptr) int32
	axCopyMultipleAttributeValues    func(uintptr, uintptr, int32, *uintptr) int32 // Batch API
	axSetAttributeValue              func(uintptr, uintptr, uintptr) int32
	axCopyAttributeNames             func(uintptr, *uintptr) int32
	axCopyActionNames                func(uintptr, *uintptr) int32
	axPerformAction                  func(uintptr, uintptr) int32
	axUIElementGetPid                func(uintptr, *int32) int32
	axValueGetValue                  func(uintptr, int32, unsafe.Pointer) bool
	axIsAttributeSettable            func(uintptr, uintptr, *bool) int32
	axUIElementGetWindow             func(uintptr, *uint32) int32 // _AXUIElementGetWindow (private but stable)

	cfStringCreateWithCString  func(uintptr, unsafe.Pointer, uint32) uintptr
	cfRelease                  func(uintptr)
	cfArrayGetCount            func(uintptr) int
	cfArrayGetValueAtIndex     func(uintptr, int) uintptr
	cfArrayCreate              func(uintptr, unsafe.Pointer, int64, uintptr) uintptr // For batch API
	cfStringGetLength         func(uintptr) int
	cfStringGetCString        func(uintptr, unsafe.Pointer, int, uint32) bool
	cfBooleanGetValue         func(uintptr) bool
	cfRetain                  func(uintptr) uintptr
	cfURLCreateWithFileSystemPath func(uintptr, uintptr, int32, bool) uintptr

	// Screen capture permission check (macOS 10.15+)
	cgPreflightScreenCaptureAccess func() bool
	cgWindowListCopyWindowInfo     func(uint32, uint32) uintptr
	cgMainDisplayID                func() uint32
	cgDisplayCreateImage           func(uint32) uintptr

	// CoreGraphics window capture
	// Note: CGRect passed as 4 separate float64 args (purego doesn't support arrays/structs by value)
	cgWindowListCreateImage func(
		/* CGRect.origin.x */ float64,
		/* CGRect.origin.y */ float64,
		/* CGRect.size.width */ float64,
		/* CGRect.size.height */ float64,
		/* CGWindowListOption */ uint32,
		/* CGWindowID */ uint32,
		/* CGWindowImageOption */ uint32,
	) uintptr
	cgImageDestinationCreateWithURL func(uintptr, uintptr, int, uintptr) uintptr
	cgImageDestinationAddImage      func(uintptr, uintptr, uintptr)
	cgImageDestinationFinalize      func(uintptr) bool
	cgImageRelease                  func(uintptr)

	// CGEvent for mouse clicks
	cgEventCreateMouseEvent      func(uintptr, int32, float64, float64, int32) uintptr
	cgEventPost                  func(int32, uintptr)
	cgEventSetIntegerValueField  func(uintptr, uint32, int64)
	cgEventGetDoubleValueField   func(uintptr, uint32) float64
	cgEventCreate                func(uintptr) uintptr
	cgWarpMouseCursorPosition    func(float64, float64) int32

	// CGEvent for keyboard events
	cgEventCreateKeyboardEvent func(uintptr, uint16, bool) uintptr
	cgEventSetFlags            func(uintptr, uint64)
	cgEventPostToPid           func(int32, uintptr) // Post to specific process

	// Global CFBoolean values
	kCFBooleanTrue  uintptr
	kCFBooleanFalse uintptr
)

const (
	kAXValueTypeCGPoint   = 1
	kAXValueTypeCGSize    = 2
	kCFStringEncodingUTF8 = 0x08000100
	kAXErrorSuccess       = 0

	// CGWindowListOption
	kCGWindowListOptionOnScreenOnly    = 1 << 0
	kCGWindowListOptionAll             = 0
	kCGWindowListOptionIncludingWindow = 1 << 3

	// CGWindowImageOption
	kCGWindowImageDefault             = 0
	kCGWindowImageBoundsIgnoreFraming = 1 << 0
	kCGWindowImageShouldBeOpaque      = 1 << 1
	kCGWindowImageOnlyShadows         = 1 << 2
	kCGWindowImageBestResolution      = 1 << 3
	kCGWindowImageNominalResolution   = 1 << 4

	// CFURL path style
	kCFURLPOSIXPathStyle = 0

	// CGEventType for mouse events
	kCGEventLeftMouseDown = 1
	kCGEventLeftMouseUp   = 2

	// CGEventField
	kCGMouseEventClickState    = 1
	kCGMouseEventX             = 56 // CGEventField for mouse X coordinate
	kCGMouseEventY             = 57 // CGEventField for mouse Y coordinate

	// CGEventTapLocation
	kCGHIDEventTap = 0

	// CGEventType for keyboard events
	kCGEventKeyDown = 10
	kCGEventKeyUp   = 11

	// CGEventFlags (modifier keys)
	kCGEventFlagMaskShift   = 0x00020000
	kCGEventFlagMaskCommand = 0x00100000

	// Virtual keycodes (macOS)
	kVK_G      = 0x05
	kVK_Return = 0x24
	kVK_Escape = 0x35
)

func initXCUI() {
	libAX, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_GLOBAL)
	if err == nil {
		purego.RegisterLibFunc(&axCreateApplication, libAX, "AXUIElementCreateApplication")
		purego.RegisterLibFunc(&axCopyAttributeValue, libAX, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&axCopyMultipleAttributeValues, libAX, "AXUIElementCopyMultipleAttributeValues")
		purego.RegisterLibFunc(&axSetAttributeValue, libAX, "AXUIElementSetAttributeValue")
		purego.RegisterLibFunc(&axCopyAttributeNames, libAX, "AXUIElementCopyAttributeNames")
		purego.RegisterLibFunc(&axCopyActionNames, libAX, "AXUIElementCopyActionNames")
		purego.RegisterLibFunc(&axPerformAction, libAX, "AXUIElementPerformAction")
		purego.RegisterLibFunc(&axUIElementGetPid, libAX, "AXUIElementGetPid")
		purego.RegisterLibFunc(&axValueGetValue, libAX, "AXValueGetValue")
		purego.RegisterLibFunc(&axIsAttributeSettable, libAX, "AXUIElementIsAttributeSettable")
	}

	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
	if err == nil {
		purego.RegisterLibFunc(&cfStringCreateWithCString, libCF, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfRelease, libCF, "CFRelease")
		purego.RegisterLibFunc(&cfArrayGetCount, libCF, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayGetValueAtIndex, libCF, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfArrayCreate, libCF, "CFArrayCreate")
		purego.RegisterLibFunc(&cfStringGetLength, libCF, "CFStringGetLength")
		purego.RegisterLibFunc(&cfStringGetCString, libCF, "CFStringGetCString")
		purego.RegisterLibFunc(&cfBooleanGetValue, libCF, "CFBooleanGetValue")
		purego.RegisterLibFunc(&cfRetain, libCF, "CFRetain")
		purego.RegisterLibFunc(&cfURLCreateWithFileSystemPath, libCF, "CFURLCreateWithFileSystemPath")

		// Get kCFBooleanTrue and kCFBooleanFalse global values
		if sym, err := purego.Dlsym(libCF, "kCFBooleanTrue"); err == nil {
			kCFBooleanTrue = *(*uintptr)(unsafe.Pointer(sym))
		}
		if sym, err := purego.Dlsym(libCF, "kCFBooleanFalse"); err == nil {
			kCFBooleanFalse = *(*uintptr)(unsafe.Pointer(sym))
		}
	}

	// CoreGraphics for window capture
	libCG, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL)
	if err == nil {
		purego.RegisterLibFunc(&cgWindowListCreateImage, libCG, "CGWindowListCreateImage")
		purego.RegisterLibFunc(&cgImageRelease, libCG, "CGImageRelease")
		purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, libCG, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, libCG, "CGWindowListCopyWindowInfo")
		purego.RegisterLibFunc(&cgMainDisplayID, libCG, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgDisplayCreateImage, libCG, "CGDisplayCreateImage")
	}

	// ImageIO for saving images
	libImageIO, err := purego.Dlopen("/System/Library/Frameworks/ImageIO.framework/ImageIO", purego.RTLD_GLOBAL)
	if err == nil {
		purego.RegisterLibFunc(&cgImageDestinationCreateWithURL, libImageIO, "CGImageDestinationCreateWithURL")
		purego.RegisterLibFunc(&cgImageDestinationAddImage, libImageIO, "CGImageDestinationAddImage")
		purego.RegisterLibFunc(&cgImageDestinationFinalize, libImageIO, "CGImageDestinationFinalize")
	}

	// Private but stable API for getting window ID from AX element
	// This is in HIServices framework (part of ApplicationServices)
	if libAX != 0 {
		// _AXUIElementGetWindow is not documented but widely used and stable
		purego.RegisterLibFunc(&axUIElementGetWindow, libAX, "_AXUIElementGetWindow")
	}

	// CGEvent for mouse events (part of CoreGraphics)
	if libCG != 0 {
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, libCG, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventPost, libCG, "CGEventPost")
		purego.RegisterLibFunc(&cgEventSetIntegerValueField, libCG, "CGEventSetIntegerValueField")
		purego.RegisterLibFunc(&cgEventGetDoubleValueField, libCG, "CGEventGetDoubleValueField")
		purego.RegisterLibFunc(&cgEventCreate, libCG, "CGEventCreate")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, libCG, "CGWarpMouseCursorPosition")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, libCG, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, libCG, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPostToPid, libCG, "CGEventPostToPid")
	}
}

// Ensure initialized
var xcuiInitOnce bool

func ensureXCUI() {
	if !xcuiInitOnce {
		initXCUI()
		xcuiInitOnce = true
	}
}

// === Usage Helpers ===

func mkString(s string) uintptr {
	b := make([]byte, len(s)+1)
	copy(b, s)
	b[len(s)] = 0
	return cfStringCreateWithCString(0, unsafe.Pointer(&b[0]), kCFStringEncodingUTF8)
}

func axString(ax uintptr, attr string) string {
	var ptr uintptr
	key := mkString(attr)
	defer cfRelease(key)

	if axCopyAttributeValue(ax, key, &ptr) == kAXErrorSuccess {
		defer cfRelease(ptr)
		return cfToString(ptr)
	}
	return ""
}

func IsElementEnabled(el uintptr) bool {
	var val uintptr
	key := mkString("AXEnabled")
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		return cfBooleanGetValue(val)
	}
	return false
}

// IsCheckboxChecked returns true if a checkbox element is checked.
func IsCheckboxChecked(el uintptr) bool {
	var val uintptr
	key := mkString("AXValue")
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		// AXValue for checkboxes is a CFNumber (0 or 1) or CFBoolean
		return cfBooleanGetValue(val)
	}
	return false
}

// axPosition returns the x,y position of an element.
func axPosition(el uintptr) (x, y int) {
	var val uintptr
	key := mkString("AXPosition")
	defer cfRelease(key)
	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		x, y = axValueToPoint(val)
	}
	return
}

// axSize returns the width,height of an element.
func axSize(el uintptr) (w, h int) {
	var val uintptr
	key := mkString("AXSize")
	defer cfRelease(key)
	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		w, h = axValueToSize(val)
	}
	return
}

// axValueToPoint extracts CGPoint from AXValue.
func axValueToPoint(val uintptr) (x, y int) {
	var point [2]float64
	if axValueGetValue(val, kAXValueTypeCGPoint, unsafe.Pointer(&point[0])) {
		x, y = int(point[0]), int(point[1])
	}
	return
}

// axValueToSize extracts CGSize from AXValue.
func axValueToSize(val uintptr) (w, h int) {
	var size [2]float64
	if axValueGetValue(val, kAXValueTypeCGSize, unsafe.Pointer(&size[0])) {
		w, h = int(size[0]), int(size[1])
	}
	return
}

// getCursorPosition returns the current mouse cursor position.
func getCursorPosition() (x, y float64) {
	if cgEventCreate == nil || cgEventGetDoubleValueField == nil {
		return 0, 0
	}
	ev := cgEventCreate(0)
	if ev == 0 {
		return 0, 0
	}
	defer cfRelease(ev)
	x = cgEventGetDoubleValueField(ev, kCGMouseEventX)
	y = cgEventGetDoubleValueField(ev, kCGMouseEventY)
	return x, y
}

// moveCursor moves the mouse cursor to the specified position.
func moveCursor(x, y float64) {
	if cgWarpMouseCursorPosition != nil {
		cgWarpMouseCursorPosition(x, y)
	}
}

// doubleClickElement simulates a double-click on an AX element using CGEvent.
// It saves and restores the cursor position.
func doubleClickElement(el uintptr) error {
	ensureXCUI()
	if cgEventCreateMouseEvent == nil {
		return fmt.Errorf("CGEvent not available")
	}

	// Save cursor position
	origX, origY := getCursorPosition()

	x, y := axPosition(el)
	w, h := axSize(el)

	if w == 0 || h == 0 {
		return fmt.Errorf("could not get element bounds")
	}

	// Click at center of element
	cx := float64(x + w/2)
	cy := float64(y + h/2)

	// First click
	down1 := cgEventCreateMouseEvent(0, kCGEventLeftMouseDown, cx, cy, 0)
	if down1 == 0 {
		return fmt.Errorf("failed to create mouse down event")
	}
	up1 := cgEventCreateMouseEvent(0, kCGEventLeftMouseUp, cx, cy, 0)
	if up1 == 0 {
		cfRelease(down1)
		return fmt.Errorf("failed to create mouse up event")
	}

	// Second click (set click count to 2 for double-click)
	down2 := cgEventCreateMouseEvent(0, kCGEventLeftMouseDown, cx, cy, 0)
	up2 := cgEventCreateMouseEvent(0, kCGEventLeftMouseUp, cx, cy, 0)
	if down2 != 0 {
		cgEventSetIntegerValueField(down2, kCGMouseEventClickState, 2)
	}
	if up2 != 0 {
		cgEventSetIntegerValueField(up2, kCGMouseEventClickState, 2)
	}

	// Post events
	cgEventPost(kCGHIDEventTap, down1)
	cgEventPost(kCGHIDEventTap, up1)
	if down2 != 0 && up2 != 0 {
		cgEventPost(kCGHIDEventTap, down2)
		cgEventPost(kCGHIDEventTap, up2)
	}

	// Release events
	cfRelease(down1)
	cfRelease(up1)
	if down2 != 0 {
		cfRelease(down2)
	}
	if up2 != 0 {
		cfRelease(up2)
	}

	// Restore cursor position
	if origX != 0 || origY != 0 {
		moveCursor(origX, origY)
	}

	return nil
}

func cfToString(ref uintptr) string {
	length := cfStringGetLength(ref)
	if length == 0 {
		return ""
	}
	buf := make([]byte, length*4+1)
	if cfStringGetCString(ref, unsafe.Pointer(&buf[0]), len(buf), kCFStringEncodingUTF8) {
		// Find null term
		for i, b := range buf {
			if b == 0 {
				return string(buf[:i])
			}
		}
		return string(buf)
	}
	return ""
}

// axChildrenWithError returns children and the AX error code.
// Error code 0 means success, -25211 means API disabled (no Accessibility permission).
func axChildrenWithError(ax uintptr) ([]uintptr, int32) {
	var ptr uintptr
	key := mkString("AXChildren")
	defer cfRelease(key)

	ret := axCopyAttributeValue(ax, key, &ptr)
	if ret != kAXErrorSuccess {
		return nil, ret
	}
	defer cfRelease(ptr)
	count := cfArrayGetCount(ptr)
	res := make([]uintptr, count)
	for i := 0; i < count; i++ {
		val := cfArrayGetValueAtIndex(ptr, i)
		res[i] = cfRetain(val)
	}
	return res, 0
}

func axChildren(ax uintptr) []uintptr {
	children, ret := axChildrenWithError(ax)
	if ret != kAXErrorSuccess {
		// Non-fatal errors like kAXErrorNotificationUnsupported (-25212)
		// are expected for certain UI elements; return empty slice silently.
		// Print debug info for API disabled error
		if ret == -25211 && collectProfileDebug {
			fmt.Printf("[DEBUG] axChildren: AXError %d (API disabled - no Accessibility permission)\n", ret)
		}
		return nil
	}
	return children
}

func axAction(ax uintptr, action string) error {
	key := mkString(action)
	defer cfRelease(key)
	err := axPerformAction(ax, key)
	if err != kAXErrorSuccess {
		return fmt.Errorf("AX error %d", err)
	}
	return nil
}

// axActionNames returns the list of actions supported by an element.
func axActionNames(ax uintptr) []string {
	var ptr uintptr
	if axCopyActionNames(ax, &ptr) != kAXErrorSuccess {
		return nil
	}
	defer cfRelease(ptr)

	count := cfArrayGetCount(ptr)
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		item := cfArrayGetValueAtIndex(ptr, i)
		names = append(names, cfToString(item))
	}
	return names
}

// === Xcode Specifics ===

func FindXcodeApp() (uintptr, error) {
	ensureXCUI()
	// Use osascript to get PID easily (simpler than iterating all procs in Go for now)
	out, err := exec.Command("pgrep", "-x", "Xcode").Output()
	if err != nil {
		return 0, fmt.Errorf("Xcode not running")
	}
	var pid int32
	fmt.Sscanf(string(out), "%d", &pid)

	app := axCreateApplication(pid)
	if app == 0 {
		return 0, fmt.Errorf("failed to create AX object for Xcode")
	}
	return app, nil
}

// === Menu Interactions ===

func ClickMenuItem(app uintptr, path []string) error {
	// Find Menu Bar
	menuBar := findElement(app, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXMenuBar"
	})
	if menuBar == 0 {
		return fmt.Errorf("menubar not found")
	}

	current := menuBar
	for _, name := range path {
		// Find child with title == name
		found := findElement(current, func(el uintptr) bool {
			return axString(el, "AXTitle") == name
		})
		if found == 0 {
			// Try "Export..." vs "Export…" fallback for last item
			if name == "Export..." || name == "Export…" {
				found = findElement(current, func(el uintptr) bool {
					t := axString(el, "AXTitle")
					return t == "Export..." || t == "Export…"
				})
			}
		}

		if found == 0 {
			return fmt.Errorf("menu item '%s' not found", name)
		}

		if err := axAction(found, "AXPress"); err != nil {
			return fmt.Errorf("failed to click '%s': %w", name, err)
		}

		current = found
	}
	return nil
}

func FindReplayButton(window uintptr) uintptr {
	// Recursive search for button with name "Replay"
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title == "Replay" || desc == "Replay" {
				return true
			}
		}
		return false
	})
}

func FindStopButton(window uintptr) uintptr {
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			// Match "Stop" or "Stop GPU workload"
			if title == "Stop" || desc == "Stop" ||
				strings.HasPrefix(title, "Stop GPU") || strings.HasPrefix(desc, "Stop GPU") {
				return true
			}
		}
		return false
	})
}

// FindShowPerformanceButton finds the "Show Performance" button (indicates profiling complete)
func FindShowPerformanceButton(window uintptr) uintptr {
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title == "Show Performance" || desc == "Show Performance" {
				return true
			}
		}
		return false
	})
}

// findElement BFS search
func findElement(root uintptr, match func(uintptr) bool) uintptr {
	queue := []uintptr{root}
	visited := 0

	// Safety limit
	maxVisit := 5000

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		visited++

		if match(el) {
			return el
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return 0
}

func GetFirstWindow(app uintptr) uintptr {
	children := axChildren(app)
	for _, child := range children {
		if axString(child, "AXRole") == "AXWindow" {
			return child
		}
	}
	return 0
}

// GetWindowByTitle finds a window whose title or document path contains the given substring (case-insensitive).
func GetWindowByTitle(app uintptr, titleSubstr string) uintptr {
	titleLower := strings.ToLower(titleSubstr)
	children := axChildren(app)
	for _, child := range children {
		if axString(child, "AXRole") == "AXWindow" {
			// Check AXTitle
			windowTitle := strings.ToLower(axString(child, "AXTitle"))
			if strings.Contains(windowTitle, titleLower) {
				return child
			}
			// Check AXDocument (file path)
			windowDoc := strings.ToLower(axString(child, "AXDocument"))
			if strings.Contains(windowDoc, titleLower) {
				return child
			}
		}
	}
	return 0
}

func FindExportButton(window uintptr) uintptr {
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title == "Export" || desc == "Export" {
				return true
			}
		}
		return false
	})
}

// axSetValue sets the AXValue attribute of an element to a string.
func axSetValue(el uintptr, value string) error {
	key := mkString("AXValue")
	defer cfRelease(key)
	val := mkString(value)
	defer cfRelease(val)

	ret := axSetAttributeValue(el, key, val)
	if ret != kAXErrorSuccess {
		return fmt.Errorf("AXSetAttributeValue failed: %d", ret)
	}
	return nil
}

// axFocus sets keyboard focus to an element.
func axFocus(el uintptr) error {
	key := mkString("AXFocused")
	defer cfRelease(key)

	if kCFBooleanTrue == 0 {
		return fmt.Errorf("kCFBooleanTrue not initialized")
	}
	ret := axSetAttributeValue(el, key, kCFBooleanTrue)
	if ret != kAXErrorSuccess {
		return fmt.Errorf("AXSetAttributeValue(AXFocused) failed: %d", ret)
	}
	return nil
}

// FindTextField finds the first text field in an element tree.
func FindTextField(root uintptr) uintptr {
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		return role == "AXTextField"
	})
}

// FindTextFieldByName finds a text field by its title or description.
func FindTextFieldByName(root uintptr, name string) uintptr {
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title == name || desc == name {
				return true
			}
		}
		return false
	})
}

// FindSheet finds the first sheet in an element tree.
func FindSheet(root uintptr) uintptr {
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		return role == "AXSheet"
	})
}

// FindAllTextFields finds all text fields in an element tree.
func FindAllTextFields(root uintptr, maxVisit int) []uintptr {
	var fields []uintptr
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXTextField" || role == "AXComboBox" {
			fields = append(fields, el)
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return fields
}

// DebugTextFields prints all text fields found in the window for debugging.
func DebugTextFields(root uintptr) {
	fields := FindAllTextFields(root, 500)
	fmt.Printf("    [DEBUG] Found %d text fields/comboboxes:\n", len(fields))
	for i, f := range fields {
		role := axString(f, "AXRole")
		title := axString(f, "AXTitle")
		desc := axString(f, "AXDescription")
		value := axString(f, "AXValue")
		identifier := axString(f, "AXIdentifier")
		fmt.Printf("      %d. role=%s title=%q desc=%q id=%q value=%q\n",
			i+1, role, title, desc, identifier, truncate(value, 50))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// sleepMs sleeps for the specified number of milliseconds.
func sleepMs(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// FindTextFieldByIdentifier finds a text field by its AXIdentifier.
func FindTextFieldByIdentifier(root uintptr, id string) uintptr {
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" || role == "AXComboBox" {
			identifier := axString(el, "AXIdentifier")
			if identifier == id {
				return true
			}
		}
		return false
	})
}

// FindSaveAsTextField finds the "Save As" text field in a save dialog.
func FindSaveAsTextField(root uintptr) uintptr {
	// First try by identifier
	if field := FindTextFieldByIdentifier(root, "saveAsNameTextField"); field != 0 {
		return field
	}
	// Fallback: look for text field with description containing "save"
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" {
			desc := axString(el, "AXDescription")
			if strings.Contains(strings.ToLower(desc), "save") {
				return true
			}
		}
		return false
	})
}

// FindPathTextField finds the path text field in a Go To Folder dialog.
func FindPathTextField(root uintptr) uintptr {
	// First try by identifier
	if field := FindTextFieldByIdentifier(root, "PathTextField"); field != 0 {
		return field
	}
	// Fallback: look for text field with description containing "folder" or "path"
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" || role == "AXComboBox" {
			desc := strings.ToLower(axString(el, "AXDescription"))
			if strings.Contains(desc, "folder") || strings.Contains(desc, "path") {
				return true
			}
		}
		return false
	})
}

// getWindowID extracts the CGWindowID from an AXUIElement (window).
func getWindowID(windowAX uintptr) (uint32, error) {
	if axUIElementGetWindow == nil {
		return 0, fmt.Errorf("_AXUIElementGetWindow not available")
	}
	var windowID uint32
	if axUIElementGetWindow(windowAX, &windowID) != 0 {
		return 0, fmt.Errorf("failed to get window ID from AX element")
	}
	return windowID, nil
}

// CaptureWindowToFile captures a window by its AX element and saves to a PNG file.
func CaptureWindowToFile(windowAX uintptr, outputPath string) error {
	if cgWindowListCreateImage == nil {
		return fmt.Errorf("CGWindowListCreateImage not available")
	}

	// Get the window ID from AX element
	windowID, err := getWindowID(windowAX)
	if err != nil {
		return err
	}

	verboseLog("CaptureWindowToFile: windowID=%d", windowID)

	// Capture the window using CGRectNull which tells it to use window's natural bounds
	// CGRectNull is defined as {{INFINITY, INFINITY}, {0, 0}}
	// Using kCGWindowListOptionIncludingWindow to capture just this window
	var image uintptr

	// Try with best resolution first
	image = cgWindowListCreateImage(
		math.Inf(1), math.Inf(1), 0, 0, // CGRectNull
		kCGWindowListOptionIncludingWindow,
		windowID,
		kCGWindowImageBoundsIgnoreFraming|kCGWindowImageBestResolution,
	)
	if image == 0 {
		// Try without best resolution
		verboseLog("CaptureWindowToFile: first attempt failed, trying without BestResolution")
		image = cgWindowListCreateImage(
			math.Inf(1), math.Inf(1), 0, 0, // CGRectNull
			kCGWindowListOptionIncludingWindow,
			windowID,
			kCGWindowImageBoundsIgnoreFraming,
		)
	}
	if image == 0 {
		// Try with nominal resolution
		verboseLog("CaptureWindowToFile: second attempt failed, trying with NominalResolution")
		image = cgWindowListCreateImage(
			math.Inf(1), math.Inf(1), 0, 0, // CGRectNull
			kCGWindowListOptionIncludingWindow,
			windowID,
			kCGWindowImageNominalResolution,
		)
	}
	if image == 0 {
		return fmt.Errorf("CGWindowListCreateImage returned null (windowID=%d) - check Screen Recording permission in System Preferences > Privacy & Security", windowID)
	}
	defer cgImageRelease(image)

	// Create CFURL for the output path
	pathStr := mkString(outputPath)
	defer cfRelease(pathStr)

	fileURL := cfURLCreateWithFileSystemPath(0, pathStr, kCFURLPOSIXPathStyle, false)
	if fileURL == 0 {
		return fmt.Errorf("failed to create file URL")
	}
	defer cfRelease(fileURL)

	// Create image type string (public.png)
	pngType := mkString("public.png")
	defer cfRelease(pngType)

	// Create image destination
	dest := cgImageDestinationCreateWithURL(fileURL, pngType, 1, 0)
	if dest == 0 {
		return fmt.Errorf("failed to create image destination")
	}
	defer cfRelease(dest)

	// Add the image
	cgImageDestinationAddImage(dest, image, 0)

	// Finalize (write to disk)
	if !cgImageDestinationFinalize(dest) {
		return fmt.Errorf("failed to finalize image")
	}

	return nil
}

// getXcodePid returns Xcode's process ID.
func getXcodePid() (int32, error) {
	out, err := exec.Command("pgrep", "-x", "Xcode").Output()
	if err != nil {
		return 0, fmt.Errorf("Xcode not running")
	}
	var pid int32
	fmt.Sscanf(string(out), "%d", &pid)
	return pid, nil
}

// sendKeyWithModifiers sends a key press with optional modifier flags (Cmd, Shift, etc).
// If targetPid is non-zero, posts the event directly to that process.
func sendKeyWithModifiers(keyCode uint16, modifiers uint64) error {
	ensureXCUI()
	if cgEventCreateKeyboardEvent == nil || cgEventSetFlags == nil {
		return fmt.Errorf("CGEvent keyboard functions not available")
	}

	// Get Xcode PID for targeted posting
	pid, err := getXcodePid()
	if err != nil {
		return err
	}

	// Create key down event
	keyDown := cgEventCreateKeyboardEvent(0, keyCode, true)
	if keyDown == 0 {
		return fmt.Errorf("failed to create key down event")
	}
	defer cfRelease(keyDown)

	// Create key up event
	keyUp := cgEventCreateKeyboardEvent(0, keyCode, false)
	if keyUp == 0 {
		return fmt.Errorf("failed to create key up event")
	}
	defer cfRelease(keyUp)

	// Set modifier flags on both events
	if modifiers != 0 {
		cgEventSetFlags(keyDown, modifiers)
		cgEventSetFlags(keyUp, modifiers)
	}

	// Post events directly to Xcode's PID
	if cgEventPostToPid != nil {
		cgEventPostToPid(pid, keyDown)
		cgEventPostToPid(pid, keyUp)
	} else {
		// Fallback to global post
		cgEventPost(kCGHIDEventTap, keyDown)
		cgEventPost(kCGHIDEventTap, keyUp)
	}

	return nil
}

// sendCmdShiftG sends Cmd+Shift+G (Go to Folder shortcut in save dialogs).
// Uses AppleScript System Events for reliable keystroke delivery to the focused window.
func sendCmdShiftG() error {
	script := `tell application "System Events"
	tell process "Xcode"
		keystroke "g" using {command down, shift down}
	end tell
end tell`
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		// Fallback to CGEvent if AppleScript fails
		return sendKeyWithModifiers(kVK_G, kCGEventFlagMaskCommand|kCGEventFlagMaskShift)
	}
	return nil
}

// sendReturn sends the Return/Enter key using AppleScript.
func sendReturn() error {
	script := `tell application "System Events"
	tell process "Xcode"
		keystroke return
	end tell
end tell`
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return sendKeyWithModifiers(kVK_Return, 0)
	}
	return nil
}

// sendEscape sends the Escape key using AppleScript.
func sendEscape() error {
	script := `tell application "System Events"
	tell process "Xcode"
		key code 53
	end tell
end tell`
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return sendKeyWithModifiers(kVK_Escape, 0)
	}
	return nil
}

// ActivateXcode brings Xcode to the front using AppleScript.
func ActivateXcode() error {
	cmd := exec.Command("osascript", "-e", `tell application "Xcode" to activate`)
	return cmd.Run()
}

// NavigateToFolderInSaveDialog uses Cmd+Shift+G to open the "Go to Folder" dialog
// in a save sheet, enters the path, and presses Return to navigate there.
// The window parameter should be the main window containing the save sheet.
func NavigateToFolderInSaveDialog(window uintptr, folderPath string) error {
	// Bring Xcode to front to receive keyboard events
	if err := ActivateXcode(); err != nil {
		return fmt.Errorf("failed to activate Xcode: %w", err)
	}
	sleepMs(300)

	// Send Cmd+Shift+G to open "Go to Folder"
	if err := sendCmdShiftG(); err != nil {
		return fmt.Errorf("failed to send Cmd+Shift+G: %w", err)
	}

	// Wait for the "Go to Folder" sheet to appear
	// It contains a text field where we can enter the path
	var goToSheet uintptr
	for i := 0; i < 20; i++ {
		sleepMs(100)
		// Look for the "Go to the folder" text field (it's typically a ComboBox)
		goToSheet = findGoToFolderSheet(window)
		if goToSheet != 0 {
			break
		}
	}
	if goToSheet == 0 {
		return fmt.Errorf("Go to Folder sheet did not appear")
	}

	// Find the text field in the sheet
	pathField := FindTextField(goToSheet)
	if pathField == 0 {
		// Try finding ComboBox (alternate UI)
		pathField = findElement(goToSheet, func(el uintptr) bool {
			role := axString(el, "AXRole")
			return role == "AXComboBox"
		})
	}
	if pathField == 0 {
		return fmt.Errorf("path text field not found in Go to Folder sheet")
	}

	// Set the path
	if err := axSetValue(pathField, folderPath); err != nil {
		return fmt.Errorf("failed to set folder path: %w", err)
	}

	// Small delay to let the UI update
	sleepMs(200)

	// Press Return to navigate
	if err := sendReturn(); err != nil {
		return fmt.Errorf("failed to send Return: %w", err)
	}

	// Wait for the dialog to close and navigation to complete
	sleepMs(500)

	return nil
}

// findGoToFolderSheet finds the "Go to Folder" sheet in a window.
func findGoToFolderSheet(window uintptr) uintptr {
	// The "Go to Folder" dialog appears as a sheet with a text field
	// Look for a sheet containing text "Go to the folder"
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXSheet" || role == "AXGroup" {
			// Check for "Go" button which indicates this is the Go to Folder dialog
			goBtn := findButtonBFS(el, "Go", 50)
			if goBtn != 0 {
				return true
			}
		}
		return false
	})
}
