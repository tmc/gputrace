//go:build darwin

package cmd

import (
	"testing"
	"time"
)

func TestValidateXcodeProfileOptions(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		wait    time.Duration
		wantErr string
	}{
		{
			name:    "default",
			timeout: 5 * time.Minute,
		},
		{
			name:    "positive wait",
			timeout: time.Second,
			wait:    time.Minute,
		},
		{
			name:    "zero timeout",
			wait:    time.Second,
			wantErr: "--timeout must be > 0",
		},
		{
			name:    "negative timeout",
			timeout: -time.Second,
			wantErr: "--timeout must be > 0",
		},
		{
			name:    "negative wait",
			timeout: time.Second,
			wait:    -time.Second,
			wantErr: "--wait must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateXcodeProfileOptions(tt.timeout, tt.wait)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateXcodeProfileOptions: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateXcodeProfileOptions succeeded, want %q", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
