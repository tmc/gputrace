//go:build darwin && gputrace_private_bindings

package counter

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/dvtinstrumentsfoundation"
)

// APSDataSource is the private DVT Instruments source for live APS samples.
// It is available only when built with gputrace_private_bindings.
type APSDataSource struct {
	source dvtinstrumentsfoundation.DTGPUDataSource
}

// NewAPSDataSourceWithDedicatedQueue creates a source with its own serial
// dispatch queue. The queue is retained by DTGPUDataSource for the source's
// lifetime.
func NewAPSDataSourceWithDedicatedQueue(device objc.ID, label string) (APSDataSource, error) {
	queue, err := newAPSQueue(label)
	if err != nil {
		return APSDataSource{}, err
	}
	source, err := NewAPSDataSource(device, queue)
	// dispatch_queue_create returns one caller-owned reference. The data source
	// retains the queue during initialization, so release that initial reference
	// on both success and failure.
	releaseAPSQueue(queue)
	if err != nil {
		return APSDataSource{}, err
	}
	return source, nil
}

func newAPSQueue(label string) (objc.ID, error) {
	handle, err := purego.Dlopen("/usr/lib/system/libdispatch.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return 0, fmt.Errorf("counter: load libdispatch: %w", err)
	}
	symbol, err := purego.Dlsym(handle, "dispatch_queue_create")
	if err != nil {
		return 0, fmt.Errorf("counter: resolve dispatch_queue_create: %w", err)
	}
	var create func(*byte, uintptr) uintptr
	purego.RegisterFunc(&create, symbol)
	name := label + "\x00"
	queue := create((*byte)(unsafe.Pointer(unsafe.StringData(name))), 0)
	if queue == 0 {
		return 0, errors.New("counter: create APS work queue")
	}
	return objc.ID(queue), nil
}

func releaseAPSQueue(queue objc.ID) {
	if queue == 0 {
		return
	}
	handle, err := purego.Dlopen("/usr/lib/system/libdispatch.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	symbol, err := purego.Dlsym(handle, "dispatch_release")
	if err != nil {
		return
	}
	var release func(uintptr)
	purego.RegisterFunc(&release, symbol)
	release(uintptr(queue))
}

// NewAPSDataSource creates an APS source for a Metal device and a caller-owned
// work queue. The queue must remain valid until Stop returns.
func NewAPSDataSource(device, workQueue objc.ID) (APSDataSource, error) {
	if device == 0 {
		return APSDataSource{}, errors.New("counter: nil Metal device")
	}
	if workQueue == 0 {
		return APSDataSource{}, errors.New("counter: nil APS work queue")
	}
	source := dvtinstrumentsfoundation.NewDTGPUDataSourceWithMTLDeviceWorkQueue(
		objectivec.ObjectFromID(device), objectivec.ObjectFromID(workQueue),
	)
	if source.ID == 0 {
		return APSDataSource{}, errors.New("counter: create APS data source")
	}
	return APSDataSource{source: source}, nil
}

// NewRawCountersAPSProfile creates the raw-counter APS profile for device.
//
// profile is a private framework profile selector. There is no exported
// DTGPUCounterProfile_GPURawCountersAPS symbol to derive it from, so prefer
// discovering a supported value with APSDataSource.SupportedCounterProfiles
// instead of hard-coding one.
func NewRawCountersAPSProfile(device objc.ID, profile uint64) (objectivec.IObject, error) {
	if device == 0 {
		return nil, errors.New("counter: nil Metal device")
	}
	value := dvtinstrumentsfoundation.NewDTGPUCounterProfile_GPURawCountersAPSWithDeviceProfile(
		objectivec.ObjectFromID(device), profile,
	)
	if value.ID == 0 {
		return nil, errors.New("counter: create APS counter profile")
	}
	return value, nil
}

// PrepareRawCountersAPS validates and prepares a raw APS profile for sampling.
func PrepareRawCountersAPS(profile objectivec.IObject) error {
	if profile == nil || profile.GetID() == 0 {
		return errors.New("counter: nil APS counter profile")
	}
	p := dvtinstrumentsfoundation.DTGPUCounterProfileGPURawCountersAPSFromID(profile.GetID())
	valid, err := p.ValidateAndConfigureRawCounters()
	if err != nil {
		return fmt.Errorf("counter: validate APS counter profile: %w", err)
	}
	if !valid {
		return errors.New("counter: validate APS counter profile")
	}
	base := dvtinstrumentsfoundation.DTGPUCounterProfileFromID(profile.GetID())
	if !base.Prepare() {
		return errors.New("counter: prepare APS counter profile")
	}
	return nil
}

// SampleRawCounters requests one raw-counter sample. The callback runs on the
// profile's work queue. The block is retained until the private framework
// invokes it, then released explicitly.
func SampleRawCounters(profile objectivec.IObject, counters uint64, callback func()) error {
	if profile == nil || profile.GetID() == 0 {
		return errors.New("counter: nil APS counter profile")
	}
	if callback == nil {
		return errors.New("counter: nil APS sample callback")
	}
	var release func()
	block, blockRelease := dvtinstrumentsfoundation.NewVoidBlock(func() {
		callback()
		if release != nil {
			release()
		}
	})
	release = blockRelease
	objc.Send[objc.ID](profile.GetID(), objc.Sel("sampleCounters:callback:"), counters, block)
	return nil
}

// SetCounterProfile selects the APS profile used by the source.
func (s APSDataSource) SetCounterProfile(profile objectivec.IObject) error {
	if s.source.ID == 0 {
		return errors.New("counter: nil APS data source")
	}
	if profile == nil || profile.GetID() == 0 {
		return errors.New("counter: nil APS counter profile")
	}
	s.source.SetAPSCounterConfig(profile)
	return nil
}

// SupportedCounterProfiles returns the counter profiles the source reports for
// its device. The private framework exposes no stable profile constant, so the
// supported set must be discovered from the source rather than assumed.
//
// The returned objects are owned by the source and are only valid while it is.
func (s APSDataSource) SupportedCounterProfiles() ([]objectivec.IObject, error) {
	if s.source.ID == 0 {
		return nil, errors.New("counter: nil APS data source")
	}
	array := s.source.SupportedCounterProfiles()
	if array.GetID() == 0 {
		return nil, errors.New("counter: APS data source reports no counter profiles")
	}
	count := array.Count()
	profiles := make([]objectivec.IObject, 0, count)
	for i := uint(0); i < count; i++ {
		profile := array.ObjectAtIndex(i)
		if profile.GetID() == 0 {
			continue
		}
		profiles = append(profiles, profile)
	}
	if len(profiles) == 0 {
		return nil, errors.New("counter: APS data source reports no counter profiles")
	}
	return profiles, nil
}

// Configure sets the source sampling configuration. The private selector
// returns an error object; a non-nil result means the configuration was
// rejected, so it is reported rather than discarded.
func (s APSDataSource) Configure(mode uint32, interval, windowLimit uint64) error {
	if s.source.ID == 0 {
		return errors.New("counter: nil APS data source")
	}
	if result := s.source.ConfigureIntervalWindowLimit(mode, interval, windowLimit); result != nil && result.GetID() != 0 {
		return fmt.Errorf("counter: configure APS data source: rejected with %s", objectDescription(result.GetID()))
	}
	return nil
}

// objectDescription returns an Objective-C object's -description as a Go
// string. It returns a placeholder when the description is unavailable so
// error paths never depend on the private framework returning a string.
func objectDescription(id objc.ID) string {
	if id == 0 {
		return "<nil>"
	}
	desc := objc.Send[objc.ID](id, objc.Sel("description"))
	if desc == 0 {
		return "<no description>"
	}
	cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
	if cstr == nil {
		return "<no description>"
	}
	return objc.GoString(cstr)
}

// Run starts sampling. The callback is invoked by the framework's work queue
// after GetRemainingData has drained the source.
func (s APSDataSource) Run() error {
	if s.source.ID == 0 {
		return errors.New("counter: nil APS data source")
	}
	if !s.source.Run() {
		return errors.New("counter: run APS data source")
	}
	return nil
}

// GetRemainingData arranges for callback to run after pending data is drained.
func (s APSDataSource) GetRemainingData(callback func()) error {
	if s.source.ID == 0 {
		return errors.New("counter: nil APS data source")
	}
	if callback == nil {
		return errors.New("counter: nil APS callback")
	}
	var release func()
	block, blockRelease := dvtinstrumentsfoundation.NewVoidBlock(func() {
		callback()
		if release != nil {
			release()
		}
	})
	release = blockRelease
	objc.Send[objc.ID](s.source.ID, objc.Sel("getRemainingData:"), block)
	return nil
}

// Stop stops sampling and releases the source's active collection state.
func (s APSDataSource) Stop() {
	if s.source.ID != 0 {
		s.source.Stop()
	}
}

// Release releases the Objective-C data source.
func (s APSDataSource) Release() {
	if s.source.ID != 0 {
		s.source.Release()
	}
}
