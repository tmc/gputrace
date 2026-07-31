package cmd

import "testing"

func TestUnattributedInventory(t *testing.T) {
	tests := []struct {
		name       string
		attributed int
		total      int
		want       bool
	}{
		{"nothing joined", 0, 490, true},
		{"all joined", 490, 490, false},
		{"partly joined", 12, 490, false},
		{"no dispatches at all", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unattributedInventory(tt.attributed, tt.total); got != tt.want {
				t.Errorf("unattributedInventory(%d, %d) = %v, want %v",
					tt.attributed, tt.total, got, tt.want)
			}
		})
	}
}

func TestFormatDispatchCount(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		attributed int
		total      int
		want       string
	}{
		// The case this exists for: the trace ran 490 dispatches and named
		// none of them, so every inventory row sits at zero.
		{"unjoinable trace", 0, 0, 490, "—"},
		// A genuine zero, in a trace whose other rows did join. Here the
		// kernel really was not dispatched, and zero is the measurement.
		{"real zero", 0, 12, 490, "0"},
		{"counted row", 56, 490, 490, "56"},
		// A nonzero count is never suppressed, even in an unjoinable trace:
		// something did attribute it, so the number stands.
		{"nonzero in unjoinable trace", 3, 0, 490, "3"},
		{"trace with no dispatches", 0, 0, 0, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDispatchCount(tt.count, tt.attributed, tt.total); got != tt.want {
				t.Errorf("formatDispatchCount(%d, %d, %d) = %q, want %q",
					tt.count, tt.attributed, tt.total, got, tt.want)
			}
		})
	}
}
