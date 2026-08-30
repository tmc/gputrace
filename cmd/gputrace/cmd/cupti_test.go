package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cuptitrace"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// The shim writes the mangled symbol into raw_symbol and leaves name
// empty, which is what every real CUDA capture looks like.
const shimRecords = `{"kind":"kernel","raw_symbol":"_Z5saxpyifPfS_","start_ns":100,"end_ns":300,"stream_id":7}
{"kind":"kernel","raw_symbol":"_Z5saxpyifPfS_","start_ns":400,"end_ns":500,"stream_id":7}
{"kind":"kernel","raw_symbol":"_Z4gemvifPfS_","start_ns":600,"end_ns":900,"stream_id":7}
`

func readRecords(t *testing.T, records string) gpuevent.Capture {
	t.Helper()
	cap, err := gpuevent.DecodeJSONL(strings.NewReader(records))
	if err != nil {
		t.Fatal(err)
	}
	return cap
}

func captureOutput(t *testing.T, run func(*cobra.Command) error) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := run(cmd); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestCuptiSummariesResolveNamesLikeEveryOtherReader pins the fix for two
// readers of one bundle disagreeing. --stats and --top grouped on Event.Name
// alone, which the shim never sets, so a capture of twenty named kernels
// summarized as "1 distinct kernels" with a blank name column — while pprof
// and the Perfetto writer, reading the same bundle, resolved them all.
func TestCuptiSummariesResolveNamesLikeEveryOtherReader(t *testing.T) {
	cap := readRecords(t, shimRecords)
	health := gpuevent.MeasureCompleteness(cap)

	stats := captureOutput(t, func(c *cobra.Command) error {
		return printCuptiStats(c, cap.Events, health)
	})
	if strings.Contains(stats, "1 distinct kernels") {
		t.Errorf("--stats collapsed two kernels into one bucket:\n%s", stats)
	}
	if !strings.Contains(stats, "2 distinct kernels") {
		t.Errorf("--stats does not report 2 distinct kernels:\n%s", stats)
	}

	top := captureOutput(t, func(c *cobra.Command) error {
		return printCuptiTop(c, cap.Events, 3)
	})
	// The same name each reader shows, so a --top row can be matched
	// against a pprof frame or a Perfetto slice by eye.
	for _, e := range cap.Events {
		want := cuptitrace.ShortName(cuptitrace.Demangle(cuptitrace.DisplayName(e)))
		if !strings.Contains(top, want) {
			t.Errorf("--top does not name %q:\n%s", want, top)
		}
		if !strings.Contains(stats, want) {
			t.Errorf("--stats does not name %q:\n%s", want, stats)
		}
	}
}

// TestCuptiStatsDeclaresAnIncompleteCapture: the totals a partial capture
// produces are well formed and wrong, so the reader has to say so before
// printing them.
func TestCuptiStatsDeclaresAnIncompleteCapture(t *testing.T) {
	cap := readRecords(t, `{"kind":"dropped","records":900}
`+shimRecords)
	out := captureOutput(t, func(c *cobra.Command) error {
		return printCuptiStats(c, cap.Events, gpuevent.MeasureCompleteness(cap))
	})
	for _, want := range []string{"INCOMPLETE", "900"} {
		if !strings.Contains(out, want) {
			t.Errorf("--stats output does not mention %q:\n%s", want, out)
		}
	}

	// A complete capture stays quiet: a warning on every run is a warning
	// nobody reads on the run that matters.
	clean := readRecords(t, shimRecords)
	out = captureOutput(t, func(c *cobra.Command) error {
		return printCuptiStats(c, clean.Events, gpuevent.MeasureCompleteness(clean))
	})
	if strings.Contains(out, "INCOMPLETE") {
		t.Errorf("a complete capture was reported incomplete:\n%s", out)
	}
}
