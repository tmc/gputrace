//go:build darwin

package cmd

import (
	"testing"
	"time"
)

// TestFileExportProbeBudget bounds how many times the automation opens Xcode's
// File menu while waiting for Export to become enabled.
//
// Every probe is a UI action, not an observation: the menu bar opens and takes
// key focus. At the previous fixed 500ms interval a two-minute wait flashed the
// menu about 240 times and made the machine unusable. This test fails if that
// budget creeps back up.
func TestFileExportProbeBudget(t *testing.T) {
	const window = 2 * time.Minute

	var elapsed time.Duration
	probes := 1 // the first probe happens before any delay
	for probes < maxFileExportProbes {
		elapsed += fileExportProbeDelay(probes)
		if elapsed > window {
			break
		}
		probes++
	}
	if probes > 20 {
		t.Errorf("%v of waiting costs %d File-menu opens, want <= 20", window, probes)
	}
	if elapsed < window {
		t.Errorf("the probe cap is reached after only %v; it must not cut a %v wait short", elapsed, window)
	}
}

// TestFileExportProbeDelayBackoff checks the schedule grows and then holds, so
// a long wait neither hammers the menu bar nor stops probing altogether.
func TestFileExportProbeDelayBackoff(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, 500 * time.Millisecond},
		{2, time.Second},
		{3, 2 * time.Second},
		{4, 4 * time.Second},
		{5, 8 * time.Second},
		{6, 8 * time.Second},
		{50, 8 * time.Second},
	} {
		if got := fileExportProbeDelay(test.attempt); got != test.want {
			t.Errorf("fileExportProbeDelay(%d) = %v, want %v", test.attempt, got, test.want)
		}
	}
}
