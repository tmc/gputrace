//go:build darwin

package xcodebindings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrameworkPathForDeveloperDir(t *testing.T) {
	developer := filepath.Join("Applications", "Xcode-beta.app", "Contents", "Developer")
	want := filepath.Join("Applications", "Xcode-beta.app", "Contents", "PlugIns", "GPUDebugger.ideplugin",
		"Contents", "Frameworks", "GTShaderProfiler.framework", "Versions", "A", "GTShaderProfiler")
	if got := frameworkPathForDeveloperDir(developer); got != want {
		t.Fatalf("frameworkPathForDeveloperDir(%q) = %q, want %q", developer, got, want)
	}
}

func TestFrameworkOverridePaths(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "GTShaderProfiler.framework")
	if err := os.Mkdir(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := frameworkOverridePaths(bundle)
	want := filepath.Join(bundle, "Versions", "A", "GTShaderProfiler")
	if len(paths) != 2 || paths[0] != want {
		t.Fatalf("frameworkOverridePaths(%q) = %v, want first %q", bundle, paths, want)
	}
}
