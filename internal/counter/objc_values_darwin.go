//go:build darwin

package counter

import (
	"fmt"
	"runtime"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// CounterSeries returns the samples of a GTMioCounterData as Go-owned numbers.
//
// The values and timestamps selectors return bare pointers, not NSArrays of
// NSNumbers, so the element width is not visible in the Go signature. The
// framework does record it: ValuesSlice and TimestampsSlice read the selector's
// runtime type encoding and refuse anything that is not a pointer to double and
// to a 64-bit integer respectively. That is the check to rely on. It catches an
// integer series read as float64 whatever the samples happen to be, where the
// non-finite-exponent test this used to do only catches the ones that decode to
// a NaN or an infinity.
func CounterSeries(cnt gtshaderprofiler.GTMioCounterData) (values []float64, timestamps []uint64, err error) {
	n := cnt.SampleCount()
	if n == 0 {
		return nil, nil, nil
	}
	if n > maxCounterSamples {
		return nil, nil, fmt.Errorf("counter %q reports %d samples, past the %d sanity limit",
			cnt.Name(), n, maxCounterSamples)
	}
	values, err = cnt.ValuesSlice()
	if err != nil {
		return nil, nil, fmt.Errorf("counter %q values (valueType %d): %w", cnt.Name(), cnt.ValueType(), err)
	}
	timestamps, err = cnt.TimestampsSlice()
	if err != nil {
		return nil, nil, fmt.Errorf("counter %q timestamps: %w", cnt.Name(), err)
	}
	if len(values) != len(timestamps) {
		return nil, nil, fmt.Errorf("counter %q has %d values but %d timestamps",
			cnt.Name(), len(values), len(timestamps))
	}
	return values, timestamps, nil
}

// maxCounterSamples bounds a sampleCount before it is used as a slice length.
// The count comes from an unowned pointer's object; a wrong read of it would
// otherwise map gigabytes of arbitrary address space. The framework bindings
// apply their own ceiling; this one is tighter, and is what the counters seen
// here have to fit under.
const maxCounterSamples = 1 << 24

// CounterDataValues converts a GTMioCounterData object to Go-owned numbers,
// discarding its timestamps. See [CounterSeries] for how the element width is
// checked.
func CounterDataValues(data objc.ID) ([]float64, error) {
	if data == 0 {
		return nil, fmt.Errorf("counter data is nil")
	}
	var values []float64
	var err error
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		values, _, err = CounterSeries(gtshaderprofiler.GTMioCounterDataFromID(data))
	})
	return values, err
}

// AppendCounterDataSamples converts a GTMioCounterData object and appends its
// values to a replay result. Timestamps are intentionally left unset: the
// caller must associate them with the corresponding APS sample boundary.
func AppendCounterDataSamples(result *CounterSamplingResult, name string, data objc.ID, encoderIndex, commandIndex int) error {
	if result == nil {
		return fmt.Errorf("counter sampling result is nil")
	}
	if name == "" {
		return fmt.Errorf("counter name is empty")
	}
	values, err := CounterDataValues(data)
	if err != nil {
		return fmt.Errorf("read counter %q: %w", name, err)
	}
	for _, value := range values {
		result.Samples = append(result.Samples, CounterSample{
			Index:        len(result.Samples),
			Values:       map[string]float64{name: value},
			EncoderIndex: encoderIndex,
			CommandIndex: commandIndex,
		})
	}
	result.SampleCount = len(result.Samples)
	return nil
}
