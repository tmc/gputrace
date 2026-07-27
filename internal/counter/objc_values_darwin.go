//go:build darwin

package counter

import (
	"fmt"

	"github.com/tmc/apple/objc"
)

// CounterDataValues converts a GTMioCounterData object to Go-owned numbers.
// The object must respond to sampleCount, valueType, and values. The private
// valueType is read for validation diagnostics; NSNumber's doubleValue is used
// for the numeric conversion so integer and floating-point counter profiles
// share one Go representation.
func CounterDataValues(data objc.ID) ([]float64, error) {
	var values []float64
	var err error
	objc.AutoreleasePool(func() {
		values, err = counterDataValues(data)
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

func counterDataValues(data objc.ID) ([]float64, error) {
	if data == 0 {
		return nil, fmt.Errorf("counter data is nil")
	}
	for _, selector := range []string{"sampleCount", "valueType", "values"} {
		if !objc.RespondsToSelector(data, objc.Sel(selector)) {
			return nil, fmt.Errorf("counter data does not respond to %s", selector)
		}
	}

	sampleCount := objc.Send[uint64](data, objc.Sel("sampleCount"))
	valueType := objc.Send[uint64](data, objc.Sel("valueType"))
	values := objc.Send[objc.ID](data, objc.Sel("values"))
	if values == 0 || !objc.RespondsToSelector(values, objc.Sel("count")) {
		if sampleCount == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("counter data value type %d has no values", valueType)
	}

	valueCount := objc.Send[uint64](values, objc.Sel("count"))
	if valueCount < sampleCount {
		return nil, fmt.Errorf("counter data value type %d has %d values, want %d", valueType, valueCount, sampleCount)
	}
	result := make([]float64, sampleCount)
	for i := uint64(0); i < sampleCount; i++ {
		var value objc.ID
		var numeric bool
		objc.AutoreleasePool(func() {
			value = objc.Send[objc.ID](values, objc.Sel("objectAtIndex:"), i)
			if value != 0 && objc.RespondsToSelector(value, objc.Sel("doubleValue")) {
				result[i] = objc.Send[float64](value, objc.Sel("doubleValue"))
				numeric = true
			}
		})
		if !numeric {
			return nil, fmt.Errorf("counter data value type %d contains nonnumeric value at index %d", valueType, i)
		}
	}
	return result, nil
}
