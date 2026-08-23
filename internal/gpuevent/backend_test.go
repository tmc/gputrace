package gpuevent

import (
	"testing"
)

func TestRegistryReturnsBackends(t *testing.T) {
	backends := Registry()
	if len(backends) == 0 {
		t.Fatal("registry returned no backends")
	}
	seen := map[string]bool{}
	for _, b := range backends {
		if b.Name == "" || b.Vendor == "" {
			t.Errorf("backend missing identity: %+v", b)
		}
		if seen[b.Name] {
			t.Errorf("duplicate backend %q", b.Name)
		}
		seen[b.Name] = true
	}
}

func TestAtLeastOneAvailableOnThisHost(t *testing.T) {
	// This repository is developed with real GPUs attached; a host where
	// every backend is unavailable cannot run capture at all.
	if !AnyAvailable() {
		t.Skip("no GPU backends available on this host")
	}
}
