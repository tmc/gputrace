//go:build darwin

package cmd

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/xcodebindings"
)

func TestWriteXcodeBindingsTextStatesProbeBoundary(t *testing.T) {
	report := xcodebindings.Report{
		FrameworkPath: "/Xcode/GTShaderProfiler",
		Framework:     true,
		Summary: map[string]int{
			"classes_present":   1,
			"classes_missing":   0,
			"selectors_present": 1,
			"selectors_missing": 0,
		},
		Classes: []xcodebindings.Class{{
			Name:    "Profiler",
			Present: true,
			Selectors: []xcodebindings.Selector{{
				Name:    "privateSelector:",
				Present: true,
			}},
		}},
	}

	var out strings.Builder
	if err := writeXcodeBindingsText(&out, report); err != nil {
		t.Fatalf("writeXcodeBindingsText: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "symbol availability only") {
		t.Fatalf("probe boundary missing:\n%s", got)
	}
	if strings.Contains(got, "privateSelector:") {
		t.Fatalf("default text dumps selector details:\n%s", got)
	}
	if !strings.Contains(got, "Selector details are available with --json.") {
		t.Fatalf("JSON detail hint missing:\n%s", got)
	}
}
