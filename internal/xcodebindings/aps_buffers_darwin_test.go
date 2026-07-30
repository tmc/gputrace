//go:build darwin && gputrace_private_bindings

package xcodebindings

import "testing"

func TestAPSBufferAdaptersRejectInvalidInputs(t *testing.T) {
	if _, _, err := GetRDEBuffer(0, 0, 0, make([]byte, 1)); err == nil {
		t.Fatal("GetRDEBuffer accepted a nil processor")
	}
	if _, _, err := GetUSCBuffer(0, 0, make([]byte, 1)); err == nil {
		t.Fatal("GetUSCBuffer accepted a nil processor")
	}
}
