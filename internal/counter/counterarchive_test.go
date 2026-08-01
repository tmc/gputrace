package counter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCounterArchiveFromTrace checks the decode against a real archive. Set
// GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory to run it.
//
// The numbers below are not asserted as constants — they vary per capture.
// What is asserted are the invariants that a wrong record layout breaks:
// every blob's stride divides its length, the attributed samples all name an
// encoder of the capture, and machine-wide samples are counted separately
// rather than folded into per-encoder figures.
func TestCounterArchiveFromTrace(t *testing.T) {
	dir := os.Getenv("GPUTRACE_TEST_GPUPROFILER_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "streamData")); err != nil {
		t.Skipf("no streamData in %s", dir)
	}

	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatalf("ParseStreamData: %v", err)
	}
	a := stats.CounterArchive
	if a == nil {
		t.Fatal("no counter archive parsed from APSCounterData")
	}
	t.Logf("counter archive: %s", a)
	t.Logf("blobs=%d stride_mismatches=%d known_encoder_ids=%d passes=%d",
		a.Blobs, a.StrideMismatches, a.KnownEncoderIDs, len(a.PassColumns))

	if a.StrideMismatches != 0 {
		t.Errorf("%d blobs rejected: derived stride did not divide the blob", a.StrideMismatches)
	}
	if a.TotalSamples == 0 {
		t.Fatal("no samples decoded")
	}
	if a.AttributedSamples == 0 {
		t.Error("no sample attributed to an encoder of this capture")
	}
	if a.AttributedSamples+a.MachineWideSamples > a.TotalSamples {
		t.Errorf("attributed(%d) + machine-wide(%d) exceeds total(%d)",
			a.AttributedSamples, a.MachineWideSamples, a.TotalSamples)
	}
	if len(a.Encoders) == 0 {
		t.Fatal("no per-encoder attribution")
	}

	var counted int
	for _, e := range a.Encoders {
		counted += e.SampleCount
		if e.EncoderID == GRCMachineWideID {
			t.Errorf("machine-wide id %#x folded into per-encoder attribution", e.EncoderID)
		}
		if e.EndTicks < e.StartTicks {
			t.Errorf("encoder %#x has end before start", e.EncoderID)
		}
	}
	if counted != a.AttributedSamples {
		t.Errorf("per-encoder counts sum to %d, want %d", counted, a.AttributedSamples)
	}

	// Every pass column list must begin with the seven GRC columns; that is
	// what makes the fixed prefix of a record decodable.
	for i, cols := range a.PassColumns {
		if len(cols) < len(GRCColumnNames) {
			t.Errorf("pass %d has %d columns, fewer than the fixed seven", i, len(cols))
			continue
		}
		for j, name := range GRCColumnNames {
			if cols[j] != name {
				t.Errorf("pass %d column %d = %q, want %q", i, j, cols[j], name)
			}
		}
	}
}
