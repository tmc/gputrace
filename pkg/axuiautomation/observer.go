package axuiautomation

import (
	"context"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/appledocs/generated/applicationservices"
	"github.com/tmc/appledocs/generated/corefoundation"
)

// Common AX notification names
const (
	NotificationFocusedUIElementChanged = "AXFocusedUIElementChanged"
	NotificationValueChanged            = "AXValueChanged"
	NotificationUIElementDestroyed      = "AXUIElementDestroyed"
	NotificationWindowCreated           = "AXWindowCreated"
	NotificationWindowMoved             = "AXWindowMoved"
	NotificationWindowResized           = "AXWindowResized"
	NotificationWindowMiniaturized      = "AXWindowMiniaturized"
	NotificationWindowDeminiaturized    = "AXWindowDeminiaturized"
	NotificationApplicationActivated    = "AXApplicationActivated"
	NotificationApplicationDeactivated  = "AXApplicationDeactivated"
	NotificationApplicationHidden       = "AXApplicationHidden"
	NotificationApplicationShown        = "AXApplicationShown"
	NotificationSelectedChildrenChanged = "AXSelectedChildrenChanged"
	NotificationSelectedTextChanged     = "AXSelectedTextChanged"
	NotificationTitleChanged            = "AXTitleChanged"
)

// ObserverEvent represents an accessibility notification event.
type ObserverEvent struct {
	Element      *Element
	Notification string
}

// ObserverHandler is a callback function for handling observer events.
type ObserverHandler func(event ObserverEvent)

// Observer provides event-based waiting for UI state changes.
type Observer struct {
	ref      applicationservices.AXObserverRef
	app      *Application
	pid      int32
	runLoop  *runLoopHelper
	handlers map[string][]ObserverHandler
	mu       sync.RWMutex
	running  bool
	events   chan ObserverEvent
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewObserver creates a new observer for the given application.
func NewObserver(app *Application) (*Observer, error) {
	if app == nil {
		return nil, ErrNotRunning
	}

	ctx, cancel := context.WithCancel(context.Background())

	obs := &Observer{
		app:      app,
		pid:      app.pid,
		runLoop:  newRunLoopHelper(),
		handlers: make(map[string][]ObserverHandler),
		events:   make(chan ObserverEvent, 100),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Create the AXObserver
	// Note: We need to use a callback-based approach here
	// For now, we'll use polling as a simpler fallback
	var ref applicationservices.AXObserverRef
	err := applicationservices.AXObserverCreate(app.pid, getObserverCallback(), &ref)
	if int(err) != axErrorSuccess {
		cancel()
		return nil, axErrorToGo(err)
	}

	obs.ref = ref

	// Add the observer's run loop source to the main run loop
	source := applicationservices.AXObserverGetRunLoopSource(ref)
	if source != 0 {
		obs.runLoop.addSource(uintptr(source))
	}

	// Register this observer for callback dispatch
	registerObserver(obs)

	return obs, nil
}

// Observer callback management
var (
	observerCallbackPtr  unsafe.Pointer
	observerCallbackOnce sync.Once
	observerRegistry     = make(map[int32]*Observer) // pid -> observer
	observerRegistryMu   sync.RWMutex
)

// getObserverCallback returns the C function pointer for the observer callback.
func getObserverCallback() applicationservices.AXObserverCallback {
	observerCallbackOnce.Do(func() {
		// Create a purego callback
		// The callback signature is: void (AXObserverRef, AXUIElementRef, CFStringRef, void*)
		callback := func(observer uintptr, element uintptr, notification uintptr, refcon uintptr) {
			// Get the PID from the observer
			var pid int32
			ref := applicationservices.AXUIElementRef(element)
			applicationservices.AXUIElementGetPid(ref, &pid)

			// Find the registered observer
			observerRegistryMu.RLock()
			obs := observerRegistry[pid]
			observerRegistryMu.RUnlock()

			if obs == nil {
				return
			}

			// Get notification name
			notifName := cfStringToGo(notification)

			// Create event
			corefoundation.CFRetain(corefoundation.CFTypeRef(element))
			event := ObserverEvent{
				Element:      newElement(ref, obs.app),
				Notification: notifName,
			}

			// Send to event channel (non-blocking)
			select {
			case obs.events <- event:
			default:
				// Channel full, drop event
				event.Element.Release()
			}
		}

		// Register the callback with purego
		observerCallbackPtr = unsafe.Pointer(purego.NewCallback(callback))
	})

	return applicationservices.AXObserverCallback(observerCallbackPtr)
}

func registerObserver(obs *Observer) {
	observerRegistryMu.Lock()
	defer observerRegistryMu.Unlock()
	observerRegistry[obs.pid] = obs
}

func unregisterObserver(obs *Observer) {
	observerRegistryMu.Lock()
	defer observerRegistryMu.Unlock()
	delete(observerRegistry, obs.pid)
}

// OnNotification registers a handler for a specific notification on an element.
func (o *Observer) OnNotification(name string, element *Element, handler ObserverHandler) error {
	if o.ref == 0 {
		return ErrInvalidElement
	}

	// Register with AXObserver
	err := applicationservices.AXObserverAddNotification(o.ref, element.ref, axAttr(name), nil)
	if int(err) != axErrorSuccess {
		return axErrorToGo(err)
	}

	// Add handler
	o.mu.Lock()
	o.handlers[name] = append(o.handlers[name], handler)
	o.mu.Unlock()

	return nil
}

// OnValueChanged registers a handler for value change notifications.
func (o *Observer) OnValueChanged(element *Element, handler ObserverHandler) error {
	return o.OnNotification(NotificationValueChanged, element, handler)
}

// OnUIElementDestroyed registers a handler for element destruction notifications.
func (o *Observer) OnUIElementDestroyed(element *Element, handler ObserverHandler) error {
	return o.OnNotification(NotificationUIElementDestroyed, element, handler)
}

// OnWindowCreated registers a handler for window creation notifications.
func (o *Observer) OnWindowCreated(handler ObserverHandler) error {
	return o.OnNotification(NotificationWindowCreated, o.app.root, handler)
}

// OnFocusChanged registers a handler for focus change notifications.
func (o *Observer) OnFocusChanged(handler ObserverHandler) error {
	return o.OnNotification(NotificationFocusedUIElementChanged, o.app.root, handler)
}

// Start starts processing events. Call this after registering all handlers.
func (o *Observer) Start() {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return
	}
	o.running = true
	o.mu.Unlock()

	// Start event dispatch goroutine
	go o.dispatchEvents()
}

// dispatchEvents processes events and calls handlers.
func (o *Observer) dispatchEvents() {
	for {
		select {
		case <-o.ctx.Done():
			return
		case event := <-o.events:
			o.mu.RLock()
			handlers := o.handlers[event.Notification]
			o.mu.RUnlock()

			for _, handler := range handlers {
				handler(event)
			}
		}
	}
}

// Stop stops processing events.
func (o *Observer) Stop() {
	o.cancel()
	o.mu.Lock()
	o.running = false
	o.mu.Unlock()
}

// Close releases all resources associated with the observer.
func (o *Observer) Close() {
	o.Stop()
	unregisterObserver(o)

	if o.runLoop != nil {
		o.runLoop.cleanup()
	}

	if o.ref != 0 {
		corefoundation.CFRelease(corefoundation.CFTypeRef(o.ref))
		o.ref = 0
	}
}

// WaitForElement waits for an element matching the query to appear.
func (o *Observer) WaitForElement(query *ElementQuery, timeout time.Duration) (*Element, error) {
	ctx, cancel := context.WithTimeout(o.ctx, timeout)
	defer cancel()

	// Poll for the element
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ErrTimeout
		case <-ticker.C:
			// Run the run loop briefly to process any pending events
			o.runLoop.runOnce(10 * time.Millisecond)

			// Check if element exists
			if elem := query.First(); elem != nil {
				return elem, nil
			}
		}
	}
}

// WaitForEnabled waits for an element to become enabled.
func (o *Observer) WaitForEnabled(element *Element, timeout time.Duration) error {
	if element == nil || element.ref == 0 {
		return ErrInvalidElement
	}

	ctx, cancel := context.WithTimeout(o.ctx, timeout)
	defer cancel()

	// Poll for enabled state
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-ticker.C:
			// Run the run loop briefly
			o.runLoop.runOnce(10 * time.Millisecond)

			if element.IsEnabled() {
				return nil
			}
		}
	}
}

// WaitForValueChange waits for an element's value to change.
func (o *Observer) WaitForValueChange(element *Element, timeout time.Duration) error {
	if element == nil || element.ref == 0 {
		return ErrInvalidElement
	}

	ctx, cancel := context.WithTimeout(o.ctx, timeout)
	defer cancel()

	initialValue := element.Value()

	// Poll for value change
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-ticker.C:
			// Run the run loop briefly
			o.runLoop.runOnce(10 * time.Millisecond)

			if element.Value() != initialValue {
				return nil
			}
		}
	}
}

// WaitForDisappear waits for an element to disappear (no longer exists).
func (o *Observer) WaitForDisappear(element *Element, timeout time.Duration) error {
	if element == nil || element.ref == 0 {
		return nil // Already gone
	}

	ctx, cancel := context.WithTimeout(o.ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-ticker.C:
			o.runLoop.runOnce(10 * time.Millisecond)

			if !element.Exists() {
				return nil
			}
		}
	}
}

// WaitWithCondition waits for a custom condition to be true.
func (o *Observer) WaitWithCondition(condition func() bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(o.ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-ticker.C:
			o.runLoop.runOnce(10 * time.Millisecond)

			if condition() {
				return nil
			}
		}
	}
}
