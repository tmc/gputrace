package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestWriteResidencyReport(t *testing.T) {
	r := &trace.ResidencyReport{
		Storage: []trace.StorageFootprint{
			{Mode: "shared", Buffers: 842, Bytes: 13 << 30},
			{Mode: "private", Buffers: 16, Bytes: 1 << 30},
		},
		Buffers:   858,
		Bytes:     14 << 30,
		Residency: trace.ResidencyCalls{NewResidencySet: 1},
	}
	var b bytes.Buffer
	writeResidencyReport(&b, r)
	got := b.String()

	for _, want := range []string{
		"shared", "private", "842", "total",
		"newResidencySet", "requestResidency", "addResidencySet",
		"not managed explicitly",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	// The reason this command exists is that a low decoded-dispatch fraction
	// was being read as a limit on these counts, which it is not. Saying so is
	// part of the output, not a comment in the source.
	if !strings.Contains(got, "does not bound these counts") {
		t.Errorf("report does not disclaim the dispatch-coverage confusion:\n%s", got)
	}
	// There is no residency-set membership in the capture, so a wired-bytes
	// figure would be invented. The output has to say that rather than let a
	// reader assume the allocated total is a wired total.
	if !strings.Contains(got, "no wired-bytes figure") {
		t.Errorf("report does not state that wired bytes are not derivable:\n%s", got)
	}
}

// A capture with no buffer records must read as an absent measurement, not as a
// program that allocates nothing.
func TestWriteResidencyReportEmpty(t *testing.T) {
	var b bytes.Buffer
	writeResidencyReport(&b, &trace.ResidencyReport{})
	got := b.String()
	if !strings.Contains(got, "no buffer-creation records") {
		t.Errorf("empty report does not disclaim:\n%s", got)
	}
	if strings.Contains(got, "total") {
		t.Errorf("empty report printed a total row:\n%s", got)
	}
}

func TestWriteResidencyReportUnsized(t *testing.T) {
	var b bytes.Buffer
	writeResidencyReport(&b, &trace.ResidencyReport{
		Storage: []trace.StorageFootprint{{Mode: "shared", Buffers: 2, Bytes: 128}},
		Buffers: 2, Bytes: 128, Unsized: 1,
	})
	if got := b.String(); !strings.Contains(got, "understate") {
		t.Errorf("unsized records were not disclosed:\n%s", got)
	}
}

func TestResidencyCommandRegistered(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "residency" {
			return
		}
	}
	t.Error("residency command is not registered on the root command")
}

// The empty report must not assert that the driver is managing residency. With
// nothing decoded, that claim has no evidence behind it.
func TestWriteResidencyReportEmptyDoesNotClaimDriverResidency(t *testing.T) {
	var b bytes.Buffer
	writeResidencyReport(&b, &trace.ResidencyReport{})
	got := b.String()
	if strings.Contains(got, "driver's automatic") {
		t.Errorf("empty report claims the driver manages residency:\n%s", got)
	}
	if !strings.Contains(got, "not the same as") {
		t.Errorf("empty report does not distinguish absent records from absent residency:\n%s", got)
	}
}
