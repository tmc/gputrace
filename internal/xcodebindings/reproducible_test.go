package xcodebindings

import (
	"reflect"
	"testing"
)

// RequireReproducible runs fn twice and asserts that both runs return identical
// results. This enforces the two-run determinism rule for binding probes.
func RequireReproducible[T any](t *testing.T, name string, fn func() T) T {
	t.Helper()
	run1 := fn()
	run2 := fn()
	if !reflect.DeepEqual(run1, run2) {
		t.Fatalf("%s failed reproducibility check:\nrun 1: %#v\nrun 2: %#v", name, run1, run2)
	}
	return run1
}
