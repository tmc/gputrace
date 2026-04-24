package cmd

import (
	"fmt"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
)

// === Minimal AX/CoreFoundation Bindings ===

// ImageIO functions (loaded via purego since tmc/apple doesn't wrap ImageIO)
var (
	cgImageDestinationCreateWithURL func(uintptr, uintptr, int, uintptr) uintptr
	cgImageDestinationAddImage      func(uintptr, uintptr, uintptr)
	cgImageDestinationFinalize      func(uintptr) bool
	imageIOOnce                     sync.Once
)

// Global CFBoolean values
var (
	kCFBooleanTrue  = uintptr(corefoundation.KCFBooleanTrue)
	kCFBooleanFalse = uintptr(corefoundation.KCFBooleanFalse)
)

func ensureImageIO() {
	imageIOOnce.Do(func() {
		libImageIO, err := purego.Dlopen("/System/Library/Frameworks/ImageIO.framework/ImageIO", purego.RTLD_GLOBAL)
		if err == nil {
			purego.RegisterLibFunc(&cgImageDestinationCreateWithURL, libImageIO, "CGImageDestinationCreateWithURL")
			purego.RegisterLibFunc(&cgImageDestinationAddImage, libImageIO, "CGImageDestinationAddImage")
			purego.RegisterLibFunc(&cgImageDestinationFinalize, libImageIO, "CGImageDestinationFinalize")
		}
	})
}

// === AX functions ===

func axCreateApplication(pid int32) uintptr {
	return uintptr(axuiautomation.AXUIElementCreateApplication(pid))
}

func axCopyAttributeValue(element uintptr, attribute uintptr, value *uintptr) int32 {
	return int32(axuiautomation.AXUIElementCopyAttributeValue(
		axuiautomation.AXUIElementRef(element),
		attribute,
		value,
	))
}

func axSetAttributeValue(element uintptr, attribute uintptr, value uintptr) int32 {
	return int32(axuiautomation.AXUIElementSetAttributeValue(
		axuiautomation.AXUIElementRef(element),
		attribute,
		value,
	))
}

func axPerformAction(element uintptr, action uintptr) int32 {
	return int32(axuiautomation.AXUIElementPerformAction(
		axuiautomation.AXUIElementRef(element),
		action,
	))
}

func axUIElementGetPid(element uintptr, pid *int32) int32 {
	return int32(axuiautomation.AXUIElementGetPid(
		axuiautomation.AXUIElementRef(element),
		pid,
	))
}

func axValueGetValue(value uintptr, valueType int32, valuePtr unsafe.Pointer) bool {
	return axuiautomation.AXValueGetValue(
		axuiautomation.AXValueRef(value),
		axuiautomation.AXValueType(valueType),
		valuePtr,
	)
}

func axValueCreate(valueType int32, valuePtr unsafe.Pointer) uintptr {
	return uintptr(axuiautomation.AXValueCreate(
		axuiautomation.AXValueType(valueType),
		valuePtr,
	))
}

// === CF functions ===

func cfRelease(value uintptr) {
	corefoundation.CFRelease(corefoundation.CFTypeRef(value))
}

func cfArrayGetCount(array uintptr) int {
	return corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(array))
}

func cfArrayGetValueAtIndex(array uintptr, idx int) uintptr {
	return uintptr(corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(array), idx))
}

func cfStringGetLength(value uintptr) int {
	return corefoundation.CFStringGetLength(corefoundation.CFStringRef(value))
}

func cfStringGetCString(value uintptr, buffer unsafe.Pointer, bufferSize int, encoding uint32) bool {
	return corefoundation.CFStringGetCString(
		corefoundation.CFStringRef(value),
		(*byte)(buffer),
		bufferSize,
		encoding,
	)
}

func cfBooleanGetValue(value uintptr) bool {
	return corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(value))
}

func cfRetain(value uintptr) uintptr {
	return uintptr(corefoundation.CFRetain(corefoundation.CFTypeRef(value)))
}

func cfURLCreateWithFileSystemPath(allocator uintptr, filePath uintptr, pathStyle int32, isDirectory bool) uintptr {
	return uintptr(corefoundation.CFURLCreateWithFileSystemPath(
		corefoundation.CFAllocatorRef(allocator),
		corefoundation.CFStringRef(filePath),
		corefoundation.CFURLPathStyle(pathStyle),
		isDirectory,
	))
}

// === CG functions ===

func cgPreflightScreenCaptureAccess() bool {
	return coregraphics.CGPreflightScreenCaptureAccess()
}

func cgWindowListCopyWindowInfo(option uint32, relativeToWindow uint32) uintptr {
	return uintptr(coregraphics.CGWindowListCopyWindowInfo(
		coregraphics.CGWindowListOption(option),
		coregraphics.CGWindowID(relativeToWindow),
	))
}

func cgMainDisplayID() uint32 {
	return coregraphics.CGMainDisplayID()
}

func cgDisplayCreateImage(displayID uint32) uintptr {
	return uintptr(coregraphics.CGDisplayCreateImage(displayID))
}

func cgWindowListCreateImage(x, y, width, height float64, listOption uint32, windowID uint32, imageOption uint32) uintptr {
	return uintptr(coregraphics.CGWindowListCreateImage(
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: x, Y: y},
			Size:   corefoundation.CGSize{Width: width, Height: height},
		},
		coregraphics.CGWindowListOption(listOption),
		coregraphics.CGWindowID(windowID),
		coregraphics.CGWindowImageOption(imageOption),
	))
}

func cgImageRelease(image uintptr) {
	coregraphics.CGImageRelease(coregraphics.CGImageRef(image))
}

func cgEventCreateMouseEvent(source uintptr, eventType int32, x, y float64, button int32) uintptr {
	return uintptr(coregraphics.CGEventCreateMouseEvent(
		coregraphics.CGEventSourceRef(source),
		coregraphics.CGEventType(eventType),
		corefoundation.CGPoint{X: x, Y: y},
		coregraphics.CGMouseButton(button),
	))
}

func cgEventPost(tap int32, event uintptr) {
	coregraphics.CGEventPost(
		coregraphics.CGEventTapLocation(tap),
		coregraphics.CGEventRef(event),
	)
}

func cgEventSetIntegerValueField(event uintptr, field uint32, value int64) {
	coregraphics.CGEventSetIntegerValueField(
		coregraphics.CGEventRef(event),
		coregraphics.CGEventField(field),
		value,
	)
}

func cgEventGetDoubleValueField(event uintptr, field uint32) float64 {
	return coregraphics.CGEventGetDoubleValueField(
		coregraphics.CGEventRef(event),
		coregraphics.CGEventField(field),
	)
}

func cgEventCreate(source uintptr) uintptr {
	return uintptr(coregraphics.CGEventCreate(coregraphics.CGEventSourceRef(source)))
}

func cgWarpMouseCursorPosition(x, y float64) int32 {
	return int32(coregraphics.CGWarpMouseCursorPosition(corefoundation.CGPoint{X: x, Y: y}))
}

func cgEventCreateKeyboardEvent(source uintptr, keyCode uint16, keyDown bool) uintptr {
	return uintptr(coregraphics.CGEventCreateKeyboardEvent(
		coregraphics.CGEventSourceRef(source),
		keyCode,
		keyDown,
	))
}

func cgEventSetFlags(event uintptr, flags uint64) {
	coregraphics.CGEventSetFlags(coregraphics.CGEventRef(event), coregraphics.CGEventFlags(flags))
}

func cgEventPostToPid(pid int32, event uintptr) {
	coregraphics.CGEventPostToPid(pid, coregraphics.CGEventRef(event))
}

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
	kCGMouseEventClickState = 1
	kCGMouseEventX          = 56 // CGEventField for mouse X coordinate
	kCGMouseEventY          = 57 // CGEventField for mouse Y coordinate

	// CGEventTapLocation
	kCGHIDEventTap = 0

	// CGEventType for keyboard events
	kCGEventKeyDown = 10
	kCGEventKeyUp   = 11

	// CGEventFlags (modifier keys)
	kCGEventFlagMaskShift   = 0x00020000
	kCGEventFlagMaskCommand = 0x00100000

	// Virtual keycodes (macOS)
	kVK_A      = 0x00
	kVK_G      = 0x05
	kVK_Return = 0x24
	kVK_Escape = 0x35
)

// === Usage Helpers ===

func mkString(s string) uintptr {
	return uintptr(corefoundation.CFStringCreateWithCString(0, s, kCFStringEncodingUTF8))
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
	return axBool(el, "AXEnabled")
}

// axBool retrieves a boolean attribute from an AX element.
func axBool(el uintptr, attr string) bool {
	var val uintptr
	key := mkString(attr)
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

// axParent returns the parent element of an AX element.
func axParent(el uintptr) uintptr {
	var val uintptr
	key := mkString("AXParent")
	defer cfRelease(key)
	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		return val
	}
	return 0
}

// findParentWindow walks up the AX hierarchy to find the containing window.
func findParentWindow(el uintptr) uintptr {
	current := el
	for i := 0; i < 50; i++ { // Limit iterations to avoid infinite loops
		if current == 0 {
			return 0
		}
		role := axString(current, "AXRole")
		if role == "AXWindow" {
			return current
		}
		parent := axParent(current)
		if parent == 0 || parent == current {
			return 0
		}
		current = parent
	}
	return 0
}

// setWindowPosition moves a window to the specified position.
func setWindowPosition(window uintptr, x, y int) error {
	point := [2]float64{float64(x), float64(y)}
	val := axValueCreate(kAXValueTypeCGPoint, unsafe.Pointer(&point[0]))
	if val == 0 {
		return fmt.Errorf("failed to create AXValue for position")
	}
	defer cfRelease(val)

	key := mkString("AXPosition")
	defer cfRelease(key)

	err := axSetAttributeValue(window, key, val)
	if err != kAXErrorSuccess {
		return fmt.Errorf("failed to set window position: AX error %d", err)
	}
	return nil
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
	cgWarpMouseCursorPosition(x, y)
}

// clickElement simulates a single click on an AX element using CGEvent.
// It saves and restores the cursor position.
// This is useful as a fallback when AXPress fails with error -25205.
func clickElement(el uintptr) error {
	x, y := axPosition(el)
	w, h := axSize(el)

	verboseLog("clickElement: position=(%d,%d) size=(%d,%d)", x, y, w, h)

	// Check for off-screen position (negative coordinates or unreasonably large)
	if y < 0 || x < 0 || y > 10000 || x > 10000 {
		verboseLog("clickElement: WARNING - element appears off-screen, attempting to find parent window")
		// Try to find and reposition the parent window
		if window := findParentWindow(el); window != 0 {
			wx, wy := axPosition(window)
			verboseLog("clickElement: parent window at (%d,%d)", wx, wy)
			if wy < 0 || wx < 0 {
				// Reposition window to visible area
				newX, newY := 100, 100
				verboseLog("clickElement: repositioning window from (%d,%d) to (%d,%d)", wx, wy, newX, newY)
				if err := setWindowPosition(window, newX, newY); err != nil {
					verboseLog("clickElement: failed to reposition window: %v", err)
				} else {
					time.Sleep(200 * time.Millisecond)
					// Re-read element position after window move
					x, y = axPosition(el)
					w, h = axSize(el)
					verboseLog("clickElement: after reposition: position=(%d,%d) size=(%d,%d)", x, y, w, h)
				}
			}
		}
	}

	if w == 0 && h == 0 {
		// Try to get bounds via AXFrame attribute
		var frameVal uintptr
		frameKey := mkString("AXFrame")
		defer cfRelease(frameKey)
		if axCopyAttributeValue(el, frameKey, &frameVal) == kAXErrorSuccess {
			defer cfRelease(frameVal)
			// AXFrame is a CGRect struct
			var frame [4]float64
			if axValueGetValue(frameVal, 3, unsafe.Pointer(&frame[0])) { // 3 = kAXValueTypeCGRect
				x, y = int(frame[0]), int(frame[1])
				w, h = int(frame[2]), int(frame[3])
				verboseLog("clickElement: got frame from AXFrame: pos=(%d,%d) size=(%d,%d)", x, y, w, h)
			}
		}
	}

	if w == 0 && h == 0 {
		// Xcode 26+ SwiftUI toolbar buttons may not expose AXPosition/AXSize.
		// Walk up the AX hierarchy to find the nearest ancestor with valid bounds,
		// then click at its center (the ancestor typically wraps just the button).
		verboseLog("clickElement: element has no bounds, walking up hierarchy for clickable ancestor")
		parent := el
		for i := 0; i < 10; i++ {
			parent = axParent(parent)
			if parent == 0 {
				break
			}
			px, py := axPosition(parent)
			pw, ph := axSize(parent)
			parentRole := axString(parent, "AXRole")
			verboseLog("clickElement: ancestor[%d] role=%s pos=(%d,%d) size=(%d,%d)", i, parentRole, px, py, pw, ph)
			if pw > 0 && ph > 0 && px >= 0 && py >= 0 {
				x, y, w, h = px, py, pw, ph
				verboseLog("clickElement: using ancestor bounds: pos=(%d,%d) size=(%d,%d)", x, y, w, h)
				break
			}
		}
	}

	if w == 0 && h == 0 {
		return fmt.Errorf("could not get element bounds (position=%d,%d size=%d,%d)", x, y, w, h)
	}

	// Click at center of element
	cx := float64(x + w/2)
	cy := float64(y + h/2)

	// Create mouse events at element coordinates without moving the visible cursor.
	// CGEvent mouse events carry their own location — posting them directly delivers
	// the click to the target without a visible cursor jump.
	down := cgEventCreateMouseEvent(0, kCGEventLeftMouseDown, cx, cy, 0)
	if down == 0 {
		return fmt.Errorf("failed to create mouse down event")
	}
	defer cfRelease(down)

	up := cgEventCreateMouseEvent(0, kCGEventLeftMouseUp, cx, cy, 0)
	if up == 0 {
		return fmt.Errorf("failed to create mouse up event")
	}
	defer cfRelease(up)

	// Post events
	cgEventPost(kCGHIDEventTap, down)
	time.Sleep(50 * time.Millisecond)
	cgEventPost(kCGHIDEventTap, up)

	return nil
}

// doubleClickElement simulates a double-click on an AX element using CGEvent.
func doubleClickElement(el uintptr) error {
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

	return nil
}

func cfToString(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	// Guard: verify the ref is actually a CFString before calling CFStringGetLength.
	// AX attributes like AXValue may return NSNumber or other types where calling
	// CFStringGetLength triggers -[__NSCFNumber length]: unrecognized selector.
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(ref)) != corefoundation.CFStringGetTypeID() {
		return ""
	}
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

// axPressWithFallback tries AXPress first, and falls back to CGEvent click if AXPress fails.
// This handles the common -25205 (action not supported) error on some Xcode UI elements.
func axPressWithFallback(el uintptr) error {
	return axPressWithFallbackWindow(el, 0)
}

// axPressWithFallbackWindow tries AXPress first (which works without raising the window).
// If AXPress fails, it raises the window (if provided) and falls back to a CGEvent click.
// If CGEvent also fails (e.g. element has no bounds in Xcode 26), falls back to osascript.
//
// IMPORTANT: On Xcode 26, SwiftUI toolbar buttons return -25205 from AXPress but the
// action actually succeeds. We detect this by checking if the button disappeared after
// the "failed" press.
func axPressWithFallbackWindow(el uintptr, windowAX uintptr) error {
	title := axString(el, "AXTitle")
	desc := axString(el, "AXDescription")

	key := mkString("AXPress")
	defer cfRelease(key)
	err := axPerformAction(el, key)
	if err == kAXErrorSuccess {
		return nil
	}

	if err == -25205 || err == -25204 {
		// Xcode 26 quirk: SwiftUI toolbar buttons report -25205 but the action
		// actually fires. Wait and check if the UI state changed.
		time.Sleep(500 * time.Millisecond)

		postRole := axString(el, "AXRole")
		postEnabled := IsElementEnabled(el)
		verboseLog("axPressWithFallbackWindow: AXPress returned %d for %q/%q, post-check: role=%q enabled=%v",
			err, title, desc, postRole, postEnabled)

		if postRole == "" || postRole == "missing value" || !postEnabled {
			verboseLog("axPressWithFallbackWindow: button state changed, treating -25205 as success")
			return nil
		}

		// If the button has a name, check if it's gone from the tree (e.g. Replay disappears after click)
		if windowAX != 0 && title != "" && title != "missing value" {
			refound := findButtonBFS(windowAX, title, 500)
			if refound == 0 {
				verboseLog("axPressWithFallbackWindow: button %q no longer in window, treating as success", title)
				return nil
			}
		}

		// AXPress truly failed. Try AppleScript click first (most reliable on Xcode 26).
		verboseLog("axPressWithFallbackWindow: AXPress failed, trying AppleScript click for %q/%q", title, desc)
		if windowAX != 0 {
			ActivateXcode()
			time.Sleep(200 * time.Millisecond)
			axAction(windowAX, "AXRaise")
			time.Sleep(200 * time.Millisecond)
		}

		if osErr := clickButtonViaAppleScript(title, desc); osErr == nil {
			return nil
		} else {
			verboseLog("axPressWithFallbackWindow: AppleScript failed: %v, trying CGEvent", osErr)
		}

		if clickErr := clickElement(el); clickErr != nil {
			return fmt.Errorf("all click methods failed for %q/%q: AXPress=%d", title, desc, err)
		}
		return nil
	}

	return fmt.Errorf("AX error %d", err)
}

// clickButtonViaAppleScript clicks a button in the frontmost Xcode window using AppleScript.
// Uses `entire contents` + `first UI element whose role is "AXWindow"` which reliably
// resolves element positions on Xcode 26, even when the Go AX API returns (0,0).
func clickButtonViaAppleScript(title, description string) error {
	candidates := []string{}
	if title != "" && title != "missing value" {
		candidates = append(candidates, title)
	}
	if description != "" && description != "missing value" && description != title {
		candidates = append(candidates, description)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no title or description to match")
	}

	for _, name := range candidates {
		script := fmt.Sprintf(`
tell application "System Events"
	tell process "Xcode"
		set frontmost to true
		delay 0.3
		set w to first UI element whose role is "AXWindow"
		set allContents to entire contents of w
		repeat with elem in allContents
			try
				if role of elem is "AXButton" then
					if name of elem is "%s" or description of elem is "%s" then
						click elem
						return "ok"
					end if
				end if
			end try
		end repeat
		return "not found"
	end tell
end tell`, name, name)

		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		result := strings.TrimSpace(string(out))
		verboseLog("clickButtonViaAppleScript: name=%q result=%q err=%v", name, result, err)
		if err == nil && result == "ok" {
			return nil
		}
	}

	return fmt.Errorf("button not found via AppleScript (title=%q desc=%q)", title, description)
}

// axActionNames returns the list of actions supported by an element.
func axActionNames(ax uintptr) []string {
	var ptr uintptr
	if axuiautomation.AXUIElementCopyActionNames(axuiautomation.AXUIElementRef(ax), &ptr) != 0 {
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
			// Only match GPU-trace-specific Stop buttons, not toolbar Stop
			// GPU trace uses "Stop GPU workload" or similar
			if strings.HasPrefix(title, "Stop GPU") || strings.HasPrefix(desc, "Stop GPU") ||
				title == "Stop GPU workload" || desc == "Stop GPU workload" {
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

// FindTextField finds the first text field in an element tree.
func FindTextField(root uintptr) uintptr {
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		return role == "AXTextField"
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
	var windowID uint32
	if axuiautomation.AXUIElementGetWindow(axuiautomation.AXUIElementRef(windowAX), &windowID) != 0 {
		return 0, fmt.Errorf("failed to get window ID from AX element")
	}
	return windowID, nil
}

// CaptureWindowToFile captures a window by its AX element and saves to a PNG file.
func CaptureWindowToFile(windowAX uintptr, outputPath string) error {
	ensureImageIO()

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
	cgEventPostToPid(pid, keyDown)
	cgEventPostToPid(pid, keyUp)

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

// NavigateToFolderInSaveDialog navigates to a folder in a save dialog.
// Uses AX APIs to avoid stealing focus from the user.
// The window parameter should be the main window containing the save sheet.
func NavigateToFolderInSaveDialog(window uintptr, folderPath string) error {
	verboseLog("NavigateToFolderInSaveDialog: navigating to %s", folderPath)

	// Try Method 1: Find and set the path bar's combo box directly (no keyboard needed)
	pathBar := findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXComboBox" {
			// Look for the path bar combo box (usually has path-related description)
			desc := axString(el, "AXDescription")
			subrole := axString(el, "AXSubrole")
			// The path bar is typically an AXComboBox with AXPathButton subrole
			if subrole == "AXPathButton" || strings.Contains(strings.ToLower(desc), "path") ||
				strings.Contains(strings.ToLower(desc), "location") {
				return true
			}
		}
		return false
	})

	if pathBar != 0 {
		verboseLog("NavigateToFolderInSaveDialog: found path bar, setting value directly")
		if err := axSetValue(pathBar, folderPath); err == nil {
			// Confirm with Return key via AXConfirm or similar
			sleepMs(300)
			// Try to confirm the value
			axPerformAction(pathBar, mkString("AXConfirm"))
			sleepMs(500)
			return nil
		}
		verboseLog("NavigateToFolderInSaveDialog: direct path bar set failed, trying Cmd+Shift+G")
	}

	// Method 2: Use Cmd+Shift+G to open Go to Folder.
	// Ensure Xcode is frontmost and the window with the save dialog is raised —
	// CGEventPostToPid is unreliable for keyboard shortcuts in sheets.
	pid := getXcodePID()
	if pid == 0 {
		return fmt.Errorf("could not find Xcode PID")
	}

	ActivateXcode()
	sleepMs(200)
	axAction(window, "AXRaise")
	sleepMs(200)

	var goToSheet uintptr
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			verboseLog("NavigateToFolderInSaveDialog: retrying Cmd+Shift+G (attempt %d)", attempt+1)
			sleepMs(500)
		}

		// Try CGEventPost (frontmost app event queue) first, fall back to PID-targeted.
		verboseLog("NavigateToFolderInSaveDialog: sending Cmd+Shift+G (attempt %d, PID %d)", attempt+1, pid)
		if err := axuiautomation.SendCmdShiftG(); err != nil {
			verboseLog("NavigateToFolderInSaveDialog: SendCmdShiftG failed: %v, trying PID-targeted", err)
			if err := sendKeyToPid(pid, kVK_G, kCGEventFlagMaskCommand|kCGEventFlagMaskShift); err != nil {
				return fmt.Errorf("failed to send Cmd+Shift+G: %w", err)
			}
		}
		sleepMs(500)

		// Search the target window and all Xcode windows for the Go to Folder UI
		for i := 0; i < 30; i++ {
			sleepMs(100)
			goToSheet = findGoToFolderSheet(window)
			if goToSheet != 0 {
				verboseLog("NavigateToFolderInSaveDialog: found Go to Folder UI in target window")
				break
			}
			// Also search all Xcode windows (Go to Folder may appear in a floating panel)
			goToSheet = findGoToFolderInAllWindows()
			if goToSheet != 0 {
				verboseLog("NavigateToFolderInSaveDialog: found Go to Folder UI in another window")
				break
			}
		}
		if goToSheet != 0 {
			break
		}
		verboseLog("NavigateToFolderInSaveDialog: Go to Folder UI not found after attempt %d", attempt+1)
	}
	if goToSheet == 0 {
		return fmt.Errorf("Go to Folder UI did not appear")
	}

	// Find the path text field
	pathField := findElement(goToSheet, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" || role == "AXComboBox" {
			if axBool(el, "AXFocused") {
				return true
			}
		}
		return false
	})
	if pathField == 0 {
		pathField = findElement(goToSheet, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXComboBox"
		})
	}
	if pathField == 0 {
		pathField = FindTextField(goToSheet)
	}
	if pathField == 0 {
		return fmt.Errorf("path text field not found")
	}

	verboseLog("NavigateToFolderInSaveDialog: setting path: %s", folderPath)
	if err := axSetValue(pathField, folderPath); err != nil {
		return fmt.Errorf("failed to set folder path: %w", err)
	}
	sleepMs(300)

	// Click Go button or send Return to confirm the path
	goBtn := findButtonBFS(goToSheet, "Go", 100)
	if goBtn != 0 {
		verboseLog("NavigateToFolderInSaveDialog: clicking Go button")
		axPressWithFallback(goBtn)
	} else {
		// Send Return via CGEventPost (frontmost app) — sendKeyToPid is unreliable
		verboseLog("NavigateToFolderInSaveDialog: sending Return to confirm path")
		axuiautomation.SendReturn()
	}
	sleepMs(500)

	// The Go to Folder sheet (GoToWindow) is a folder browser that doesn't
	// auto-close after navigation. It blocks the Save button in the parent
	// save panel. Dismiss it via its Close button.
	dismissGoToFolderSheet(window)

	return nil
}

// dismissGoToFolderSheet finds and closes any lingering Go to Folder sheet.
func dismissGoToFolderSheet(window uintptr) {
	goToSheet := findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		ident := axString(el, "AXIdentifier")
		return role == "AXSheet" && ident == "GoToWindow"
	})
	if goToSheet == 0 {
		return // No Go to Folder sheet found — already dismissed
	}
	verboseLog("dismissGoToFolderSheet: found GoToWindow sheet, dismissing")

	// Try Close button first (id="CloseButton")
	closeBtn := findElement(goToSheet, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXButton" &&
			axString(el, "AXIdentifier") == "CloseButton"
	})
	if closeBtn != 0 {
		axPressWithFallback(closeBtn)
		sleepMs(300)
		return
	}

	// Fallback: send Escape
	axuiautomation.SendEscape()
	sleepMs(300)
}

// sendKeyToPid sends a key event directly to a process without changing focus.
func sendKeyToPid(pid int32, keyCode uint16, modifiers uint64) error {
	keyDown := cgEventCreateKeyboardEvent(0, keyCode, true)
	keyUp := cgEventCreateKeyboardEvent(0, keyCode, false)
	if keyDown == 0 || keyUp == 0 {
		return fmt.Errorf("failed to create keyboard events")
	}
	defer cfRelease(keyDown)
	defer cfRelease(keyUp)

	if modifiers != 0 {
		cgEventSetFlags(keyDown, modifiers)
		cgEventSetFlags(keyUp, modifiers)
	}

	cgEventPostToPid(pid, keyDown)
	sleepMs(50)
	cgEventPostToPid(pid, keyUp)

	return nil
}

// getXcodePID returns Xcode's process ID.
func getXcodePID() int32 {
	appAX, err := FindXcodeApp()
	if err != nil {
		return 0
	}
	defer cfRelease(appAX)

	var pid int32
	if axUIElementGetPid(appAX, &pid) == kAXErrorSuccess {
		return pid
	}
	return 0
}

// findGoToFolderInAllWindows searches all Xcode windows for the Go to Folder UI.
func findGoToFolderInAllWindows() uintptr {
	appAX, err := FindXcodeApp()
	if err != nil {
		return 0
	}
	defer cfRelease(appAX)
	for _, w := range GetAllWindows(appAX) {
		if sheet := findGoToFolderSheet(w); sheet != 0 {
			return sheet
		}
	}
	return 0
}

// findGoToFolderSheet finds the "Go to Folder" UI in a window.
// Modern macOS uses an inline text field in the path bar, not a separate sheet.
func findGoToFolderSheet(window uintptr) uintptr {
	// First try: Look for a sheet with "Go" button (older macOS style)
	sheet := findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXSheet" || role == "AXGroup" {
			goBtn := findButtonBFS(el, "Go", 50)
			if goBtn != 0 {
				return true
			}
		}
		return false
	})
	if sheet != 0 {
		return sheet
	}

	// Second try: Look for inline "Go to:" text field (modern macOS style)
	// This appears as a text field/combo box with "Go to:" label or a path-like value
	field := findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXTextField" || role == "AXComboBox" {
			// Check if this is focused (Go to field gets focus when Cmd+Shift+G is pressed)
			if axBool(el, "AXFocused") {
				return true
			}
			// Check for "Go to" or path-related descriptors
			desc := strings.ToLower(axString(el, "AXDescription"))
			placeholder := strings.ToLower(axString(el, "AXPlaceholderValue"))
			if strings.Contains(desc, "go to") || strings.Contains(desc, "folder") ||
				strings.Contains(placeholder, "go to") || strings.Contains(placeholder, "folder") ||
				strings.Contains(placeholder, "/") {
				return true
			}
			// Check value starts with "/" (path already entered)
			val := axString(el, "AXValue")
			if strings.HasPrefix(val, "/") || strings.HasPrefix(val, "~") {
				return true
			}
		}
		return false
	})
	if field != 0 {
		// Return the window as context since the field is inline
		return window
	}

	return 0
}
