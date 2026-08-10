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

// TestAppsUnsetPrefersTheLoadedFramework pins the order to the bundle whose
// framework is actually mapped, which is Xcode.app: the generated
// gtshaderprofiler bindings dlopen it by an absolute path at package
// initialization.
//
// This assertion used to be the opposite, on the stated grounds that a release
// candidate "ships the newer dictionary and its GTShaderProfiler is the one
// internal/agxps loads". The second half was false, and it is what made the
// default split: names came from the release candidate while the numbers came
// from Xcode.app. Newer names describing a binary that is not measuring is the
// defect, not the fix.
func TestAppsUnsetPrefersTheLoadedFramework(t *testing.T) {
	t.Setenv(AppEnv, "")
	apps := Apps()
	if len(apps) < 2 {
		t.Fatalf("Apps() = %v, want several candidates", apps)
	}
	if apps[0] != "/Applications/Xcode.app" {
		t.Errorf("Apps()[0] = %q, want /Applications/Xcode.app: it is the bundle the "+
			"generated bindings dlopen, and the catalog has to follow the framework", apps[0])
	}
}

// TestFrameworkAndCatalogAgree is the invariant the split-brain violated: one
// setting has to move both halves. Before this, the catalog followed
// GPUTRACE_XCODE_APP and the framework was a hardcoded constant, so a pin moved
// the counter names without moving the binary that produced the counters.
func TestFrameworkAndCatalogAgree(t *testing.T) {
	for _, app := range []string{"/tmp/Fake.app", "/Applications/Xcode-rc.app"} {
		t.Setenv(AppEnv, app)
		for _, p := range append(FrameworkPaths(), CounterGraphPaths()...) {
			if !strings.HasPrefix(p, app+"/") {
				t.Errorf("with %s=%s, resolved %q from another bundle", AppEnv, app, p)
			}
		}
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
