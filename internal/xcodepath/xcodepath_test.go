package xcodepath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppsPinIsExclusive(t *testing.T) {
	// A pin that falls back to another Xcode would report counter names from a
	// release the caller did not ask for, which is the failure this package
	// exists to prevent.
	t.Setenv(AppEnv, "/Applications/Xcode-rc.app")
	apps := Apps()
	if len(apps) != 1 || apps[0] != "/Applications/Xcode-rc.app" {
		t.Fatalf("Apps() = %v, want exactly the pinned bundle", apps)
	}
}

func TestAppsUnsetPrefersReleaseCandidate(t *testing.T) {
	t.Setenv(AppEnv, "")
	apps := Apps()
	if len(apps) < 2 {
		t.Fatalf("Apps() = %v, want several candidates", apps)
	}
	if !strings.Contains(apps[0], "Xcode-rc.app") {
		t.Errorf("Apps()[0] = %q, want the release candidate first: it ships the newer "+
			"dictionary and its GTShaderProfiler is the one internal/agxps loads", apps[0])
	}
}

func TestCounterGraphPathsCoverEveryBundle(t *testing.T) {
	t.Setenv(AppEnv, "/tmp/Fake.app")
	paths := CounterGraphPaths()
	if len(paths) != len(counterGraphRelative) {
		t.Fatalf("got %d paths for one bundle, want %d", len(paths), len(counterGraphRelative))
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/tmp/Fake.app/") {
			t.Errorf("path %q escaped the pinned bundle", p)
		}
	}
}

func TestCounterGraphPathFindsFirstThatExists(t *testing.T) {
	// Build a bundle where only the second candidate location exists, so a
	// loader that just returns paths[0] without checking would fail here.
	app := t.TempDir()
	want := filepath.Join(app, counterGraphRelative[1])
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(AppEnv, app)
	if got := CounterGraphPath(); got != want {
		t.Errorf("CounterGraphPath() = %q, want %q", got, want)
	}
}

func TestCounterGraphPathEmptyWhenAbsent(t *testing.T) {
	// Absence is not an error: the dictionary is enrichment. Callers must be
	// able to tell "not installed" from a path they should try to read.
	t.Setenv(AppEnv, t.TempDir())
	if got := CounterGraphPath(); got != "" {
		t.Errorf("CounterGraphPath() = %q, want empty for a bundle with no plist", got)
	}
}
