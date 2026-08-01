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
	"path/filepath"
)

// AppEnv names the environment variable that pins the bundle. It is the same
// variable the capture commands already use, so one setting selects the Xcode
// that both drives a capture and explains its counters.
const AppEnv = "GPUTRACE_XCODE_APP"

// candidateApps are the bundles searched when AppEnv is unset, in preference
// order. A release candidate sorts first: it is the newer data, and it is the
// build whose GTShaderProfiler internal/agxps loads.
var candidateApps = []string{
	"/Applications/Xcode-rc.app",
	"/Applications/Xcode.app",
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

// Apps returns the bundles to search, most preferred first. When AppEnv is set
// it is the only candidate: a pin that silently falls back to another Xcode
// would defeat the point of pinning.
func Apps() []string {
	if app := os.Getenv(AppEnv); app != "" {
		return []string{app}
	}
	return candidateApps
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

// CounterGraphPath returns the first GPUCounterGraph.plist that exists, or ""
// when none does. An empty result is not an error: the counter dictionary is
// enrichment, and callers work without it.
func CounterGraphPath() string {
	for _, p := range CounterGraphPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
