//go:build darwin

package replay

import (
	"testing"
)

func TestQueryDeviceCapabilities(t *testing.T) {
	caps, err := QueryDeviceCapabilities()
	if err != nil {
		t.Skipf("No Metal device: %v", err)
	}

	t.Logf("Device capabilities:\n%s", caps)

	// Verify we got sensible values
	if caps.Name == "" {
		t.Error("Expected device name")
	}

	if caps.MaxTotalThreadsPerGroup == 0 {
		t.Error("Expected non-zero MaxTotalThreadsPerGroup")
	}

	if caps.MaxBufferLength == 0 {
		t.Error("Expected non-zero MaxBufferLength")
	}

	// Apple Silicon should support stage boundary sampling
	if !caps.SupportsCounterSamplingAtStageBoundary {
		t.Log("Warning: Device doesn't support stage boundary counter sampling")
	}

	// Log GPU family support
	t.Log("GPU Family support:")
	for family, supported := range caps.SupportsFamily {
		if supported {
			t.Logf("  %s: supported", family)
		}
	}
}

func TestValidateDispatch(t *testing.T) {
	caps, err := QueryDeviceCapabilities()
	if err != nil {
		t.Skipf("No Metal device: %v", err)
	}

	tests := []struct {
		name       string
		tX, tY, tZ int // threads
		gX, gY, gZ int // threadgroup
		wantErr    bool
	}{
		{"valid small", 32, 1, 1, 32, 1, 1, false},
		{"valid 2D", 64, 64, 1, 8, 8, 1, false},
		{"zero threadgroup", 32, 1, 1, 0, 1, 1, true},
		{"zero grid", 0, 1, 1, 32, 1, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := caps.ValidateDispatch(tt.tX, tt.tY, tt.tZ, tt.gX, tt.gY, tt.gZ)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDispatch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBufferSize(t *testing.T) {
	caps, err := QueryDeviceCapabilities()
	if err != nil {
		t.Skipf("No Metal device: %v", err)
	}

	// Valid size
	if err := caps.ValidateBufferSize(1024); err != nil {
		t.Errorf("1KB buffer should be valid: %v", err)
	}

	// Max size should be valid
	if err := caps.ValidateBufferSize(caps.MaxBufferLength); err != nil {
		t.Errorf("Max buffer size should be valid: %v", err)
	}

	// Over max should fail
	if err := caps.ValidateBufferSize(caps.MaxBufferLength + 1); err == nil {
		t.Error("Buffer over max should fail")
	}
}

func TestValidateDispatchEdgeCases(t *testing.T) {
	caps, err := QueryDeviceCapabilities()
	if err != nil {
		t.Skipf("No Metal device: %v", err)
	}

	// Test at the exact limit for threadgroup
	maxX := int(caps.MaxThreadsPerThreadgroup.Width)
	if err := caps.ValidateDispatch(1024, 1, 1, maxX, 1, 1); err != nil {
		t.Errorf("Dispatch at max X should be valid: %v", err)
	}

	// Test over the limit
	if err := caps.ValidateDispatch(1024, 1, 1, maxX+1, 1, 1); err == nil {
		t.Error("Dispatch over max X should fail")
	}

	// Test product exceeds total (e.g., 32*32*2 = 2048 > 1024)
	if caps.MaxTotalThreadsPerGroup == 1024 {
		if err := caps.ValidateDispatch(1024, 1, 1, 32, 32, 2); err == nil {
			t.Error("Threadgroup product exceeding max should fail")
		}
	}
}
