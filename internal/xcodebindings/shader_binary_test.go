//go:build darwin

package xcodebindings

import "testing"

func TestNewShaderBinaryDataRejectsNilParent(t *testing.T) {
	if _, err := NewShaderBinaryData(0, 1, 0); err == nil {
		t.Fatal("NewShaderBinaryData accepted a nil parent")
	}
}

func TestNewShaderBinaryDataRejectsNilData(t *testing.T) {
	if _, err := NewShaderBinaryData(1, 0, 0); err == nil {
		t.Fatal("NewShaderBinaryData accepted nil binary data")
	}
}
