//go:build darwin

package cmd

import (
	"reflect"
	"testing"
)

func TestBuildXctraceRecordArgvIncludesRepeatedInstruments(t *testing.T) {
	oldTemplate := xctraceRecordTemplate
	oldInstruments := xctraceRecordInstruments
	oldTimeLimit := xctraceRecordTimeLimit
	oldOutput := xctraceRecordOutput
	oldAllProcesses := xctraceRecordAllProcesses
	oldAttach := xctraceRecordAttach
	defer func() {
		xctraceRecordTemplate = oldTemplate
		xctraceRecordInstruments = oldInstruments
		xctraceRecordTimeLimit = oldTimeLimit
		xctraceRecordOutput = oldOutput
		xctraceRecordAllProcesses = oldAllProcesses
		xctraceRecordAttach = oldAttach
	}()

	xctraceRecordTemplate = "Metal System Trace"
	xctraceRecordInstruments = []string{"Metal GPU Counters", "", "Metal Application"}
	xctraceRecordTimeLimit = "1s"
	xctraceRecordOutput = "/tmp/out.trace"
	xctraceRecordAllProcesses = true
	xctraceRecordAttach = ""

	got := buildXctraceRecordArgv(nil)
	want := []string{
		"xcrun", "xctrace", "record",
		"--template", "Metal System Trace",
		"--instrument", "Metal GPU Counters",
		"--instrument", "Metal Application",
		"--time-limit", "1s",
		"--output", "/tmp/out.trace",
		"--no-prompt",
		"--all-processes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
