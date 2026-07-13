// Package xcodepath locates private frameworks in the selected Xcode app.
package xcodepath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultApp      = "/Applications/Xcode.app"
	developerSuffix = "/Contents/Developer"
	frameworkSuffix = "Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler"
)

// GTShaderProfiler returns the path to the GTShaderProfiler framework binary.
func GTShaderProfiler() string {
	return resolve(os.Getenv, selectedDeveloperDir)
}

func resolve(getenv func(string) string, developerDir func() (string, error)) string {
	if app := getenv("GPUTRACE_XCODE_APP"); app != "" {
		return filepath.Join(app, frameworkSuffix)
	}
	if dir, err := developerDir(); err == nil && strings.TrimSpace(dir) != "" {
		dir = filepath.Clean(strings.TrimSpace(dir))
		app := strings.TrimSuffix(dir, developerSuffix)
		return filepath.Join(app, frameworkSuffix)
	}
	return filepath.Join(defaultApp, frameworkSuffix)
}

func selectedDeveloperDir() (string, error) {
	out, err := exec.Command("xcode-select", "-p").Output()
	return string(out), err
}
