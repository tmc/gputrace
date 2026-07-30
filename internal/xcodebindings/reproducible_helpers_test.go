package xcodebindings

import (
	"testing"
)

func TestRequireReproducible(t *testing.T) {
	val := 42
	got := RequireReproducible(t, "deterministic int", func() int {
		return val
	})
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}
