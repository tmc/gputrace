//go:build darwin

package counter

import (
	"fmt"
	"runtime"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// CounterDataValues converts a GTMioCounterData object to Go-owned numbers.
// The private values selector returns a double pointer, not an NSArray of
// NSNumbers, so callers must not reinterpret it as one.
func CounterDataValues(data objc.ID) ([]float64, error) {
	if data == 0 {
		return nil, fmt.Errorf("counter data is nil")
	}
	var values []float64
	var err error
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		values, err = gtshaderprofiler.GTMioCounterDataFromID(data).ValuesSlice()
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
