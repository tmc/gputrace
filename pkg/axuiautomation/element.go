package axuiautomation

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/appledocs/generated/applicationservices"
	"github.com/tmc/appledocs/generated/corefoundation"
)

// Element represents an accessibility UI element.
type Element struct {
	ref applicationservices.AXUIElementRef
	app *Application
}

// newElement creates a new Element wrapping an AXUIElementRef.
// The element takes ownership of the ref and will release it when no longer needed.
func newElement(ref applicationservices.AXUIElementRef, app *Application) *Element {
	if ref == 0 {
		return nil
	}
	return &Element{ref: ref, app: app}
}

// newElementRetained creates a new Element, retaining the ref.
// Use this when the ref is owned by something else (e.g., an array).
func newElementRetained(ref applicationservices.AXUIElementRef, app *Application) *Element {
	if ref == 0 {
		return nil
	}
	corefoundation.CFRetain(corefoundation.CFTypeRef(ref))
	return &Element{ref: ref, app: app}
}

// Ref returns the underlying AXUIElementRef.
// The caller should not release this ref; it is owned by the Element.
func (e *Element) Ref() applicationservices.AXUIElementRef {
	if e == nil {
		return 0
	}
	return e.ref
}

// Release releases the underlying AXUIElementRef.
// After calling Release, the Element should not be used.
func (e *Element) Release() {
	if e != nil && e.ref != 0 {
		corefoundation.CFRelease(corefoundation.CFTypeRef(e.ref))
		e.ref = 0
	}
}

// Exists returns true if the element reference is valid.
func (e *Element) Exists() bool {
	if e == nil || e.ref == 0 {
		return false
	}
	// Check if we can get the role attribute
	role := e.Role()
	return role != ""
}

// Role returns the element's accessibility role (e.g., "AXButton", "AXWindow").
func (e *Element) Role() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXRole")
}

// Subrole returns the element's accessibility subrole.
func (e *Element) Subrole() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXSubrole")
}

// Title returns the element's title.
func (e *Element) Title() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXTitle")
}

// Description returns the element's description.
func (e *Element) Description() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXDescription")
}

// Identifier returns the element's unique identifier.
func (e *Element) Identifier() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXIdentifier")
}

// Value returns the element's value as a string.
func (e *Element) Value() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXValue")
}

// IsEnabled returns true if the element is enabled/interactive.
func (e *Element) IsEnabled() bool {
	if e == nil || e.ref == 0 {
		return false
	}
	return getAXAttributeBool(e.ref, "AXEnabled")
}

// IsFocused returns true if the element is focused.
func (e *Element) IsFocused() bool {
	if e == nil || e.ref == 0 {
		return false
	}
	return getAXAttributeBool(e.ref, "AXFocused")
}

// IsSelected returns true if the element is selected.
func (e *Element) IsSelected() bool {
	if e == nil || e.ref == 0 {
		return false
	}
	return getAXAttributeBool(e.ref, "AXSelected")
}

// IsChecked returns true if the element's value is truthy (for checkboxes/switches).
func (e *Element) IsChecked() bool {
	if e == nil || e.ref == 0 {
		return false
	}
	return getAXAttributeBool(e.ref, "AXValue")
}

// Position returns the element's position on screen.
func (e *Element) Position() (x, y int) {
	if e == nil || e.ref == 0 {
		return 0, 0
	}
	point, ok := getAXAttributePoint(e.ref, "AXPosition")
	if !ok {
		return 0, 0
	}
	return int(point.X), int(point.Y)
}

// Size returns the element's size.
func (e *Element) Size() (width, height int) {
	if e == nil || e.ref == 0 {
		return 0, 0
	}
	size, ok := getAXAttributeSize(e.ref, "AXSize")
	if !ok {
		return 0, 0
	}
	return int(size.Width), int(size.Height)
}

// Frame returns the element's frame (position + size).
func (e *Element) Frame() Rect {
	if e == nil || e.ref == 0 {
		return Rect{}
	}
	point, _ := getAXAttributePoint(e.ref, "AXPosition")
	size, _ := getAXAttributeSize(e.ref, "AXSize")
	return Rect{
		Origin: point,
		Size:   size,
	}
}

// Center returns the center point of the element.
func (e *Element) Center() (x, y int) {
	frame := e.Frame()
	return int(frame.Origin.X + frame.Size.Width/2),
		int(frame.Origin.Y + frame.Size.Height/2)
}

// Click performs a click on the element.
// It first tries AXPress, then falls back to CGEvent if AXPress fails.
func (e *Element) Click() error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}

	// Try AXPress first
	err := applicationservices.AXUIElementPerformAction(e.ref, axAttr("AXPress"))
	if int(err) == axErrorSuccess {
		return nil
	}

	// If AXPress failed with action unsupported or cannot complete, use CGEvent fallback
	if int(err) == axErrorActionUnsupported || int(err) == axErrorCannotComplete {
		x, y := e.Center()
		if x == 0 && y == 0 {
			return ErrElementNotFound
		}
		return cgEventClick(x, y)
	}

	return axErrorToGo(err)
}

// DoubleClick performs a double-click on the element.
func (e *Element) DoubleClick() error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}

	x, y := e.Center()
	if x == 0 && y == 0 {
		return ErrElementNotFound
	}
	return cgEventDoubleClick(x, y)
}

// PerformAction performs a named accessibility action on the element.
func (e *Element) PerformAction(action string) error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}
	err := applicationservices.AXUIElementPerformAction(e.ref, axAttr(action))
	return axErrorToGo(err)
}

// SetValue sets the element's value.
func (e *Element) SetValue(value string) error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}

	cStr := append([]byte(value), 0)
	cfValue := corefoundation.CFStringCreateWithCString(0, cStr, cfStringEncodingUTF8)
	if cfValue == 0 {
		return &Error{Message: "failed to create CFString"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(cfValue))

	err := applicationservices.AXUIElementSetAttributeValue(e.ref, axAttr("AXValue"), corefoundation.CFTypeRef(cfValue))
	return axErrorToGo(err)
}

// Focus sets focus to this element.
func (e *Element) Focus() error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}

	initCFBoolean()
	// Set AXFocused to true
	// Note: We need to get kCFBooleanTrue
	err := applicationservices.AXUIElementSetAttributeValue(e.ref, axAttr("AXFocused"), corefoundation.CFTypeRef(getCFBooleanTrue()))
	return axErrorToGo(err)
}

// SetPosition moves the element to the specified coordinates.
func (e *Element) SetPosition(x, y float64) error {
	if e == nil || e.ref == 0 {
		return ErrInvalidElement
	}

	point := Point{X: x, Y: y}
	axValue := applicationservices.AXValueCreate(applicationservices.AXValueType(axValueTypeCGPoint), unsafe.Pointer(&point))
	if axValue == 0 {
		return &Error{Message: "failed to create AXValue for position"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(axValue))

	err := applicationservices.AXUIElementSetAttributeValue(e.ref, axAttr("AXPosition"), corefoundation.CFTypeRef(axValue))
	return axErrorToGo(err)
}

var cfBooleanTrue uintptr
var cfBooleanFalse uintptr
var cfBooleanTrueOnce sync.Once

func getCFBooleanTrue() uintptr {
	cfBooleanTrueOnce.Do(func() {
		libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if sym, err := purego.Dlsym(libCF, "kCFBooleanTrue"); err == nil {
			cfBooleanTrue = *(*uintptr)(unsafe.Pointer(sym))
		}
		if sym, err := purego.Dlsym(libCF, "kCFBooleanFalse"); err == nil {
			cfBooleanFalse = *(*uintptr)(unsafe.Pointer(sym))
		}
	})
	return cfBooleanTrue
}

// Parent returns the parent element.
func (e *Element) Parent() *Element {
	if e == nil || e.ref == 0 {
		return nil
	}

	var value corefoundation.CFTypeRef
	err := applicationservices.AXUIElementCopyAttributeValue(e.ref, axAttr("AXParent"), &value)
	if int(err) != axErrorSuccess || value == 0 {
		return nil
	}

	return newElement(applicationservices.AXUIElementRef(value), e.app)
}

// Children returns the element's children.
func (e *Element) Children() []*Element {
	if e == nil || e.ref == 0 {
		return nil
	}

	refs := getAXAttributeElements(e.ref, "AXChildren")
	if len(refs) == 0 {
		return nil
	}

	result := make([]*Element, len(refs))
	for i, ref := range refs {
		result[i] = newElement(ref, e.app)
	}
	return result
}

// ChildCount returns the number of children.
func (e *Element) ChildCount() int {
	if e == nil || e.ref == 0 {
		return 0
	}

	var count int
	err := applicationservices.AXUIElementGetAttributeValueCount(e.ref, axAttr("AXChildren"), &count)
	if int(err) != axErrorSuccess {
		return 0
	}
	return count
}

// Query methods - start a new query rooted at this element

// Descendants returns a query for all descendants of this element.
func (e *Element) Descendants() *ElementQuery {
	return newElementQuery(e, e.app)
}

// Buttons returns a query for button elements.
func (e *Element) Buttons() *ElementQuery {
	return e.Descendants().ByRole("AXButton")
}

// Windows returns a query for window elements.
func (e *Element) Windows() *ElementQuery {
	return e.Descendants().ByRole("AXWindow")
}

// TextFields returns a query for text field elements.
func (e *Element) TextFields() *ElementQuery {
	return e.Descendants().ByRole("AXTextField")
}

// Checkboxes returns a query for checkbox elements.
func (e *Element) Checkboxes() *ElementQuery {
	return e.Descendants().ByRole("AXCheckBox")
}

// Groups returns a query for group elements.
func (e *Element) Groups() *ElementQuery {
	return e.Descendants().ByRole("AXGroup")
}

// StaticTexts returns a query for static text elements.
func (e *Element) StaticTexts() *ElementQuery {
	return e.Descendants().ByRole("AXStaticText")
}

// PopUpButtons returns a query for popup button elements.
func (e *Element) PopUpButtons() *ElementQuery {
	return e.Descendants().ByRole("AXPopUpButton")
}

// MenuItems returns a query for menu item elements.
func (e *Element) MenuItems() *ElementQuery {
	return e.Descendants().ByRole("AXMenuItem")
}

// Document returns the element's document URL (typically for windows).
func (e *Element) Document() string {
	if e == nil || e.ref == 0 {
		return ""
	}
	return getAXAttributeString(e.ref, "AXDocument")
}

// Raise raises the window to the front.
func (e *Element) Raise() error {
	return e.PerformAction("AXRaise")
}

// Application returns the parent Application for this element.
func (e *Element) Application() *Application {
	if e == nil {
		return nil
	}
	return e.app
}

// IntValue returns the element's value as an integer.
// Returns 0 if the value is not a number.
func (e *Element) IntValue() int {
	if e == nil || e.ref == 0 {
		return 0
	}
	// For most checkboxes and toggles, the value is stored as a string "0" or "1"
	value := e.Value()
	if value == "1" {
		return 1
	}
	return 0
}
