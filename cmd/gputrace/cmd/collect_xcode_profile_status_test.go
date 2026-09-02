//go:build darwin

package cmd

import "testing"

func TestPerformanceDataReady(t *testing.T) {
	tests := []struct {
		name                string
		showPerformance     bool
		performanceControls bool
		want                bool
	}{
		{name: "summary", showPerformance: true, want: true},
		{name: "performance view", performanceControls: true, want: true},
		{name: "not profiled", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := performanceDataReady(test.showPerformance, test.performanceControls)
			if got != test.want {
				t.Fatalf("performanceDataReady(%t, %t) = %t, want %t", test.showPerformance, test.performanceControls, got, test.want)
			}
		})
	}
}
