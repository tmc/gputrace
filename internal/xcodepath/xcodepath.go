// Package xcodepath locates resources inside an installed Xcode bundle.
//
// More than one Xcode is often installed, and they do not ship the same data.
// The GPUCounterGraph.plist in Xcode.app 17.2 (December 2025) defines 455
// counters and 50 timeline groups; the one in Xcode-rc.app (April 2026)
// defines 456 and 51, adding "Compressed Texture Write Inefficiency". Reading
// the counter dictionary from one bundle while loading GTShaderProfiler from
// another labels a capture with names from a different release, and nothing in
// the output says so.
//
// So callers do not hardcode /Applications/Xcode.app. They ask for a resource
// and report the Path they actually got.
package xcodepath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AppEnv names the environment variable that pins the bundle. It is the same
// variable the capture commands already use, and it now also selects the
// GTShaderProfiler that [FrameworkPath] returns, so one setting selects the
// Xcode that drives a capture, loads the framework, and explains its counters.
const AppEnv = "GPUTRACE_XCODE_APP"

// candidateApps are fallback bundles after explicit framework paths,
// DEVELOPER_DIR, and xcode-select have been considered.
var candidateApps = []string{
	"/Applications/Xcode.app",
	"/Applications/Xcode-rc.app",
	"/Applications/Xcode-beta.app",
}

// counterGraphRelative are the places GPUCounterGraph.plist appears within a
// bundle. Within one bundle these are copies of each other; across bundles
// they are not.
var counterGraphRelative = []string{
	"Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/Resources/GPUCounterGraph.plist",
	"Contents/PlugIns/GPUDebugger.ideplugin/Contents/Resources/GPUCounterGraph.plist",
	"Contents/Applications/Instruments.app/Contents/PlugIns/GPUPlugin.xrplugin/Contents/Resources/GPUCounterGraph.plist",
}

// Apps returns bundles in the generated framework loader's preference order.
// When AppEnv is set it is the only candidate: a pin that silently falls back
// to another Xcode would defeat the point of pinning.
func Apps() []string {
	if app := os.Getenv(AppEnv); app != "" {
		return []string{app}
	}
	var apps []string
	for _, path := range filepath.SplitList(os.Getenv("APPLE_GTSHADERPROFILER_FRAMEWORK_PATH")) {
		apps = appendUnique(apps, xcodeAppForPath(path))
	}
	if developerDir := os.Getenv("GPUTRACE_XCODE_DEVELOPER_DIR"); developerDir != "" {
		apps = appendUnique(apps, xcodeAppForDeveloperDir(developerDir))
	} else if developerDir := os.Getenv("DEVELOPER_DIR"); developerDir != "" {
		apps = appendUnique(apps, xcodeAppForDeveloperDir(developerDir))
	} else {
		apps = appendUnique(apps, xcodeAppForDeveloperDir(activeDeveloperDir()))
	}
	for _, app := range candidateApps {
		apps = appendUnique(apps, app)
	}
	return apps
}

var findDeveloperDir = func() string {
	out, err := exec.Command("xcode-select", "--print-path").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func activeDeveloperDir() string {
	return findDeveloperDir()
}

func xcodeAppForDeveloperDir(developerDir string) string {
	developerDir = filepath.Clean(strings.TrimSpace(developerDir))
	if filepath.Base(developerDir) != "Developer" || filepath.Base(filepath.Dir(developerDir)) != "Contents" {
		return ""
	}
	return filepath.Dir(filepath.Dir(developerDir))
}

func xcodeAppForPath(path string) string {
	for path = filepath.Clean(strings.TrimSpace(path)); path != "." && path != string(filepath.Separator); path = filepath.Dir(path) {
		if strings.HasSuffix(path, ".app") {
			return path
		}
	}
	return ""
}

func appendUnique(paths []string, path string) []string {
	if path == "" {
		return paths
	}
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

// CounterGraphPaths returns every GPUCounterGraph.plist location to try, in
// preference order. It does not check for existence; callers take the first
// that reads.
func CounterGraphPaths() []string {
	apps := Apps()
	paths := make([]string, 0, len(apps)*len(counterGraphRelative))
	for _, app := range apps {
		for _, rel := range counterGraphRelative {
			paths = append(paths, filepath.Join(app, rel))
		}
	}
	return paths
}

// frameworkRelative is where GTShaderProfiler lives within a bundle.
const frameworkRelative = "Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler"

// FrameworkPaths returns every GTShaderProfiler to try, in preference order,
// from the same bundles [CounterGraphPaths] reads. Resolving the framework and
// the catalog from one list is what keeps a capture's numbers and its counter
// names in the same release.
func FrameworkPaths() []string {
	apps := Apps()
	paths := make([]string, 0, len(apps))
	for _, app := range apps {
		paths = append(paths, filepath.Join(app, frameworkRelative))
	}
	return paths
}

// FrameworkPath returns the first GTShaderProfiler that exists, or "" when none
// does. Callers that must pass some path to a loader should treat "" as "let
// the loader report it", not substitute a bundle of their own.
//
// Note that the generated gtshaderprofiler bindings dlopen /Applications/Xcode.app
// themselves at package initialization. Pinning AppEnv to another bundle
// changes what this returns but does not unload theirs, so a pinned run can
// still have that framework resident. Reading a resolved path back from a
// loaded class is the only way to know which one answered.
func FrameworkPath() string {
	for _, p := range FrameworkPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// CounterGraphPath returns the GPUCounterGraph.plist belonging to the same
// bundle [FrameworkPath] resolved to, or "" when that bundle has none. An empty
// result is not an error: the counter dictionary is enrichment, and callers
// work without it.
//
// Scanning every bundle independently, as this used to, resolves the two halves
// separately: with more than one Xcode installed, a bundle missing the plist
// would take the framework from one release and the counter names from another.
// That mismatch is invisible in the output -- the numbers are real and the
// labels are plausible, they just describe different releases. Following the
// framework keeps a capture's numbers and its counter names together.
// When no candidate bundle has the framework there is nothing to stay in sync
// with, so the search widens to every bundle again.
func CounterGraphPath() string {
	if app := frameworkApp(); app != "" {
		for _, rel := range counterGraphRelative {
			p := filepath.Join(app, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	for _, p := range CounterGraphPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// frameworkApp returns the bundle holding the GTShaderProfiler that
// [FrameworkPath] resolves to, or "" when no candidate has one.
func frameworkApp() string {
	for _, app := range Apps() {
		if _, err := os.Stat(filepath.Join(app, frameworkRelative)); err == nil {
			return app
		}
	}
	return ""
}
