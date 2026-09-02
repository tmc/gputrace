//go:build darwin

package counter

import (
	"testing"

	"github.com/tmc/apple/objc"
)

func TestAppendCounterDataSamplesRejectsInvalidInput(t *testing.T) {
	if err := AppendCounterDataSamples(nil, "cycles", 1, 0, 0); err == nil {
		t.Fatal("AppendCounterDataSamples accepted a nil result")
	}
	if err := AppendCounterDataSamples(&CounterSamplingResult{}, "", 1, 0, 0); err == nil {
		t.Fatal("AppendCounterDataSamples accepted an empty counter name")
	}
	if err := AppendCounterDataSamples(&CounterSamplingResult{}, "cycles", objc.ID(0), 0, 0); err == nil {
		t.Fatal("AppendCounterDataSamples accepted nil counter data")
	}
}
