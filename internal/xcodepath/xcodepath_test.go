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

func TestXcodeAppForDeveloperDir(t *testing.T) {
	developer := "/Applications/Xcode-beta.app/Contents/Developer"
	if got := xcodeAppForDeveloperDir(developer); got != "/Applications/Xcode-beta.app" {
		t.Fatalf("xcodeAppForDeveloperDir(%q) = %q", developer, got)
	}
}

// TestAppsUnsetPrefersTheSelectedXcode pins the catalog to the Xcode whose
// framework the generated loader selects through xcode-select.
func TestAppsUnsetPrefersTheSelectedXcode(t *testing.T) {
	t.Setenv(AppEnv, "")
	saved := findDeveloperDir
	findDeveloperDir = func() string { return "/Applications/Xcode-beta.app/Contents/Developer" }
	t.Cleanup(func() { findDeveloperDir = saved })
	apps := Apps()
	if len(apps) < 2 {
		t.Fatalf("Apps() = %v, want several candidates", apps)
	}
	if apps[0] != "/Applications/Xcode-beta.app" {
		t.Errorf("Apps()[0] = %q, want the bundle selected by xcode-select", apps[0])
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

// TestCounterGraphFollowsFrameworkBundle pins the cross-bundle half of the
// split-brain. TestFrameworkAndCatalogAgree covers a pinned AppEnv, where only
// one bundle is a candidate; this covers the unpinned case, where the two
// halves used to be resolved by independent scans. Two installed Xcodes, the
// first missing only the catalog, made the catalog come from the second while
// the framework came from the first: real numbers under names from another
// release, with nothing in the output to show it.
func TestCounterGraphFollowsFrameworkBundle(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "Xcode.app")
	second := filepath.Join(root, "Xcode-rc.app")

	// first has the framework but no catalog; second has both.
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(first, frameworkRelative))
	write(filepath.Join(second, frameworkRelative))
	write(filepath.Join(second, counterGraphRelative[0]))

	saved := candidateApps
	candidateApps = []string{first, second}
	t.Cleanup(func() { candidateApps = saved })
	savedFind := findDeveloperDir
	findDeveloperDir = func() string { return "" }
	t.Cleanup(func() { findDeveloperDir = savedFind })
	t.Setenv(AppEnv, "")

	fw := FrameworkPath()
	if fw != filepath.Join(first, frameworkRelative) {
		t.Fatalf("FrameworkPath() = %q, want the framework in %s", fw, first)
	}
	if got := CounterGraphPath(); got != "" {
		t.Errorf("CounterGraphPath() = %q, want \"\": the framework resolved to %s, "+
			"which has no catalog, so naming counters from %s would mix releases",
			got, first, second)
	}
}
