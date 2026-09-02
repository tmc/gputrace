//go:build darwin && gputrace_private_bindings

package counter

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/dvtinstrumentsfoundation"
)

// APSDataSource is the private DVT Instruments source for live APS samples.
// It is available only when built with gputrace_private_bindings.
type APSDataSource struct {
	source   dvtinstrumentsfoundation.DTGPUDataSource
	delegate objc.ID
}

// APSRawData is one payload delivered by DTGPUDataSource's delegate. Data is
// copied before the delegate method returns; the framework-owned buffer must
// not be retained by the caller.
type APSRawData struct {
	Data            []byte
	SampleCount     uint64
	SampleType      uint64
	RingBufferIndex uint32
	SourceIndex     uint32
	SourceID        objc.ID
}

// APSCounterProfileInfo describes a profile advertised by the active GPU
// data source. Profile selectors are private and device-specific, so callers
// should discover them here instead of hard-coding a numeric value.
type APSCounterProfileInfo struct {
	Profile uint64
	Name    string
	IsAPS   bool
}

// ErrAPSUnavailable reports that the host's private APS source cannot be
// instantiated. It is returned with APSUnavailableError when the framework
// provides a domain and code for the failure.
var ErrAPSUnavailable = errors.New("APS counter source is unavailable")

// APSUnavailableError preserves the NSError identity reported by
// GPURawCounter. Callers can use errors.Is(err, ErrAPSUnavailable) and
// errors.As(err, *APSUnavailableError) without parsing a diagnostic string.
type APSUnavailableError struct {
	Domain string
	Code   int64
	Detail string
}

func (e *APSUnavailableError) Error() string {
	if e == nil {
		return ErrAPSUnavailable.Error()
	}
	return fmt.Sprintf("counter: APS unavailable: %s (code %d): %s", e.Domain, e.Code, e.Detail)
}

func (e *APSUnavailableError) Unwrap() error { return ErrAPSUnavailable }

var apsDelegateClassID atomic.Uint64

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
	if sourceErr := rawCounterSourceGroupError(); sourceErr != nil {
		return sourceErr
	}
	p := dvtinstrumentsfoundation.DTGPUCounterProfileGPURawCountersAPSFromID(profile.GetID())
	config, err := rawCountersAPSConfig(profile)
	if err != nil {
		return err
	}
	p.SetAPSCounterConfig(config)
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

// rawCounterSourceGroupError asks GPURawCounter for the detailed source-group
// discovery error. ValidateAndConfigureRawCounters returns false without an
// NSError when discovery produces no groups, which otherwise hides the reason
// APS cannot be prepared on the host.
func rawCounterSourceGroupError() error {
	if os.Getenv("GPUTRACE_APS_PRELOAD_BUNDLE") != "" {
		const bundleImage = "/System/Library/Extensions/AGXGPURawCounterBundle.bundle/Contents/MacOS/AGXGPURawCounterBundle"
		if _, err := purego.Dlopen(bundleImage, purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
			return fmt.Errorf("counter: preload APS source-group bundle: %w", err)
		}
	}
	handle, err := purego.Dlopen("/System/Library/PrivateFrameworks/GPURawCounter.framework/GPURawCounter", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	symbol, err := purego.Dlsym(handle, "GRCCopyAllCounterSourceGroupWithError")
	if err != nil || symbol == 0 {
		return nil
	}
	var copyGroups func(*objc.ID) objc.ID
	purego.RegisterFunc(&copyGroups, symbol)
	var frameworkError objc.ID
	groups := copyGroups(&frameworkError)
	if frameworkError != 0 {
		return &APSUnavailableError{
			Domain: errorDomain(frameworkError),
			Code:   errorCode(frameworkError),
			Detail: objectDescription(frameworkError),
		}
	}
	if groups == 0 || !objc.RespondsToSelector(groups, objc.Sel("count")) || objc.Send[uint](groups, objc.Sel("count")) == 0 {
		return errors.New("counter: discover APS counter source groups: no groups")
	}
	return nil
}

// rawCountersAPSConfig returns the host-specific dictionary accepted by the
// APS profile's setAPSCounterConfig: selector. CounterProfileForHost returns
// an NSArray, even when it contains one configuration dictionary.
func rawCountersAPSConfig(profile objectivec.IObject) (objectivec.IObject, error) {
	if profile == nil || profile.GetID() == 0 {
		return nil, errors.New("counter: nil APS counter profile")
	}
	base := dvtinstrumentsfoundation.DTGPUCounterProfileFromID(profile.GetID())
	configs := base.CounterProfileForHost()
	if configs == nil || configs.GetID() == 0 {
		return nil, errors.New("counter: APS profile has no host configuration")
	}
	if !objc.RespondsToSelector(configs.GetID(), objc.Sel("count")) {
		return nil, errors.New("counter: APS host configuration is not an array")
	}
	array := foundation.NSArrayFromID(configs.GetID())
	for i := uint(0); i < array.Count(); i++ {
		candidate := array.ObjectAtIndex(i)
		if candidate.GetID() != 0 && objc.RespondsToSelector(candidate.GetID(), objc.Sel("objectForKeyedSubscript:")) {
			return candidate, nil
		}
	}
	return nil, errors.New("counter: APS host configuration has no dictionary")
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
	if !objc.RespondsToSelector(profile.GetID(), objc.Sel("sampleCounters:callback:")) {
		return errors.New("counter: APS profile does not support sample callback")
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
	config, err := rawCountersAPSConfig(profile)
	if err != nil {
		return err
	}
	dvtinstrumentsfoundation.DTGPUCounterProfileGPURawCountersAPSFromID(profile.GetID()).SetAPSCounterConfig(config)
	s.source.SetAPSCounterConfig(config)
	return nil
}

// SetDataCallback installs a delegate that copies raw APS payloads as the
// source makes them available. The callback runs on the source's work queue.
// Keep the APSDataSource value alive until Stop and GetRemainingData have
// completed so the delegate remains retained by the Go wrapper.
func (s *APSDataSource) SetDataCallback(callback func(APSRawData)) error {
	if s == nil || s.source.ID == 0 {
		return errors.New("counter: nil APS data source")
	}
	if callback == nil {
		return errors.New("counter: nil APS data callback")
	}

	className := fmt.Sprintf("GoAPSDataSourceDelegate_%d", apsDelegateClassID.Add(1))
	selector := objc.Sel("readyToSendData:sampleCount:length:dataSource:sampleType:ringBufferIndex:sourceIndex:")
	methods := []objc.MethodDef{{
		Cmd: selector,
		Fn: func(_ objc.ID, _ objc.SEL, data *uint64, count, length uint64, source objc.ID, sampleType uint64, ringBufferIndex, sourceIndex uint32) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			objc.AutoreleasePool(func() {
				var payload []byte
				if data != nil && length != 0 && length <= uint64(^uint(0)>>1) {
					payload = append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(data)), int(length))...)
				}
				callback(APSRawData{
					Data:            payload,
					SampleCount:     count,
					SampleType:      sampleType,
					RingBufferIndex: ringBufferIndex,
					SourceIndex:     sourceIndex,
					SourceID:        source,
				})
			})
		},
	}}
	protocol := objc.GetProtocol("DTGPUDataSourceDelegate")
	if protocol == nil {
		return errors.New("counter: DTGPUDataSourceDelegate protocol is unavailable")
	}
	var protocols []*objc.Protocol
	protocols = append(protocols, protocol)
	class, err := objc.RegisterClass(className, objc.GetClass("NSObject"), protocols, nil, methods)
	if err != nil {
		return fmt.Errorf("counter: register APS data delegate: %w", err)
	}
	delegate := objc.Send[objc.ID](objc.ID(class), objc.Sel("alloc"))
	delegate = objc.Send[objc.ID](delegate, objc.Sel("init"))
	if delegate == 0 {
		return errors.New("counter: create APS data delegate")
	}
	if s.delegate != 0 {
		s.source.SetDelegate(nil)
		releaseAPSDelegate(s.delegate)
	}
	objc.Send[struct{}](s.source.ID, objc.Sel("setDelegate:"), delegate)
	s.delegate = delegate
	return nil
}

func releaseAPSDelegate(delegate objc.ID) {
	if delegate != 0 {
		objc.Send[objc.ID](delegate, objc.Sel("release"))
	}
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

// SupportedCounterProfileInfo returns the device-advertised profile selectors
// and names. It reads only profiles reported by DTGPUDataSource and does not
// prepare or start sampling.
func (s APSDataSource) SupportedCounterProfileInfo() ([]APSCounterProfileInfo, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var result []APSCounterProfileInfo
	var err error
	objc.AutoreleasePool(func() {
		profiles, profilesErr := s.SupportedCounterProfiles()
		if profilesErr != nil {
			err = profilesErr
			return
		}
		result = make([]APSCounterProfileInfo, 0, len(profiles))
		for _, profile := range profiles {
			if profile == nil || profile.GetID() == 0 {
				continue
			}
			base := dvtinstrumentsfoundation.DTGPUCounterProfileFromID(profile.GetID())
			if !objc.RespondsToSelector(profile.GetID(), objc.Sel("profile")) {
				continue
			}
			info := APSCounterProfileInfo{Profile: base.Profile()}
			if objc.RespondsToSelector(profile.GetID(), objc.Sel("profileName")) {
				info.Name = base.ProfileName()
			}
			if objc.RespondsToSelector(profile.GetID(), objc.Sel("isAPS")) {
				info.IsAPS = base.IsAPS()
			}
			if info.Profile != 0 {
				result = append(result, info)
			}
		}
		if len(result) == 0 {
			err = errors.New("counter: APS data source reports no usable profile selectors")
		}
	})
	return result, err
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

func errorDomain(id objc.ID) string {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("domain")) {
		return "<unknown>"
	}
	domain := objc.Send[objc.ID](id, objc.Sel("domain"))
	if domain == 0 || !objc.RespondsToSelector(domain, objc.Sel("UTF8String")) {
		return "<unknown>"
	}
	cstr := objc.Send[*byte](domain, objc.Sel("UTF8String"))
	if cstr == nil {
		return "<unknown>"
	}
	return objc.GoString(cstr)
}

func errorCode(id objc.ID) int64 {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("code")) {
		return 0
	}
	return objc.Send[int64](id, objc.Sel("code"))
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
func (s *APSDataSource) Release() {
	if s == nil {
		return
	}
	if s.source.ID != 0 {
		s.Stop()
	}
	if s.delegate != 0 {
		if s.source.ID != 0 {
			s.source.SetDelegate(nil)
		}
		releaseAPSDelegate(s.delegate)
		s.delegate = 0
	}
	if s.source.ID != 0 {
		s.source.Release()
		s.source.ID = 0
	}
}
