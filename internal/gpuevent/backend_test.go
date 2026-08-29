package gpuevent

import (
	"strings"
	"testing"

	"github.com/tmc/lib/nvidia/cupti"
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

// The candidate list must actually be a list. It previously existed but
// was unreachable past its first entry, so a host carrying only a
// versioned soname — the shape of a runtime-only CUDA install — reported
// no kernel tracing while tracing fine.
func TestCuptiCandidatesIncludeVersionedSonames(t *testing.T) {
	if len(cuptiCandidates) < 2 {
		t.Fatalf("cuptiCandidates = %v, want the versioned sonames too", cuptiCandidates)
	}
	if cuptiCandidates[0] != "libcupti.so" {
		t.Errorf("first candidate = %q, want the plain soname so the loader search path wins", cuptiCandidates[0])
	}
	var versioned int
	for _, c := range cuptiCandidates {
		if strings.Contains(c, ".so.") {
			versioned++
		}
	}
	if versioned == 0 {
		t.Error("no versioned soname among the candidates")
	}
}

// A true result must mean the library is actually loaded; on a host
// without CUPTI the claim holds vacuously.
func TestCuptiLoadableAgreesWithLoaded(t *testing.T) {
	if cuptiLoadable() && !cupti.Loaded() {
		t.Error("cuptiLoadable() reported success without the library loaded")
	}
}
