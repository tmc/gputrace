// Package testtrace resolves the local GPU capture that integration tests run
// against.
//
// Captures are large and machine-specific, so they are not checked in and the
// tests that need one skip without it. Historically each test named its own
// environment variable, and a developer wanting to run them had to discover and
// set eight of those separately, all pointing into the same .gputrace bundle.
// In practice nobody did, so the suite stayed green by skipping and a change
// that broke those paths could land unnoticed.
//
// Set GPUTRACE_TEST_TRACE to a .gputrace bundle and every capture-shaped
// variable derives from it. The specific variables still win when set, so an
// unusual capture can still be aimed at one test.
package testtrace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BundleEnv names the single variable the derived paths come from.
const BundleEnv = "GPUTRACE_TEST_TRACE"

// Kind is the shape of path a test wants out of the capture.
type Kind int

const (
	// Bundle is the .gputrace directory itself.
	Bundle Kind = iota
	// ProfilerDir is the .gpuprofiler_raw directory inside the bundle. Its
	// name carries the original capture's name, so it cannot be constructed
	// by joining a constant and has to be found.
	ProfilerDir
	// StreamData is the streamData file inside ProfilerDir.
	StreamData
)

// Path returns the value of env, or a path of the requested kind derived from
// BundleEnv when env is unset. It returns "" when neither is available, which
// callers report as a skip.
//
// Derivation is best-effort: an unreadable or unrecognized bundle yields "" so
// the test skips, rather than a half-formed path that would fail deeper in with
// a less obvious message.
func Path(env string, kind Kind) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	bundle := os.Getenv(BundleEnv)
	if bundle == "" {
		return ""
	}
	switch kind {
	case Bundle:
		return bundle
	case ProfilerDir:
		return profilerDir(bundle)
	case StreamData:
		dir := profilerDir(bundle)
		if dir == "" {
			return ""
		}
		path := filepath.Join(dir, "streamData")
		if _, err := os.Stat(path); err != nil {
			return ""
		}
		return path
	}
	return ""
}

// profilerDir finds the .gpuprofiler_raw directory inside a bundle. The
// directory is named after the capture rather than by a fixed convention, so
// it is matched on suffix.
func profilerDir(bundle string) string {
	entries, err := os.ReadDir(bundle)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".gpuprofiler_raw") {
			return filepath.Join(bundle, e.Name())
		}
	}
	// Some bundles nest it a level down. Walk shallowly rather than assume,
	// but stop at the first match so a bundle holding several captures does
	// not silently pick one at random beyond the first.
	var found string
	filepath.WalkDir(bundle, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() && strings.HasSuffix(d.Name(), ".gpuprofiler_raw") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
