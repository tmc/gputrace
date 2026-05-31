//go:build darwin

package main

import "testing"

func TestMacgoConfigDevMode(t *testing.T) {
	tests := []struct {
		name string
		in   bool
		want bool
	}{
		{"enabled", true, true},
		{"disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := macgoConfig(tt.in)
			if cfg.DevMode != tt.want {
				t.Fatalf("DevMode = %v, want %v", cfg.DevMode, tt.want)
			}
		})
	}
}
