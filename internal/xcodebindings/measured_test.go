package xcodebindings

import (
	"testing"
)

func TestMeasured(t *testing.T) {
	tests := []struct {
		name   string
		m      Measured[uint64]
		wantOK bool
		wantV  uint64
	}{
		{
			name:   "measured zero",
			m:      MeasuredVal(uint64(0)),
			wantOK: true,
			wantV:  0,
		},
		{
			name:   "unmeasured",
			m:      Unmeasured[uint64](),
			wantOK: false,
			wantV:  0,
		},
		{
			name:   "measured non-zero",
			m:      MeasuredVal(uint64(42)),
			wantOK: true,
			wantV:  42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.m.OK != tt.wantOK {
				t.Errorf("OK = %v, want %v", tt.m.OK, tt.wantOK)
			}
			if tt.m.V != tt.wantV {
				t.Errorf("V = %v, want %v", tt.m.V, tt.wantV)
			}
		})
	}
}
